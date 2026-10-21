package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

// Só estas colunas são lidas. O arquivo traz dezenas de outras (Fob, Seguro,
// ICMS, PIS Recolhido, Peso Liquido, NCM...) e elas são ignoradas de propósito:
// PIS/COFINS/ICMS são recuperáveis e não entram no custo de importação.
var colunas = []struct{ alias, nome string }{
	{"di", "Nr.DI/Duimp"},
	{"data", "Data DI/Duimp"},
	{"pedido", "Pedido"},
	{"descricao", "Descricao"},
	{"qtde", "Qtde"},
	{"unidade", "Unidade"},
	{"cif", "CIF Total"},
	{"ii", "II Recolhido"},
	{"siscomex", "Tx. Siscomex"},
	{"exportador", "Exportador"},
}

type Item struct {
	DI         string    `json:"di"`
	Data       time.Time `json:"data"`
	TemData    bool      `json:"temData"`
	Pedido     string    `json:"pedido"`
	Descricao  string    `json:"descricao"`
	Qtde       float64   `json:"qtde"`
	Unidade    string    `json:"unidade"`
	CIF        float64   `json:"cif"`
	II         float64   `json:"ii"`
	Siscomex   float64   `json:"siscomex"`
	Exportador string    `json:"exportador"`
}

// NRefDe: o nome do arquivo sem extensão é a N/Ref. (B2600037349.CSV -> B2600037349).
func NRefDe(caminho string) string {
	n := filepath.Base(caminho)
	return strings.TrimSuffix(n, filepath.Ext(n))
}

// `Pedido` aparece nas posições 4 e 45 com o mesmo valor: fica a primeira.
// Cabeçalhos vazios do fim do arquivo são descartados.
func mapearColunas(cabecalho []string) (map[string]int, error) {
	idx := map[string]int{}
	for i, nome := range cabecalho {
		k := NormKey(nome)
		if k == "" {
			continue
		}
		if _, visto := idx[k]; !visto {
			idx[k] = i
		}
	}
	campo, faltando := map[string]int{}, []string{}
	for _, c := range colunas {
		if i, ok := idx[NormKey(c.nome)]; ok {
			campo[c.alias] = i
		} else {
			faltando = append(faltando, c.nome)
		}
	}
	if len(faltando) > 0 {
		return nil, fmt.Errorf("colunas ausentes: %s", strings.Join(faltando, ", "))
	}
	return campo, nil
}

// O .csv traz número e data em pt-BR; o .xlsx, lido cru, traz número em formato
// de máquina e data como número de série. Converter os dois do mesmo jeito
// faria 668.14 virar 66814 e trocar dia por mês conforme o formato do arquivo.
type conversor struct {
	num  func(string) float64
	data func(string) (time.Time, bool)
}

var convBR = conversor{ParseBR, ParseDataBR}
var convCru = conversor{numCru, dataCrua}

func numCru(s string) float64 {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n
	}
	return ParseBR(s) // célula gravada como texto numa planilha
}

// shortcut: assume o sistema de datas de 1900; planilha no sistema de 1904
// sairia 4 anos adiante. Trocar se algum arquivo vier do Excel de Mac antigo.
func dataCrua(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if serial, err := strconv.ParseFloat(s, 64); err == nil && serial > 0 {
		if t, err := excelize.ExcelDateToTime(serial, false); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
		}
	}
	return ParseDataBR(s) // célula gravada como texto
}

func montarItem(valor func(string) string, cv conversor) (Item, bool) {
	descricao := strings.TrimSpace(valor("descricao"))
	if descricao == "" {
		return Item{}, false // linhas em branco do fim do arquivo
	}
	it := Item{
		DI:         strings.TrimSpace(valor("di")),
		Pedido:     strings.TrimSpace(valor("pedido")),
		Descricao:  descricao,
		Qtde:       cv.num(valor("qtde")),
		Unidade:    strings.TrimSpace(valor("unidade")),
		CIF:        cv.num(valor("cif")),
		II:         cv.num(valor("ii")),
		Siscomex:   cv.num(valor("siscomex")),
		Exportador: strings.TrimSpace(valor("exportador")),
	}
	it.Data, it.TemData = cv.data(valor("data"))
	return it, true
}

func decodificar(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // BOM
	if utf8.Valid(b) {
		return string(b)
	}
	// Sistema legado brasileiro: quase sempre cp1252.
	s, err := charmap.Windows1252.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(s)
}

// celula devolve a coluna pedida, ou vazio quando a linha é mais curta que o
// cabeçalho (acontece nas linhas finais truncadas do legado).
func celula(celulas []string, campo map[string]int) func(string) string {
	return func(alias string) string {
		if i, ok := campo[alias]; ok && i < len(celulas) {
			return celulas[i]
		}
		return ""
	}
}

func itensDasLinhas(linhas [][]string, cv conversor) ([]Item, error) {
	if len(linhas) == 0 {
		return nil, fmt.Errorf("arquivo de materiais vazio")
	}
	campo, err := mapearColunas(linhas[0])
	if err != nil {
		return nil, err
	}
	itens := []Item{}
	for _, celulas := range linhas[1:] {
		if it, ok := montarItem(celula(celulas, campo), cv); ok {
			itens = append(itens, it)
		}
	}
	return itens, nil
}

func lerCSV(caminho string) ([]Item, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(decodificar(b)))
	r.Comma = ';' // fixo: nunca da configuração regional da máquina
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	linhas, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	return itensDasLinhas(linhas, convBR)
}

func lerXLSX(caminho string) ([]Item, error) {
	f, err := excelize.OpenFile(caminho)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	abas := f.GetSheetList()
	if len(abas) == 0 {
		return nil, fmt.Errorf("planilha de materiais sem abas")
	}
	// Cru de propósito: a data vem como número de série, sem passar pelo formato
	// do arquivo, que poderia ser mm/dd e trocar dia por mês em silêncio.
	linhas, err := f.GetRows(abas[0], excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	return itensDasLinhas(linhas, convCru)
}

func LerMateriais(caminho string) (string, []Item, error) {
	var itens []Item
	var err error
	if strings.EqualFold(filepath.Ext(caminho), ".csv") {
		itens, err = lerCSV(caminho)
	} else {
		itens, err = lerXLSX(caminho)
	}
	return NRefDe(caminho), itens, err
}
