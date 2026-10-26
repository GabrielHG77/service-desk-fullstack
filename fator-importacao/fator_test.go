package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

func perto(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestParseBR(t *testing.T) {
	casos := []struct {
		in   string
		want float64
	}{
		{"668\t14", 668.14}, // TAB como separador decimal no CSV legado
		{"  1.278\t261048", 1278.261048},
		{"1.234,56", 1234.56},
		{"  154\t23", 154.23},
		{"", 0},
		{"-", 0},
		{"R$ 1.000,00", 1000},
	}
	for _, c := range casos {
		if got := ParseBR(c.in); !perto(got, c.want) {
			t.Errorf("ParseBR(%q) = %v, queria %v", c.in, got, c.want)
		}
	}
}

// A própria linha se verifica: II Recolhido / CIF Total tem que bater com % II.
func TestConferenciaCSV(t *testing.T) {
	if got := ParseBR("  120\t27") / ParseBR("  668\t14") * 100; !perto(got, 18.00) {
		t.Errorf("II/CIF = %.2f%%, queria 18,00%%", got)
	}
	if got := ParseBR("1.046\t57") / ParseBR("5.232\t85") * 100; !perto(got, 20.00) {
		t.Errorf("II/CIF = %.2f%%, queria 20,00%%", got)
	}
}

func TestParseDataBR(t *testing.T) {
	d, ok := ParseDataBR("06/04/2026")
	if !ok || FmtDataBR(d) != "06/04/2026" || d.Month() != time.April {
		t.Errorf("06/04/2026 virou %v — tem que ser 6 de abril, não 4 de junho", d)
	}
	if d, ok := ParseDataBR("03/08/26"); !ok || FmtDataBR(d) != "03/08/2026" {
		t.Errorf("ano de dois dígitos do CSV: %v %v", d, ok)
	}
	if d, ok := ParseDataBR("03/08/2026 00:00:00"); !ok || FmtDataBR(d) != "03/08/2026" {
		t.Errorf("hora grudada na data: %v %v", d, ok)
	}
	if _, ok := ParseDataBR("31/02/2026"); ok {
		t.Error("31/02 devia ser rejeitado, não normalizado para março")
	}
	if _, ok := ParseDataBR(""); ok {
		t.Error("vazio devia ser rejeitado")
	}
}

func TestFmtBRL(t *testing.T) {
	for in, want := range map[float64]string{
		1234.5: "1.234,50", 925350: "925.350,00", 0: "0,00", -1234.56: "-1.234,56", 668.14: "668,14",
	} {
		if got := FmtBRL(in); got != want {
			t.Errorf("FmtBRL(%v) = %q, queria %q", in, got, want)
		}
	}
}

const notaNacional = `
NOTA DE DEBITO SSZ2602015
Despacho Importação
Honorários de despacho 1.417,79
SUBTOTAL TRIBUTÁVEL 1.417,79
Armazenagem - destino 3.203,43
Armazenagem - destino 338,63
Armazenagem - destino 4.828,19
Marinha Mercante 2.551,00
Transporte Rodoviário 980,45
`

const notaInternacional = `
NOTA DE DEBITO INTERNACIONAL
Frete marítimo 12.000,00
SUBTOTAL NÃO TRIBUTÁVEL 12.000,00
Taxa Administrativa 150,00
Handling origem 123,50
SUBTOTAL TRIBUTÁVEL 273,50
`

// O erro mais caro: uma nota com três cobranças registrava só a primeira, e o
// número resultante parecia normal.
func TestArmazenagemSomaTodas(t *testing.T) {
	c, tipo := ExtrairCustos(notaNacional)
	if tipo != "nacional" {
		t.Fatalf("tipo = %q", tipo)
	}
	if !perto(c.Armazenagem, 8370.25) { // 3.203,43 + 338,63 + 4.828,19
		t.Errorf("armazenagem = %v, queria 8370.25 (pegou só a primeira?)", c.Armazenagem)
	}
	if !perto(c.Honorarios, 1417.79) || !perto(c.Marinha, 2551) || !perto(c.Rodoviario, 980.45) {
		t.Errorf("custos nacionais errados: %+v", c)
	}
	if c.FreteIntl != 0 {
		t.Errorf("nota nacional não traz frete internacional, veio %v", c.FreteIntl)
	}
}

func TestNotaInternacional(t *testing.T) {
	c, tipo := ExtrairCustos(notaInternacional)
	if tipo != "internacional" || !perto(c.FreteIntl, 12000) || !perto(c.Origem, 273.50) {
		t.Errorf("tipo=%q custos=%+v", tipo, c)
	}
	if c.Armazenagem != 0 {
		t.Errorf("armazenagem devia ser zero, veio %v", c.Armazenagem)
	}
}

func TestFiltroDespachante(t *testing.T) {
	if _, tipo := ExtrairCustos("ACME DESPACHANTES ADUANEIROS LTDA\n" + notaNacional); tipo != "despachante" {
		t.Errorf("nota do despachante devia ser descartada, veio %q", tipo)
	}
	// Razão social com a palavra ARMAZENAGEM não pode virar nota nacional.
	c, tipo := ExtrairCustos("ARMAZENAGEM GERAL DO PORTO LTDA\n" + notaInternacional)
	if tipo != "internacional" || c.Armazenagem != 0 {
		t.Errorf("falso positivo: tipo=%q armazenagem=%v", tipo, c.Armazenagem)
	}
}

func TestEhExtrato(t *testing.T) {
	for nome, want := range map[string]bool{
		"Extrato DI 26-0001.pdf": true, "EXTRATO DUIMP.pdf": true, "SSZ2602015.pdf": false,
	} {
		if got := EhExtrato(nome); got != want {
			t.Errorf("EhExtrato(%q) = %v", nome, got)
		}
	}
}

func TestValorAusenteEhZero(t *testing.T) {
	c, _ := ExtrairCustos("Armazenagem - destino 10,00\nnada mais aqui")
	if c.Honorarios != 0 || c.Marinha != 0 || c.Rodoviario != 0 || !perto(c.Armazenagem, 10) {
		t.Errorf("valor não encontrado tem que ser zero, não erro: %+v", c)
	}
}

func TestLayoutTexto(t *testing.T) {
	// O extrator devolve fora de ordem e quebra palavras em fragmentos.
	frags := []pdf.Text{
		{S: "3.203,43", W: 40, X: 400, Y: 700},
		{S: "Armazena", W: 48, X: 50, Y: 700},
		{S: "gem", W: 18, X: 98, Y: 700},
		{S: "- destino", W: 40, X: 120, Y: 700},
		{S: "Marinha Mercante", W: 80, X: 50, Y: 680},
		{S: "2.551,00", W: 40, X: 400, Y: 681}, // mesma linha visual
	}
	want := "Armazenagem - destino 3.203,43\nMarinha Mercante 2.551,00"
	if got := LayoutTexto(frags); got != want {
		t.Errorf("LayoutTexto =\n%q\nqueria\n%q", got, want)
	}
}

// pdfComLinhas gera um PDF mínimo com stream comprimido e texto em
// WinAnsiEncoding, como as notas reais.
func pdfComLinhas(linhas []string) []byte {
	escapar := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	var conteudo strings.Builder
	for i, l := range linhas {
		b, _ := charmap.Windows1252.NewEncoder().String(escapar.Replace(l))
		fmt.Fprintf(&conteudo, "BT /F1 10 Tf 50 %d Td (%s) Tj ET\n", 740-i*18, b)
	}
	var comp bytes.Buffer
	zw := zlib.NewWriter(&comp)
	_, _ = zw.Write([]byte(conteudo.String()))
	zw.Close()

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"", // o stream entra à mão abaixo
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	var off []int
	for i, o := range objs {
		off = append(off, out.Len())
		if i == 3 {
			fmt.Fprintf(&out, "4 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", comp.Len())
			out.Write(comp.Bytes())
			out.WriteString("\nendstream\nendobj\n")
			continue
		}
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range off {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(objs)+1, xref)
	return out.Bytes()
}

// `Pedido` aparece duas vezes e o cabeçalho termina com colunas sem nome.
var cabCSV = []string{"Nr.DI/Duimp", "Data DI/Duimp", "Fob", "Pedido", "Descricao", "Qtde", "Unidade",
	"CIF Total", "II Recolhido", "% II", "PIS Recolhido", "ICMS", "Tx. Siscomex", "Exportador", "Pedido", "", ""}

func linhaCSV(di, data, pedido, descricao, qtde, cif, ii, siscomex, exportador string) string {
	return strings.Join([]string{di, data, "  500\t00", pedido, descricao, qtde, "PC",
		cif, ii, "18,00", "  99\t99", "  88\t88", siscomex, exportador, pedido, "", ""}, ";")
}

func escrever(t *testing.T, caminho string, b []byte) {
	t.Helper()
	if err := os.WriteFile(caminho, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func arvore(t *testing.T) (raiz, planilha string) {
	t.Helper()
	raiz = t.TempDir()
	sea := filepath.Join(raiz, "dados", "2026-08", "SEA", "pedido-sea")
	air := filepath.Join(raiz, "dados", "2026-08", "air", "pedido-air") // modal em minúscula
	for _, d := range []string{sea, air} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	csv := strings.Join([]string{strings.Join(cabCSV, ";"),
		linhaCSV("26/0000001-1", "03/08/26", "PO-1", "PARAFUSO", "10", "  668\t14", "  120\t27", "  154\t23", "ACME GMBH"),
		linhaCSV("26/0000001-1", "03/08/26", "PO-1", "PORCA", "5", "5.232\t85", "1.046\t57", "  0\t00", "ACME GMBH"),
		linhaCSV("", "", "", "", "", "", "", "", ""), // linha fantasma do fim
		"",
	}, "\r\n")
	cp1252, _ := charmap.Windows1252.NewEncoder().String(csv)
	escrever(t, filepath.Join(sea, "B26TESTE01.CSV"), []byte(cp1252))

	escrever(t, filepath.Join(sea, "SSZ2602015.pdf"), pdfComLinhas([]string{
		"Despacho Importação", "SUBTOTAL TRIBUTÁVEL 1.417,79",
		"Armazenagem - destino 3.203,43", "Armazenagem - destino 338,63",
		"Armazenagem - destino 4.828,19", "Marinha Mercante 500,00",
		"Transporte Rodoviário 200,00"}))
	escrever(t, filepath.Join(sea, "INTL-001.pdf"), pdfComLinhas([]string{
		"SUBTOTAL NÃO TRIBUTÁVEL 1.000,00", "Taxa Administrativa 50,00",
		"SUBTOTAL TRIBUTÁVEL 273,50"}))
	// Estes dois têm que ser descartados: contagem dobrada.
	escrever(t, filepath.Join(sea, "Extrato DI 26-0001.pdf"), pdfComLinhas([]string{"Armazenagem - destino 9.999,99"}))
	escrever(t, filepath.Join(sea, "desp.pdf"), pdfComLinhas([]string{
		"ACME DESPACHANTES ADUANEIROS LTDA", "Armazenagem - destino 7.777,77"}))

	f := excelize.NewFile()
	defer f.Close()
	cab := []any{"Nr.DI/Duimp", "Data DI/Duimp", "Pedido", "Descrição", "Qtde",
		"Unidade", "CIF Total", "II Recolhido", "Tx. Siscomex", "Exportador", "NCM"}
	item := []any{"26/0000002-2", time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		"PO-2", "ROLAMENTO", 2, "PC", 10000, 1800, 154.23, "BETA AG", "8482.10.10"}
	vazia := []any{nil, nil, nil, "", nil, nil, nil, nil, nil, nil, nil}
	for cel, linha := range map[string]*[]any{"A1": &cab, "A2": &item, "A3": &vazia} {
		if err := f.SetSheetRow("Sheet1", cel, linha); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SaveAs(filepath.Join(air, "B26TESTE02.xlsx")); err != nil {
		t.Fatal(err)
	}
	escrever(t, filepath.Join(air, "nota.pdf"), pdfComLinhas([]string{
		"Despacho Importação", "SUBTOTAL TRIBUTÁVEL 1.417,79",
		"Armazenagem - destino 100,00", "Transporte Rodoviário 50,00"}))

	return raiz, filepath.Join(raiz, "ConsolidadoPath.xlsx")
}

func TestPontaAPonta(t *testing.T) {
	raiz, planilha := arvore(t)

	alvos := Varrer(raiz)
	if len(alvos) != 2 {
		t.Fatalf("Varrer achou %d pedidos, queria 2", len(alvos))
	}
	modais := map[string]bool{}
	for _, a := range alvos {
		modais[a.Modal] = true
	}
	if !modais["AIR"] || !modais["SEA"] {
		t.Errorf("modal em minúscula também tem que contar: %v", modais)
	}

	dry, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, DryRun: true, Regras: RegrasPadrao()})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Erros) != 0 {
		t.Fatalf("erros: %+v", dry.Erros)
	}
	if len(dry.Adicionados) != 2 {
		t.Fatalf("adicionados = %d, queria 2", len(dry.Adicionados))
	}
	if _, err := os.Stat(planilha); err == nil {
		t.Error("dry-run não pode tocar a planilha")
	}

	var sea *Pedido
	for _, p := range dry.Adicionados {
		if p.Modal == "SEA" {
			sea = p
		}
	}
	if sea == nil {
		t.Fatal("pedido SEA não foi processado")
	}
	if sea.NRef != "B26TESTE01" {
		t.Errorf("N/Ref. vem do nome do arquivo, não da pasta: %q", sea.NRef)
	}
	if sea.NumItens != 2 {
		t.Errorf("itens = %d, queria 2 (linha fantasma pulada)", sea.NumItens)
	}
	if !perto(sea.CIF, 5900.99) { // 668,14 + 5.232,85
		t.Errorf("CIF = %v, queria 5900.99", sea.CIF)
	}
	// armazenagem 8.370,25 + honorários 1.417,79 + marinha 500 + rodoviário 200
	//   + frete 1.000 + origem 273,50
	logistico := 8370.25 + 1417.79 + 500.0 + 200.0 + 1000.0 + 273.50
	if !perto(sea.Total, 120.27+1046.57+154.23+logistico) {
		t.Errorf("total = %v, queria %v", sea.Total, 120.27+1046.57+154.23+logistico)
	}
	if !perto(sea.FatorLogistico*sea.CIF, logistico) {
		t.Errorf("fator logístico tem que excluir II e Siscomex: %v", sea.FatorLogistico*sea.CIF)
	}

	r1, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, Regras: RegrasPadrao()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Adicionados) != 2 || !r1.Gravado {
		t.Fatalf("gravação: adicionados=%d gravado=%v", len(r1.Adicionados), r1.Gravado)
	}

	// Reexecução: a N/Ref. bloqueia regravação.
	r2, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, Regras: RegrasPadrao()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Adicionados) != 0 || len(r2.Pulados) != 2 {
		t.Errorf("reexecução duplicou: adicionados=%d pulados=%v", len(r2.Adicionados), r2.Pulados)
	}

	conferirPlanilha(t, planilha)

	// Backup antes de gravar por cima; reprocessar não duplica.
	if _, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, Reprocessar: true, Regras: RegrasPadrao()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSuffix(planilha, ".xlsx") + ".bak.xlsx"); err != nil {
		t.Error("backup .bak.xlsx não foi criado antes de sobrescrever")
	}
	f, err := excelize.OpenFile(planilha)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	linhas, _ := f.GetRows("SEA")
	if len(linhas) != 4 {
		t.Errorf("reprocessar duplicou: SEA tem %d linhas, queria 4", len(linhas))
	}
}

