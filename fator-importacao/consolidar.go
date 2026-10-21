package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Faixa que a área de Procurement espera. Fora dela vira aviso, não erro:
// pedido pequeno estoura a faixa porque despesa fixa por processo não
// acompanha o valor da carga.
var Faixa = struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}{0.24, 0.30}

type Alvo struct {
	Mes   string `json:"mes"`
	Modal string `json:"modal"`
	NRef  string `json:"nref"`
	Dir   string `json:"dir"`
}

type Somas struct {
	Qtde     float64 `json:"qtde"`
	CIF      float64 `json:"cif"`
	II       float64 `json:"ii"`
	Siscomex float64 `json:"siscomex"`
}

type Pedido struct {
	Alvo
	Itens          []Item       `json:"-"`
	NumItens       int          `json:"itens"`
	Custos         Custos       `json:"custos"`
	Somas          Somas        `json:"somas"`
	Total          float64      `json:"total"`
	CIF            float64      `json:"cif"`
	Fator          float64      `json:"fator"`
	FatorLogistico float64      `json:"fatorLogistico"`
	Exportador     string       `json:"exportador"`
	PDFs           ResultadoPDF `json:"pdfs"`
}

type Erro struct {
	Pasta  string `json:"pasta"`
	Mes    string `json:"mes"`
	Modal  string `json:"modal"`
	NRef   string `json:"nref"`
	Motivo string `json:"motivo"`
}

type Agregado struct {
	Pedidos        int     `json:"pedidos"`
	Linhas         int     `json:"linhas"`
	CIF            float64 `json:"cif"`
	Total          float64 `json:"total"`
	FatorPonderado float64 `json:"fatorPonderado"`
	FatorLogistico float64 `json:"fatorLogistico"`
}

type Resultado struct {
	DryRun      bool                `json:"dryRun"`
	Gravado     bool                `json:"gravado"`
	Adicionados []*Pedido           `json:"adicionados"`
	Pulados     []string            `json:"pulados"`
	Erros       []Erro              `json:"erros"`
	Avisos      []*Pedido           `json:"avisos"`
	Totais      map[string]Agregado `json:"totais"`
}

func subpastas(dir string) []string {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return nil // pasta ausente não é erro aqui: a tela já avisa sobre a raiz
	}
	var nomes []string
	for _, e := range entradas {
		if e.IsDir() {
			nomes = append(nomes, e.Name())
		}
	}
	sort.Strings(nomes)
	return nomes
}

// Varrer percorre dados\<mês>\<modal>\<pedido>\ — modal em qualquer caixa.
func Varrer(raiz string) []Alvo {
	base := filepath.Join(raiz, "dados")
	var alvos []Alvo
	for _, mes := range subpastas(base) {
		for _, dirModal := range subpastas(filepath.Join(base, mes)) {
			modal := strings.ToUpper(dirModal)
			if modal != "AIR" && modal != "SEA" {
				continue
			}
			for _, nome := range subpastas(filepath.Join(base, mes, dirModal)) {
				alvos = append(alvos, Alvo{mes, modal, nome, filepath.Join(base, mes, dirModal, nome)})
			}
		}
	}
	return alvos
}

func arquivoDeMateriais(dir string) (string, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var cand []string
	for _, e := range entradas {
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		// ~$ é o arquivo de bloqueio que o Excel deixa com a planilha aberta.
		if !e.IsDir() && (ext == ".xlsx" || ext == ".csv") && !strings.HasPrefix(n, "~$") {
			cand = append(cand, n)
		}
	}
	sort.Strings(cand)
	switch {
	case len(cand) == 0:
		return "", fmt.Errorf("nenhum arquivo de materiais (.xlsx/.csv) na pasta")
	case len(cand) > 1:
		return "", fmt.Errorf("mais de um arquivo de materiais: %s", strings.Join(cand, ", "))
	}
	return filepath.Join(dir, cand[0]), nil
}

