// Fator de importação para Windows: varre as pastas de pedidos, lê as notas de
// débito e consolida em ConsolidadoPath.xlsx. Serve a tela em 127.0.0.1 e abre
// numa janela própria do Edge ou do Chrome, como o mqtt-panel faz.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var assets embed.FS

const raizPadrao = `Y:\BR10\Procurement\Public\Teste fator importação\`

type Config struct {
	Raiz     string `json:"raiz"`
	Planilha string `json:"planilha,omitempty"`
}

var (
	appDir     string
	configPath string
	raizFlag   string

	mu        sync.Mutex // uma consolidação por vez: duas em paralelo corromperiam a planilha
	historico []*Execucao
)

type Execucao struct {
	ID        string     `json:"id"`
	Quando    string     `json:"quando"`
	DryRun    bool       `json:"dryRun"`
	Resultado *Resultado `json:"resultado,omitempty"`
	Texto     string     `json:"texto"`
}

// lerConfigBruta devolve o que está gravado. `planilha` ausente significa
// "derivar da raiz": assim trocar a raiz não deixa a planilha apontando para a
// pasta antiga.
func lerConfigBruta() Config {
	c := Config{Raiz: raizPadrao}
	if b, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if raizFlag != "" {
		c.Raiz = raizFlag
	}
	return c
}

func lerConfig() Config {
	c := lerConfigBruta()
	switch {
	case c.Planilha == "":
		c.Planilha = filepath.Join(c.Raiz, "ConsolidadoPath.xlsx")
	case !filepath.IsAbs(c.Planilha):
		c.Planilha = filepath.Join(c.Raiz, c.Planilha)
	}
	return c
}

func gravarConfig(c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, b, 0o644)
}

type MesResumo struct {
	Mes        string `json:"mes"`
	Total      int    `json:"total"`
	Existentes int    `json:"existentes"`
	Novos      int    `json:"novos"`
}

type HistResumo struct {
	ID          string `json:"id"`
	Quando      string `json:"quando"`
	DryRun      bool   `json:"dryRun"`
	Adicionados int    `json:"adicionados"`
	Pulados     int    `json:"pulados"`
	Erros       int    `json:"erros"`
}

func estado() map[string]any {
	c := lerConfig()
	var problemas []string

	dados := filepath.Join(c.Raiz, "dados")
	var alvos []Alvo
	// Caminho errado não pode parecer "nada a fazer": a tela ficaria vazia.
	if _, err := os.Stat(dados); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			problemas = append(problemas, fmt.Sprintf("a pasta %s não existe", dados))
		} else {
			problemas = append(problemas, fmt.Sprintf("não deu para ler %s: %v", dados, err))
		}
	} else {
		alvos = Varrer(c.Raiz)
	}

	jaNaPlanilha := map[string]map[string]bool{"AIR": {}, "SEA": {}}
	if pl, err := AbrirPlanilha(c.Planilha); err != nil {
		problemas = append(problemas, fmt.Sprintf("planilha de destino: %v", err))
	} else {
		jaNaPlanilha["AIR"] = pl.NRefsExistentes("AIR")
		jaNaPlanilha["SEA"] = pl.NRefsExistentes("SEA")
		pl.Close()
	}

	porMes := map[string]*MesResumo{}
	for _, a := range alvos {
		m, ok := porMes[a.Mes]
		if !ok {
			m = &MesResumo{Mes: a.Mes}
			porMes[a.Mes] = m
		}
		m.Total++
		if jaNaPlanilha[a.Modal][a.NRef] {
			m.Existentes++
		} else {
			m.Novos++
		}
	}
	meses := make([]*MesResumo, 0, len(porMes))
	for _, m := range porMes {
		meses = append(meses, m)
	}
	sort.Slice(meses, func(i, j int) bool { return meses[i].Mes < meses[j].Mes })

	hist := []HistResumo{}
	for i := len(historico) - 1; i >= 0; i-- {
		h := historico[i]
		hist = append(hist, HistResumo{h.ID, h.Quando, h.DryRun,
			len(h.Resultado.Adicionados), len(h.Resultado.Pulados), len(h.Resultado.Erros)})
	}

	var erro any
	if len(problemas) > 0 {
		erro = strings.Join(problemas, " · ")
	}
	return map[string]any{
		"cfg": c, "erro": erro, "faixa": Faixa, "meses": meses, "historico": hist,
	}
}

func relatorio(r *Resultado, c Config, quando string) string {
	cab := "Execução " + quando
	if r.DryRun {
		cab += " (conferência, nada gravado)"
	}
	L := []string{cab,
		"Pasta de dados: " + filepath.Join(c.Raiz, "dados"),
		"Planilha: " + c.Planilha, "",
		fmt.Sprintf("Adicionados: %d · Pulados (já na planilha): %d · Pastas com erro: %d",
			len(r.Adicionados), len(r.Pulados), len(r.Erros)), ""}

	for _, m := range []string{"AIR", "SEA"} {
		t := r.Totais[m]
		if t.Pedidos == 0 {
			continue
		}
		L = append(L, fmt.Sprintf("%s: %d pedidos, %d linhas de material, CIF R$ %s, custo R$ %s, fator ponderado %s, fator logístico %s",
			m, t.Pedidos, t.Linhas, FmtBRL(t.CIF), FmtBRL(t.Total), FmtPct(t.FatorPonderado), FmtPct(t.FatorLogistico)))
	}
	if len(r.Erros) > 0 {
		L = append(L, "", "Pastas que falharam:")
		for _, e := range r.Erros {
			L = append(L, "  "+e.Pasta+" -> "+e.Motivo)
		}
	}
	var falhasPdf []string
	for _, p := range r.Adicionados {
		for _, f := range p.PDFs.Falhas {
			falhasPdf = append(falhasPdf, fmt.Sprintf("  %s: %s -> %s", p.NRef, f.Arquivo, f.Motivo))
		}
	}
	if len(falhasPdf) > 0 {
		L = append(L, "", "PDFs que não abriram (pedido gravado sem eles):")
		L = append(L, falhasPdf...)
	}
	if len(r.Avisos) > 0 {
		L = append(L, "", fmt.Sprintf("Fora da faixa %s–%s (fator alto em pedido pequeno é esperado):",
			FmtPct(Faixa.Min), FmtPct(Faixa.Max)))
		for _, a := range r.Avisos {
			L = append(L, fmt.Sprintf("  %s [%s] CIF R$ %s -> %s", a.NRef, a.Modal, FmtBRL(a.CIF), FmtPct(a.Fator)))
		}
	}
	return strings.Join(L, "\n")
}

type pedidoRodar struct {
	Meses       []string `json:"meses"`
	DryRun      bool     `json:"dryRun"`
	Reprocessar bool     `json:"reprocessar"`
}

func rodar(req pedidoRodar) (*Execucao, error) {
	mu.Lock()
	defer mu.Unlock()

	c := lerConfig()
	quando := time.Now().Format("2006-01-02 15:04:05")
	r, err := Consolidar(OpcoesConsolidar{
		Raiz: c.Raiz, Planilha: c.Planilha,
		Meses: req.Meses, DryRun: req.DryRun, Reprocessar: req.Reprocessar,
	})
	if err != nil {
		return nil, err
	}
	e := &Execucao{
		ID: fmt.Sprint(len(historico) + 1), Quando: quando,
		DryRun: r.DryRun, Resultado: r, Texto: relatorio(r, c, quando),
	}
	historico = append(historico, e)

	// Log em arquivo é um extra: não pode derrubar a execução.
	dir := filepath.Join(appDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err == nil {
		nome := "run-" + strings.NewReplacer(":", "-", " ", "-").Replace(quando) + ".txt"
		_ = os.WriteFile(filepath.Join(dir, nome), []byte(e.Texto), 0o644)
	}
	return e, nil
}

func escreverJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func erroJSON(w http.ResponseWriter, code int, msg string) {
	escreverJSON(w, code, map[string]string{"erro": msg})
}

func handler() http.Handler {
	html, _ := assets.ReadFile("ui.html")
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	})
	mux.HandleFunc("GET /api/estado", func(w http.ResponseWriter, r *http.Request) {
		escreverJSON(w, http.StatusOK, estado())
	})
	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		var body Config
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			erroJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		c := lerConfigBruta()
		if raiz := strings.TrimSpace(body.Raiz); raiz != "" {
			c.Raiz = raiz
			if strings.TrimSpace(body.Planilha) == "" {
				c.Planilha = "" // volta a derivar da raiz nova
			}
		}
		if pl := strings.TrimSpace(body.Planilha); pl != "" {
			c.Planilha = pl
		}
		if err := gravarConfig(c); err != nil {
			erroJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		escreverJSON(w, http.StatusOK, estado())
	})
	mux.HandleFunc("POST /api/rodar", func(w http.ResponseWriter, r *http.Request) {
		var body pedidoRodar
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			erroJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		e, err := rodar(body)
		if err != nil {
			erroJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		// A tela espera os campos do resultado no mesmo nível de id/quando/texto.
		saida := map[string]any{}
		b, _ := json.Marshal(e.Resultado)
		_ = json.Unmarshal(b, &saida)
		saida["id"], saida["quando"], saida["texto"] = e.ID, e.Quando, e.Texto
		escreverJSON(w, http.StatusOK, saida)
	})
	mux.HandleFunc("GET /api/log/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		for _, h := range historico {
			if h.ID == id {
				escreverJSON(w, http.StatusOK, h)
				return
			}
		}
		erroJSON(w, http.StatusNotFound, "execução não encontrada")
	})

	// A tela é servida só para quem está na própria máquina.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || (h != "127.0.0.1" && h != "::1") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// open abre a tela numa janela própria, sem barra de endereço e sem abas, para
// parecer aplicativo e não site. Mesma ordem de busca do mqtt-panel: numa
// máquina sem admin o Edge e o Chrome ficam em LocalAppData.
func open(url string) {
	if runtime.GOOS != "windows" {
		_ = exec.Command("xdg-open", url).Start()
		return
	}
	for _, dir := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData")} {
		if dir == "" {
			continue
		}
		for _, exe := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`} {
			p := filepath.Join(dir, exe)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			perfil := filepath.Join(os.Getenv("LocalAppData"), "FatorImportacao", "janela")
			cmd := exec.Command(p, "--app="+url, "--window-size=1180,920",
				"--user-data-dir="+perfil, "--no-first-run", "--no-default-browser-check")
			if cmd.Start() == nil {
				// Fechou a janela, fechou o app: nada de processo órfão.
				go func() { _ = cmd.Wait(); os.Exit(0) }()
				return
			}
		}
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func main() {
	porta := flag.Int("porta", 7317, "porta do servidor local")
	semJanela := flag.Bool("sem-janela", false, "não abrir a janela (usado em teste)")
	flag.StringVar(&raizFlag, "raiz", "", "pasta de dados, sobrepõe a configuração")
	cfg := flag.String("config", "", "caminho do config.json")
	flag.Parse()

	// Rodando como .exe portátil a config fica ao lado dele; por `go run`, no cwd.
	if exe, err := os.Executable(); err == nil && !strings.Contains(exe, "go-build") {
		appDir = filepath.Dir(exe)
	} else {
		appDir, _ = os.Getwd()
	}
	configPath = *cfg
	if configPath == "" {
		configPath = filepath.Join(appDir, "config.json")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *porta)
	url := "http://" + addr + "/"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Segunda execução não sobe outro servidor: traz a janela da que já está aberta.
		log.Printf("o app já está aberto; abrindo a janela da instância existente")
		if !*semJanela {
			open(url)
			select {} // o app encerra quando a janela fechar
		}
		return
	}

	log.Printf("Fator de importação em %s", url)
	log.Printf("Configuração: %s", configPath)
	if !*semJanela {
		open(url)
	}
	log.Fatal(http.Serve(ln, handler()))
}