func conferirPlanilha(t *testing.T, planilha string) {
	t.Helper()
	f, err := excelize.OpenFile(planilha)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	linhas, _ := f.GetRows("SEA")
	if len(linhas) != 4 { // cabeçalho + 2 materiais + 1 total
		t.Fatalf("SEA tem %d linhas, queria 4", len(linhas))
	}
	for i, want := range cabecalhos["SEA"] {
		if linhas[0][i] != want {
			t.Errorf("SEA cabeçalho col %d = %q, queria %q", i+1, linhas[0][i], want)
		}
	}
	if air, _ := f.GetRows("AIR"); len(air) != 3 {
		t.Errorf("AIR tem %d linhas, queria 3", len(air))
	}

	get := func(aba string, col, lin int) string {
		v, _ := f.GetCellValue(aba, cel(col, lin))
		return v
	}
	// Linha de material: Mercadoria preenchida, Modal vazio. Total: o contrário.
	if get("SEA", 8, 2) != "PARAFUSO" || get("SEA", 7, 2) != "" {
		t.Error("linha de material tem que ter Mercadoria e não ter Modal")
	}
	if get("SEA", 8, 4) != "" || get("SEA", 7, 4) != "SEA" {
		t.Error("linha de total tem que ter Modal e não ter Mercadoria")
	}
	if get("SEA", 6, 4) != "ACME GMBH" {
		t.Errorf("exportador na linha de total: %q", get("SEA", 6, 4))
	}

	L := layouts["SEA"]
	bruto, _ := f.GetCellValue("SEA", cel(L.armazenagem, 4), excelize.Options{RawCellValue: true})
	if !perto(numCru(bruto), 8370.25) {
		t.Errorf("armazenagem gravada = %q, queria 8370.25", bruto)
	}
	// Outros fica sempre vazia: a analista digita ali à mão.
	if v := get("SEA", L.outros, 4); v != "" {
		t.Errorf("coluna Outros tem que ficar vazia, veio %q", v)
	}
	if fo, _ := f.GetCellFormula("SEA", cel(L.total, 4)); fo != "SUM(L4:T4)" {
		t.Errorf("fórmula do Total = %q, queria SUM(L4:T4)", fo)
	}
	if fo, _ := f.GetCellFormula("SEA", cel(L.pct, 4)); fo != `IF(K4=0,"",U4/K4)` {
		t.Errorf("fórmula do Percentual = %q", fo)
	}

	// Data gravada como 3 de agosto, não 8 de março.
	if v := get("SEA", 2, 2); v != "03/08/2026" {
		t.Errorf("data do CSV gravada como %q, queria 03/08/2026", v)
	}
	// 06/04/2026 do .xlsx continua 6 de abril.
	if v := get("AIR", 2, 2); v != "06/04/2026" {
		t.Errorf("data do xlsx gravada como %q, queria 06/04/2026", v)
	}

	A := layouts["AIR"]
	if fo, _ := f.GetCellFormula("AIR", cel(A.total, 3)); fo != "SUM(L3:S3)" {
		t.Errorf("fórmula do Total no AIR = %q, queria SUM(L3:S3)", fo)
	}
}

