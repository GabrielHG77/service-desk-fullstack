package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Linha de material: Mercadoria preenchida, Modal vazio.
// Linha de total:    o contrário. É o que separa os dois níveis depois.
var cabecalhos = map[string][]string{
	"SEA": {"N/Ref.", "Data", "Nr.DI/Duimp", "Ref. Cliente", "Origem", "Exportador", "Modal",
		"Mercadoria", "Qtde", "Unidade de Medida", "Valor BRL", "II (R$)", "Tx. Siscomex (R$)",
		"Valor Frete Rodoviário", "MARINHA MERCANTE", "Despesas na Origem", "Armazenagem (R$)",
		"Honorarios (R$)", "Frete Maritímo (R$)", "Outros (R$)", "Total (R$)", "Percentual Custo de Importação"},
	// Aéreo não tem Marinha Mercante: tudo dali para a direita anda uma coluna.
	"AIR": {"N/Ref.", "Data", "Nr.DI/Duimp", "Ref. Cliente", "Origem", "Exportador", "Modal",
		"Mercadoria", "Qtde", "Unidade de Medida", "Valor BRL", "II (R$)", "Tx. Siscomex (R$)",
		"Valor Frete Rodoviário", "Despesas na Origem", "Armazenagem (R$)",
		"Honorarios (R$)", "Frete Aéreo (R$)", "Outros (R$)", "Total (R$)", "Percentual Custo de Importação"},
}

// Índice 1-based das colunas que mudam entre os modais. marinha 0 = não existe.
type layout struct {
	rodoviario, marinha, origem, armazenagem, honorarios, freteIntl, outros, total, pct int
}

var layouts = map[string]layout{
	"SEA": {rodoviario: 14, marinha: 15, origem: 16, armazenagem: 17, honorarios: 18, freteIntl: 19, outros: 20, total: 21, pct: 22},
	"AIR": {rodoviario: 14, marinha: 0, origem: 15, armazenagem: 16, honorarios: 17, freteIntl: 18, outros: 19, total: 20, pct: 21},
}

type Planilha struct {
	f                     *excelize.File
	estMoeda, estPct      int
	estData, estCabecalho int
	proxima               map[string]int // próxima linha livre por aba
}

func ptr(s string) *string { return &s }

func AbrirPlanilha(caminho string) (*Planilha, error) {
	var f *excelize.File
	st, err := os.Stat(caminho)
	switch {
	case err == nil && st.Size() > 0:
		if f, err = excelize.OpenFile(caminho); err != nil {
			return nil, err
		}
	case err != nil && !os.IsNotExist(err):
		return nil, err
	default:
		// Inclui a planilha de 0 bytes, que estoura com erro críptico se aberta.
		f = excelize.NewFile()
	}

	p := &Planilha{f: f, proxima: map[string]int{}}
	estilos := []struct {
		destino *int
		estilo  *excelize.Style
	}{
		{&p.estMoeda, &excelize.Style{CustomNumFmt: ptr("#,##0.00")}},
		{&p.estPct, &excelize.Style{CustomNumFmt: ptr("0.0%")}},
		// Formato de data explícito: a máquina está em inglês e não decide isso.
		{&p.estData, &excelize.Style{CustomNumFmt: ptr("dd/mm/yyyy")}},
		{&p.estCabecalho, &excelize.Style{Font: &excelize.Font{Bold: true}}},
	}
	for _, e := range estilos {
		id, err := f.NewStyle(e.estilo)
		if err != nil {
			return nil, err
		}
		*e.destino = id
	}

	for _, modal := range []string{"AIR", "SEA"} {
		i, err := f.GetSheetIndex(modal)
		if err != nil {
			return nil, err
		}
		if i == -1 {
			if _, err := f.NewSheet(modal); err != nil {
				return nil, err
			}
		}
		linhas, err := f.GetRows(modal)
		if err != nil {
			return nil, err
		}
		if len(linhas) == 0 || len(linhas[0]) == 0 || strings.TrimSpace(linhas[0][0]) == "" {
			cab := cabecalhos[modal]
			if err := f.SetSheetRow(modal, "A1", &cab); err != nil {
				return nil, err
			}
			fim, _ := excelize.ColumnNumberToName(len(cab))
			f.SetCellStyle(modal, "A1", fim+"1", p.estCabecalho)
			p.proxima[modal] = 2
		} else {
			p.proxima[modal] = len(linhas) + 1
		}
	}
	// Aba padrão do arquivo novo, que não faz parte do layout.
	if i, _ := f.GetSheetIndex("Sheet1"); i != -1 {
		f.DeleteSheet("Sheet1")
	}
	if i, _ := f.GetSheetIndex("AIR"); i != -1 {
		f.SetActiveSheet(i)
	}
	return p, nil
}

func cel(col, linha int) string {
	nome, _ := excelize.CoordinatesToCellName(col, linha)
	return nome
}

func colNome(i int) string {
	n, _ := excelize.ColumnNumberToName(i)
	return n
}

// NRefsExistentes lê a coluna A: a N/Ref. é a chave que bloqueia regravação.
func (p *Planilha) NRefsExistentes(modal string) map[string]bool {
	set := map[string]bool{}
	linhas, err := p.f.GetRows(modal)
	if err != nil {
		return set
	}
	for i, l := range linhas {
		if i == 0 || len(l) == 0 {
			continue
		}
		if v := strings.TrimSpace(l[0]); v != "" {
			set[v] = true
		}
	}
	return set
}

