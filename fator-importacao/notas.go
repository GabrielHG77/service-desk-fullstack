package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Os regex do spec usam lookbehind, que o motor do Go não tem. Viram grupo de
// captura: mesma semântica (acha o rótulo, pega o primeiro número depois dele).
// Corpo idêntico ao validado nas notas reais. Sensíveis a maiúsculas, de propósito.
var (
	rxHonorarios  = regexp.MustCompile(`Despacho Importação[\s\S]*?SUBTOTAL TRIBUTÁVEL[^\d]*([\d.]+,\d{2})`)
	rxArmazenagem = regexp.MustCompile(`Armazenagem - destino[^\d]*([\d.]+,\d{2})`)
	rxMarinha     = regexp.MustCompile(`Marinha Mercante[^\d]*([\d.]+,\d{2})`)
	rxRodoviario  = regexp.MustCompile(`Transporte Rodoviário[^\d]*([\d.]+,\d{2})`)
	rxFreteIntl   = regexp.MustCompile(`SUBTOTAL NÃO TRIBUTÁVEL[^\d]*([\d.]+,\d{2})`)
	rxOrigem      = regexp.MustCompile(`Taxa Administrativa[\s\S]*?SUBTOTAL TRIBUTÁVEL[^\d]*([\d.]+,\d{2})`)
)

const (
	marcaNacional    = "Armazenagem - destino"
	marcaDespachante = "DESPACHANTES ADUANEIROS"
)

type Custos struct {
	Honorarios  float64 `json:"honorarios"`
	Armazenagem float64 `json:"armazenagem"`
	Marinha     float64 `json:"marinha"`
	Rodoviario  float64 `json:"rodoviario"`
	FreteIntl   float64 `json:"freteIntl"`
	Origem      float64 `json:"origem"`
}

func (c *Custos) soma(o Custos) {
	c.Honorarios += o.Honorarios
	c.Armazenagem += o.Armazenagem
	c.Marinha += o.Marinha
	c.Rodoviario += o.Rodoviario
	c.FreteIntl += o.FreteIntl
	c.Origem += o.Origem
}

// Logistico é o custo sem II e sem Tx. Siscomex: a parcela sobre a qual uma
// decisão de compra ou de despachante muda alguma coisa.
func (c Custos) Logistico() float64 {
	return c.Honorarios + c.Armazenagem + c.Marinha + c.Rodoviario + c.FreteIntl + c.Origem
}

func um(rx *regexp.Regexp, texto string) float64 {
	if m := rx.FindStringSubmatch(texto); m != nil {
		return ParseBR(m[1])
	}
	return 0
}

// ExtrairCustos decide o tipo da nota pela presença de `Armazenagem - destino`:
// a nacional traz os custos de destino, a internacional traz frete e origem.
// O segundo retorno é o tipo, ou "despachante" quando a nota deve ser ignorada.
func ExtrairCustos(texto string) (Custos, string) {
	var c Custos
	if strings.Contains(texto, marcaDespachante) {
		return c, "despachante"
	}
	if strings.Contains(texto, marcaNacional) {
		c.Honorarios = um(rxHonorarios, texto)
		// A única que repete: uma cobrança por período de permanência. Somar todas.
		for _, m := range rxArmazenagem.FindAllStringSubmatch(texto, -1) {
			c.Armazenagem += ParseBR(m[1])
		}
		c.Marinha = um(rxMarinha, texto)
		c.Rodoviario = um(rxRodoviario, texto)
		return c, "nacional"
	}
	c.FreteIntl = um(rxFreteIntl, texto)
	c.Origem = um(rxOrigem, texto)
	return c, "internacional"
}

// EhExtrato descarta extratos de DI/DUIMP, que repetem valores já
// contabilizados e causariam contagem dobrada.
func EhExtrato(nome string) bool {
	return strings.Contains(strings.ToLower(nome), "extrato")
}

// LayoutTexto reconstrói as linhas a partir das posições dos fragmentos: os
// regex dependem da ordem em que rótulo e número aparecem, e o extrator
// devolve caractere a caractere, fora de ordem.
func LayoutTexto(frags []pdf.Text) string {
	type linha struct {
		y     float64
		parts []pdf.Text
	}
	var linhas []linha
	for _, t := range frags {
		if t.S == "" {
			continue
		}
		achou := false
		for i := range linhas {
			if math.Abs(linhas[i].y-t.Y) <= 2 {
				linhas[i].parts = append(linhas[i].parts, t)
				achou = true
				break
			}
		}
		if !achou {
			linhas = append(linhas, linha{t.Y, []pdf.Text{t}})
		}
	}
	sort.SliceStable(linhas, func(a, b int) bool { return linhas[a].y > linhas[b].y })

	var out []string
	for _, l := range linhas {
		sort.SliceStable(l.parts, func(a, b int) bool { return l.parts[a].X < l.parts[b].X })
		var b strings.Builder
		fim := math.NaN()
		for _, p := range l.parts {
			// Sem folga = mesma palavra quebrada em fragmentos; com folga = espaço.
			if !math.IsNaN(fim) && p.X-fim > 0.8 {
				b.WriteByte(' ')
			}
			b.WriteString(p.S)
			fim = p.X + p.W
		}
		if s := strings.Join(strings.Fields(b.String()), " "); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

func PdfTexto(caminho string) (texto string, err error) {
	// A biblioteca entra em panic em PDF corrompido; vira erro para o pedido ser
	// registrado e pulado em vez de derrubar a execução inteira.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PDF ilegível: %v", r)
		}
	}()
	f, r, err := pdf.Open(caminho)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var paginas []string
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		paginas = append(paginas, LayoutTexto(p.Content().Text))
	}
	return strings.Join(paginas, "\n"), nil
}

type FalhaPDF struct {
	Arquivo string `json:"arquivo"`
	Motivo  string `json:"motivo"`
}

type UsoPDF struct {
	Arquivo string `json:"arquivo"`
	Tipo    string `json:"tipo"`
}

type ResultadoPDF struct {
	Custos
	Usados    []UsoPDF   `json:"usados"`
	Ignorados []string   `json:"ignorados"`
	Falhas    []FalhaPDF `json:"falhas"`
}

func CustosDoPedido(dir string) (ResultadoPDF, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return ResultadoPDF{}, err
	}
	var nomes []string
	for _, e := range entradas {
		n := e.Name()
		if !e.IsDir() && strings.EqualFold(filepath.Ext(n), ".pdf") && !EhExtrato(n) {
			nomes = append(nomes, n)
		}
	}
	sort.Strings(nomes)

	res := ResultadoPDF{Usados: []UsoPDF{}, Ignorados: []string{}, Falhas: []FalhaPDF{}}
	for _, n := range nomes {
		texto, err := PdfTexto(filepath.Join(dir, n))
		if err != nil {
			res.Falhas = append(res.Falhas, FalhaPDF{n, err.Error()})
			continue
		}
		c, tipo := ExtrairCustos(texto)
		if tipo == "despachante" {
			res.Ignorados = append(res.Ignorados, n)
			continue
		}
		res.Custos.soma(c)
		res.Usados = append(res.Usados, UsoPDF{n, tipo})
	}
	return res, nil
}