func TestCSVColunaPedidoDuplicada(t *testing.T) {
	raiz, _ := arvore(t)
	nref, itens, err := LerMateriais(filepath.Join(raiz, "dados", "2026-08", "SEA", "pedido-sea", "B26TESTE01.CSV"))
	if err != nil {
		t.Fatal(err)
	}
	if nref != "B26TESTE01" || len(itens) != 2 {
		t.Fatalf("nref=%q itens=%d", nref, len(itens))
	}
	if itens[0].Pedido != "PO-1" || !perto(itens[0].CIF, 668.14) || itens[0].Exportador != "ACME GMBH" {
		t.Errorf("item lido errado: %+v", itens[0])
	}
}

func TestPastaSemMateriaisNaoDerruba(t *testing.T) {
	raiz, planilha := arvore(t)
	if err := os.MkdirAll(filepath.Join(raiz, "dados", "2026-08", "SEA", "pedido-vazio"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, DryRun: true, Regras: RegrasPadrao()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Adicionados) != 2 {
		t.Errorf("os outros dois pedidos tinham que passar: %d", len(r.Adicionados))
	}
	if len(r.Erros) != 1 || !strings.Contains(r.Erros[0].Motivo, "nenhum arquivo de materiais") {
		t.Errorf("erros = %+v", r.Erros)
	}
}

// A pasta chama-se "pedido-sea" e o arquivo "B26TESTE01.CSV": a chave é a do
// arquivo. Com a da pasta, o painel de meses contava pedido já consolidado como
// novo e a pré-checagem de duplicata nunca disparava.
func TestNRefVemDoArquivoNaoDaPasta(t *testing.T) {
	raiz, planilha := arvore(t)

	for _, a := range Varrer(raiz) {
		if filepath.Base(a.Dir) == a.NRef {
			t.Errorf("Varrer devolveu o nome da pasta como N/Ref.: %q", a.NRef)
		}
	}
	nrefs := map[string]bool{}
	for _, a := range Varrer(raiz) {
		nrefs[a.NRef] = true
	}
	if !nrefs["B26TESTE01"] || !nrefs["B26TESTE02"] {
		t.Fatalf("N/Ref. esperadas não apareceram: %v", nrefs)
	}

	if _, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, Regras: RegrasPadrao()}); err != nil {
		t.Fatal(err)
	}
	// Agora a varredura tem que reconhecer os dois como já gravados.
	pl, err := AbrirPlanilha(planilha)
	if err != nil {
		t.Fatal(err)
	}
	defer pl.Close()
	existentes := map[string]map[string]bool{"AIR": pl.NRefsExistentes("AIR"), "SEA": pl.NRefsExistentes("SEA")}
	for _, a := range Varrer(raiz) {
		if !existentes[a.Modal][a.NRef] {
			t.Errorf("%s/%s ficaria contado como novo depois de consolidado", a.Modal, a.NRef)
		}
	}
}