// RemoverNRefs apaga as linhas daqueles pedidos. Usado só pelo reprocessamento
// explícito, nunca como efeito colateral de marcar o mês.
func (p *Planilha) RemoverNRefs(modal string, nrefs map[string]bool) int {
	linhas, err := p.f.GetRows(modal)
	if err != nil {
		return 0
	}
	var alvo []int
	for i, l := range linhas {
		if i == 0 || len(l) == 0 {
			continue
		}
		if nrefs[strings.TrimSpace(l[0])] {
			alvo = append(alvo, i+1)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(alvo)))
	for _, n := range alvo {
		p.f.RemoveRow(modal, n)
	}
	if novas, err := p.f.GetRows(modal); err == nil {
		p.proxima[modal] = len(novas) + 1
	}
	return len(alvo)
}

func (p *Planilha) moeda(modal string, col, linha int, v float64) {
	c := cel(col, linha)
	p.f.SetCellFloat(modal, c, v, 2, 64)
	p.f.SetCellStyle(modal, c, c, p.estMoeda)
}

func (p *Planilha) GravarPedido(ped *Pedido) error {
	modal := ped.Modal
	L := layouts[modal]

	for _, it := range ped.Itens {
		n := p.proxima[modal]
		p.proxima[modal]++
		p.f.SetCellStr(modal, cel(1, n), ped.NRef)
		if it.TemData {
			c := cel(2, n)
			if err := p.f.SetCellValue(modal, c, it.Data); err != nil {
				return err
			}
			p.f.SetCellStyle(modal, c, c, p.estData)
		}
		p.f.SetCellStr(modal, cel(3, n), it.DI)
		p.f.SetCellStr(modal, cel(4, n), it.Pedido)
		p.f.SetCellStr(modal, cel(8, n), it.Descricao)
		p.f.SetCellFloat(modal, cel(9, n), it.Qtde, -1, 64)
		p.f.SetCellStr(modal, cel(10, n), it.Unidade)
		p.moeda(modal, 11, n, it.CIF)
		p.moeda(modal, 12, n, it.II)
		p.moeda(modal, 13, n, it.Siscomex)
	}

	n := p.proxima[modal]
	p.proxima[modal]++
	p.f.SetCellStr(modal, cel(1, n), ped.NRef)
	if len(ped.Itens) > 0 {
		p.f.SetCellStr(modal, cel(4, n), ped.Itens[0].Pedido)
	}
	p.f.SetCellStr(modal, cel(6, n), ped.Exportador)
	p.f.SetCellStr(modal, cel(7, n), modal)
	p.f.SetCellFloat(modal, cel(9, n), ped.Somas.Qtde, -1, 64)
	p.moeda(modal, 11, n, ped.Somas.CIF)
	p.moeda(modal, 12, n, ped.Somas.II)
	p.moeda(modal, 13, n, ped.Somas.Siscomex)
	p.moeda(modal, L.rodoviario, n, ped.Custos.Rodoviario)
	if L.marinha > 0 {
		p.moeda(modal, L.marinha, n, ped.Custos.Marinha)
	}
	p.moeda(modal, L.origem, n, ped.Custos.Origem)
	p.moeda(modal, L.armazenagem, n, ped.Custos.Armazenagem)
	p.moeda(modal, L.honorarios, n, ped.Custos.Honorarios)
	p.moeda(modal, L.freteIntl, n, ped.Custos.FreteIntl)

	// Outros fica vazia de propósito: a analista digita ali à mão. Zero obrigaria
	// a apagar antes. Como Total é fórmula até Outros, o que ela digitar entra.
	cTotal := cel(L.total, n)
	if err := p.f.SetCellFormula(modal, cTotal,
		fmt.Sprintf("SUM(%s%d:%s%d)", colNome(12), n, colNome(L.outros), n)); err != nil {
		return err
	}
	p.f.SetCellStyle(modal, cTotal, cTotal, p.estMoeda)

	cPct := cel(L.pct, n)
	if err := p.f.SetCellFormula(modal, cPct,
		fmt.Sprintf(`IF(%s%d=0,"",%s%d/%s%d)`, colNome(11), n, colNome(L.total), n, colNome(11), n)); err != nil {
		return err
	}
	p.f.SetCellStyle(modal, cPct, cPct, p.estPct)
	return nil
}

// SalvarPlanilha copia a planilha para .bak.xlsx antes de sobrescrever: o fluxo
// anterior escrevia direto e, se algo saísse errado no meio, não tinha volta.
func (p *Planilha) SalvarPlanilha(caminho string) error {
	if st, err := os.Stat(caminho); err == nil && st.Size() > 0 {
		if err := copiar(caminho, strings.TrimSuffix(caminho, ".xlsx")+".bak.xlsx"); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return p.f.SaveAs(caminho)
}

func copiar(de, para string) error {
	o, err := os.Open(de)
	if err != nil {
		return err
	}
	defer o.Close()
	d, err := os.Create(para)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err = io.Copy(d, o); err != nil {
		return err
	}
	return d.Sync()
}

func (p *Planilha) Close() { p.f.Close() }