func ProcessarPedido(a Alvo) (*Pedido, error) {
	arq, err := arquivoDeMateriais(a.Dir)
	if err != nil {
		return nil, err
	}
	nref, itens, err := LerMateriais(arq)
	if err != nil {
		return nil, err
	}
	if len(itens) == 0 {
		return nil, fmt.Errorf("arquivo de materiais sem linhas com Descricao")
	}
	pdfs, err := CustosDoPedido(a.Dir)
	if err != nil {
		return nil, err
	}

	var s Somas
	exportador := ""
	for _, it := range itens {
		s.Qtde += it.Qtde
		s.CIF += it.CIF
		s.II += it.II
		s.Siscomex += it.Siscomex
		if exportador == "" {
			exportador = it.Exportador
		}
	}
	if s.CIF <= 0 {
		return nil, fmt.Errorf("CIF Total somado é zero — conversão de número suspeita")
	}

	// Só II e Tx. Siscomex. PIS/COFINS/ICMS são recuperáveis e ficam de fora.
	logistico := pdfs.Custos.Logistico()
	total := s.II + s.Siscomex + logistico

	p := &Pedido{
		Alvo: a, Itens: itens, NumItens: len(itens),
		Custos: pdfs.Custos, Somas: s, Total: total, CIF: s.CIF,
		Fator: total / s.CIF, FatorLogistico: logistico / s.CIF,
		Exportador: exportador, PDFs: pdfs,
	}
	p.NRef = nref // o nome do arquivo manda, não o da pasta
	return p, nil
}

type OpcoesConsolidar struct {
	Raiz, Planilha string
	Meses          []string
	DryRun         bool
	Reprocessar    bool
}

func Consolidar(o OpcoesConsolidar) (*Resultado, error) {
	alvo := map[string]bool{}
	for _, m := range o.Meses {
		alvo[m] = true
	}
	var candidatos []Alvo
	for _, a := range Varrer(o.Raiz) {
		if alvo[a.Mes] {
			candidatos = append(candidatos, a)
		}
	}

	pl, err := AbrirPlanilha(o.Planilha)
	if err != nil {
		return nil, err
	}
	defer pl.Close()

	existentes := map[string]map[string]bool{
		"AIR": pl.NRefsExistentes("AIR"),
		"SEA": pl.NRefsExistentes("SEA"),
	}
	if o.Reprocessar {
		for _, modal := range []string{"AIR", "SEA"} {
			nrefs := map[string]bool{}
			for _, a := range candidatos {
				if a.Modal == modal {
					nrefs[a.NRef] = true
				}
			}
			pl.RemoverNRefs(modal, nrefs)
			existentes[modal] = pl.NRefsExistentes(modal)
		}
	}

	r := &Resultado{
		DryRun: o.DryRun, Adicionados: []*Pedido{}, Pulados: []string{},
		Erros: []Erro{}, Avisos: []*Pedido{},
	}
	for _, a := range candidatos {
		if existentes[a.Modal][a.NRef] {
			r.Pulados = append(r.Pulados, a.NRef)
			continue
		}
		p, err := ProcessarPedido(a)
		if err != nil {
			// Pasta que falha não derruba a execução: registra e segue.
			r.Erros = append(r.Erros, Erro{a.Dir, a.Mes, a.Modal, a.NRef, err.Error()})
			continue
		}
		if existentes[a.Modal][p.NRef] {
			r.Pulados = append(r.Pulados, p.NRef)
			continue
		}
		if err := pl.GravarPedido(p); err != nil {
			r.Erros = append(r.Erros, Erro{a.Dir, a.Mes, a.Modal, p.NRef, err.Error()})
			continue
		}
		existentes[a.Modal][p.NRef] = true
		r.Adicionados = append(r.Adicionados, p)
		if p.Fator < Faixa.Min || p.Fator > Faixa.Max {
			r.Avisos = append(r.Avisos, p)
		}
	}

	precisaGravar := len(r.Adicionados) > 0 || o.Reprocessar
	if !o.DryRun && precisaGravar {
		if err := pl.SalvarPlanilha(o.Planilha); err != nil {
			return nil, err
		}
		r.Gravado = true
	}
	r.Totais = agregar(r.Adicionados)
	return r, nil
}

// Fator ponderado: soma dos custos / soma dos CIF. A média simples dá o mesmo
// peso a um pedido de R$ 4,5 mil e a um de R$ 925 mil, e engana.
func agregar(pedidos []*Pedido) map[string]Agregado {
	out := map[string]Agregado{}
	for _, modal := range []string{"AIR", "SEA"} {
		var ag Agregado
		var imposto float64
		for _, p := range pedidos {
			if p.Modal != modal {
				continue
			}
			ag.Pedidos++
			ag.Linhas += p.NumItens
			ag.CIF += p.Somas.CIF
			ag.Total += p.Total
			imposto += p.Somas.II + p.Somas.Siscomex
		}
		if ag.CIF > 0 {
			ag.FatorPonderado = ag.Total / ag.CIF
			ag.FatorLogistico = (ag.Total - imposto) / ag.CIF
		}
		out[modal] = ag
	}
	return out
}