func TestRegrasNormalizar(t *testing.T) {
	p := RegrasPadrao()
	if len(p.Custo) != len(Componentes) || p.Faixa != (Faixa{0.24, 0.30}) {
		t.Fatalf("padrão errado: %+v", p)
	}
	// Chave desconhecida some; faixa invertida volta ao padrão; repetida não duplica.
	n := Regras{Faixa: Faixa{0.9, 0.1}, Custo: []string{"ii", "icms", "ii", "armazenagem"}}.Normalizar()
	if n.Faixa != p.Faixa {
		t.Errorf("faixa invertida devia voltar ao padrão, veio %+v", n.Faixa)
	}
	if len(n.Custo) != 2 || !n.Usa("ii") || !n.Usa("armazenagem") || n.Usa("icms") {
		t.Errorf("custo normalizado errado: %v", n.Custo)
	}
	// Lista vazia cai no padrão em vez de zerar o fator de todo mundo.
	if v := (Regras{Faixa: p.Faixa}).Normalizar(); len(v.Custo) != len(p.Custo) {
		t.Errorf("custo vazio devia cair no padrão, veio %v", v.Custo)
	}
}

func TestRegrasTotalEFormula(t *testing.T) {
	somas := Somas{II: 100, Siscomex: 10}
	custos := Custos{Rodoviario: 1, Marinha: 2, Origem: 4, Armazenagem: 8, Honorarios: 16, FreteIntl: 32}

	cheio := RegrasPadrao()
	if got := cheio.Total(somas, custos); !perto(got, 173) {
		t.Errorf("total cheio = %v, queria 173", got)
	}
	if got := cheio.FormulaTotal("SEA", 4); got != "SUM(L4:T4)" {
		t.Errorf("com tudo ligado a fórmula tem que ser contígua, veio %q", got)
	}
	if got := cheio.FormulaTotal("AIR", 3); got != "SUM(L3:S3)" {
		t.Errorf("fórmula do aéreo = %q", got)
	}

	// Só logística: sai o imposto, e as colunas deixam de ser contíguas.
	semImposto := Regras{Faixa: cheio.Faixa, Custo: []string{"rodoviario", "marinha", "origem", "armazenagem", "honorarios", "freteIntl"}}
	if got := semImposto.Total(somas, custos); !perto(got, 63) {
		t.Errorf("total sem imposto = %v, queria 63", got)
	}
	if got := semImposto.Imposto(somas); got != 0 {
		t.Errorf("imposto devia ser zero, veio %v", got)
	}
	if got := semImposto.FormulaTotal("SEA", 4); got != "SUM(N4:T4)" {
		t.Errorf("fórmula sem imposto = %q", got)
	}
	// Marinha mercante não existe no aéreo: não pode entrar na fórmula.
	if got := cheio.FormulaTotal("AIR", 3); strings.Contains(got, "+") {
		t.Errorf("aéreo com tudo ligado ainda é contíguo, veio %q", got)
	}
	// Buraco no meio força a soma explícita, com Outros sempre no fim.
	furado := Regras{Faixa: cheio.Faixa, Custo: []string{"ii", "armazenagem"}}
	if got := furado.FormulaTotal("SEA", 4); got != "L4+Q4+T4" {
		t.Errorf("fórmula furada = %q, queria L4+Q4+T4", got)
	}
}

func TestFaixaConfiguravelMudaOsAvisos(t *testing.T) {
	raiz, planilha := arvore(t)
	// O pedido SEA de teste dá fator bem acima de 30%: com a faixa larga some.
	larga := Regras{Faixa: Faixa{0.10, 3.00}, Custo: RegrasPadrao().Custo}
	r, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, DryRun: true, Regras: larga})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Avisos) != 0 {
		t.Errorf("faixa larga não devia gerar aviso, veio %d", len(r.Avisos))
	}
	estreita := Regras{Faixa: Faixa{0.24, 0.2401}, Custo: RegrasPadrao().Custo}
	r2, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, DryRun: true, Regras: estreita})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Avisos) != len(r2.Adicionados) {
		t.Errorf("faixa estreita devia avisar de todos: %d de %d", len(r2.Avisos), len(r2.Adicionados))
	}
}

func TestLerConsolidadoAlimentaOsGraficos(t *testing.T) {
	raiz, planilha := arvore(t)
	regras := RegrasPadrao()
	if _, err := Consolidar(OpcoesConsolidar{Raiz: raiz, Planilha: planilha, Meses: []string{"2026-08"}, Regras: regras}); err != nil {
		t.Fatal(err)
	}
	c, err := LerConsolidado(planilha, regras)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Pedidos) != 2 {
		t.Fatalf("queria 2 pedidos lidos da planilha, veio %d (%+v)", len(c.Pedidos), c.Pedidos)
	}
	porNRef := map[string]PedidoGravado{}
	for _, p := range c.Pedidos {
		porNRef[p.NRef] = p
	}
	sea := porNRef["B26TESTE01"]
	if sea.Modal != "SEA" || sea.Linhas != 2 {
		t.Errorf("linha de total lida errado: %+v", sea)
	}
	if !perto(sea.CIF, 5900.99) {
		t.Errorf("CIF lido = %v, queria 5900.99", sea.CIF)
	}
	// O Total vem somado das colunas, porque a célula Total guarda fórmula.
	logistico := 8370.25 + 1417.79 + 500.0 + 200.0 + 1000.0 + 273.50
	if !perto(sea.Total, 120.27+1046.57+154.23+logistico) {
		t.Errorf("total recalculado = %v", sea.Total)
	}
	if !perto(sea.Imposto, 120.27+1046.57+154.23) {
		t.Errorf("imposto = %v", sea.Imposto)
	}
	if sea.Mes != "2026-08" {
		t.Errorf("mês tirado da data das linhas de material = %q, queria 2026-08", sea.Mes)
	}
	if !perto(sea.Fator, sea.Total/sea.CIF) || !perto(sea.FatorLogistico, (sea.Total-sea.Imposto)/sea.CIF) {
		t.Errorf("fatores errados: %+v", sea)
	}
	// 06/04/2026 do .xlsx continua abril, não junho.
	if porNRef["B26TESTE02"].Mes != "2026-04" {
		t.Errorf("mês do pedido aéreo = %q, queria 2026-04", porNRef["B26TESTE02"].Mes)
	}
}

func TestLerConsolidadoSemPlanilhaNaoQuebra(t *testing.T) {
	c, err := LerConsolidado(filepath.Join(t.TempDir(), "nao-existe.xlsx"), RegrasPadrao())
	if err != nil {
		t.Fatalf("planilha ausente não pode virar erro: %v", err)
	}
	if len(c.Pedidos) != 0 || c.Aviso == "" {
		t.Errorf("queria aviso e lista vazia, veio %+v", c)
	}
}
