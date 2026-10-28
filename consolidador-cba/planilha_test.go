package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

var cols = []string{"Sales Doc.", "Item", "Created On", "Arquivo Origem", "AT_MCP2_PIT_M"}

func row(doc, item any, file, pit string) []any { return []any{doc, item, 46202.0, file, pit} }

func TestPlanilhaGravaSoNovosEPreservaOResto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Consolidado_CBA.xlsx")

	// 1. planilha nova
	res, err := writePlanilha(path, gravarReq{Columns: cols, Rows: [][]any{row(5940235.0, 10.0, "jun.xlsx", "120"), row(5940235.0, 20.0, "jun.xlsx", "35")}}, true)
	if err != nil || res.Added != 2 || res.Backup != "" {
		t.Fatalf("primeira gravação: %+v %v", res, err)
	}
	info, err := readPlanilha(path)
	if err != nil || !info.Exists || info.Rows != 2 || info.Keys["5940235|10"] != "jun.xlsx" {
		t.Fatalf("leitura: %+v %v", info, err)
	}

	// 2. alguém escreve à mão: uma coluna Obs na aba Consolidado e uma aba de dashboard
	f, _ := excelize.OpenFile(path)
	f.SetCellStr(dataSheet, "F1", "Obs")
	f.SetCellStr(dataSheet, "F2", "conferido")
	f.NewSheet("Dashboard")
	f.SetCellStr("Dashboard", "A1", "gráficos")
	f.Save()
	f.Close()

	// 3. nova gravação: dois itens que já estão (um com o número como texto com zeros) e um novo, e uma coluna nova
	more := append(cols, "AT_NEW")
	res, err = writePlanilha(path, gravarReq{Columns: more, Rows: [][]any{
		append(row("0005940235", "000010", "jul.xlsx", "999"), "x"),
		append(row(5940235.0, 20.0, "jul.xlsx", "999"), "x"),
		append(row(5975910.0, 470.0, "jul.xlsx", "120"), "y"),
	}}, true)
	if err != nil || res.Added != 1 || res.Skipped != 2 || res.Backup == "" {
		t.Fatalf("segunda gravação: %+v %v", res, err)
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("sem cópia de segurança: %v", err)
	}
	f, _ = excelize.OpenFile(path)
	defer f.Close()
	rows, _ := f.GetRows(dataSheet)
	if got := rows[0]; len(got) != 7 || got[5] != "Obs" || got[6] != "AT_NEW" {
		t.Errorf("cabeçalho = %q", got)
	}
	if rows[1][4] != "120" || rows[1][5] != "conferido" {
		t.Errorf("item já gravado mudou ou perdeu a Obs: %q", rows[1])
	}
	if rows[3][0] != "5975910" || rows[3][6] != "y" {
		t.Errorf("item novo = %q", rows[3])
	}
	if v, _ := f.GetCellValue("Dashboard", "A1"); v != "gráficos" {
		t.Errorf("aba Dashboard perdida: %q", v)
	}
	if v, _ := f.GetCellValue(dataSheet, "C4"); v != "29/06/2026" {
		t.Errorf("data gravada como %q", v)
	}
	f.Close()

	// 4. regravar: sobrescreve os valores do app, mantém a Obs
	res, err = writePlanilha(path, gravarReq{Columns: cols, Replace: true, Rows: [][]any{row(5940235.0, 10.0, "jun.xlsx", "125")}}, true)
	if err != nil || res.Updated != 1 || res.Added != 0 {
		t.Fatalf("regravar: %+v %v", res, err)
	}
	f2, _ := excelize.OpenFile(path)
	defer f2.Close()
	if v, _ := f2.GetCellValue(dataSheet, "E2"); v != "125" {
		t.Errorf("regravar não trocou o valor: %q", v)
	}
	if v, _ := f2.GetCellValue(dataSheet, "F2"); v != "conferido" {
		t.Errorf("regravar apagou a Obs: %q", v)
	}
}

func TestPlanilhaSemColunasChave(t *testing.T) {
	if _, err := writePlanilha(filepath.Join(t.TempDir(), "x.xlsx"), gravarReq{Columns: []string{"Material"}, Rows: [][]any{{"A"}}}, true); err == nil {
		t.Error("gravou sem Sales Doc. e Item")
	}
}

func TestScanIgnoraAPlanilha(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "BR10 jun.xlsx"), "x")
	write(t, filepath.Join(root, "Consolidado_CBA.xlsx"), "x")
	write(t, filepath.Join(root, "Consolidado_CBA.bak.xlsx"), "x")
	write(t, filepath.Join(root, "~cba-gravando-Consolidado_CBA.xlsx"), "x")
	pl := filepath.Join(root, "Consolidado_CBA.xlsx")
	res, _, err := scanFolder(root, pl, backupPath(pl))
	if err != nil || len(res.Files) != 1 || res.Files[0].Name != "BR10 jun.xlsx" {
		t.Errorf("arquivos = %+v %v", res.Files, err)
	}
}

// Uma planilha que o app não fez: título em cima, cabeçalho na linha 3 com outros nomes e outra ordem,
// coluna escrita à mão, número de pedido como texto com zeros, data como texto, tabela do Excel e outra aba.
func planilhaDeFora(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Pedidos BR10.xlsx")
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "Resumo")
	f.SetCellStr("Resumo", "A1", "gráficos e notas")
	f.NewSheet("Pedidos")
	f.SetCellStr("Pedidos", "A1", "Relatório de pedidos")
	f.SetSheetRow("Pedidos", "A3", &[]any{"Obs", "Material", "Sales Doc", "Item", "Created On", "AT_MCP2_PIT_M", "Arquivo"})
	f.SetSheetRow("Pedidos", "A4", &[]any{"conferido", "M1", "0005940235", "10", "29/06/2026", "120", "jun.xlsx"})
	f.SetSheetRow("Pedidos", "A5", &[]any{nil, "M2", 5940235, 20, 46202, "35", "jun.xlsx"})
	if err := f.AddTable("Pedidos", &excelize.Table{Range: "A3:G5", Name: "TabelaPedidos", StyleName: "TableStyleMedium2"}); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestPlanilhaDeForaEncontraOsDados(t *testing.T) {
	path := planilhaDeFora(t)
	info, err := readPlanilha(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Sheet != "Pedidos" || info.HeaderRow != 3 || info.Rows != 2 || info.Keys["5940235|10"] != "jun.xlsx" || len(info.Tables) != 1 {
		t.Fatalf("info = %+v", info)
	}

	// gravar um item novo: vai para a aba Pedidos, nas colunas certas, e a tabela cresce até ele
	cols := []string{"Sales Doc.", "Item", "Created On", "Arquivo Origem", "SOrg.", "Material", "AT_MCP2_PIT_M"}
	res, err := writePlanilha(path, gravarReq{Columns: cols, Rows: [][]any{
		{5940235.0, 10.0, 46202.0, "jul.xlsx", "BR10", "M1", "999"}, // já está (como texto com zeros)
		{5975910.0, 470.0, 46223.0, "jul.xlsx", "BR10", "M9", "18"},
	}}, true)
	if err != nil || res.Sheet != "Pedidos" || res.Added != 1 || res.Skipped != 1 {
		t.Fatalf("gravar = %+v %v", res, err)
	}
	f, _ := excelize.OpenFile(path)
	if v, _ := f.GetCellValue("Pedidos", "C6"); v != "5975910" {
		t.Errorf("Sales Doc. do item novo em C6 = %q", v)
	}
	if v, _ := f.GetCellValue("Pedidos", "H3"); v != "SOrg." {
		t.Errorf("coluna que faltava não entrou no fim do cabeçalho: H3 = %q", v)
	}
	if tb, _ := f.GetTables("Pedidos"); len(tb) != 1 || tb[0].Range != "A3:H6" {
		t.Errorf("tabela = %+v", tb)
	}
	if dim, _ := f.GetSheetDimension("Pedidos"); dim != "A1:H6" {
		t.Errorf("dimensão da aba = %q, quem lê a planilha não veria tudo", dim)
	}
	f.Close()
}

func TestOrganizarNoPadraoDoApp(t *testing.T) {
	path := planilhaDeFora(t)
	cols := []string{"Sales Doc.", "Item", "Created On", "Arquivo Origem", "SOrg.", "Material", "AT_MCP2_PIT_M"}
	res, err := organizePlanilha(path, organizarReq{Columns: cols}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sheet != "Pedidos" || res.Rows != 2 || res.TopRows != 2 || res.Renamed != 2 || len(res.Added) != 1 || res.Added[0] != "SOrg." || len(res.Kept) != 1 || res.Kept[0] != "Obs" {
		t.Fatalf("resultado = %+v", res)
	}
	f, _ := excelize.OpenFile(path)
	defer f.Close()
	rows, _ := f.GetRows("Pedidos")
	want := []string{"Sales Doc.", "Item", "Created On", "Arquivo Origem", "SOrg.", "Material", "AT_MCP2_PIT_M", "Obs"}
	if strings.Join(rows[0], "|") != strings.Join(want, "|") {
		t.Errorf("cabeçalho = %q", rows[0])
	}
	if strings.Join(rows[1], "|") != "5940235|10|29/06/2026|jun.xlsx||M1|120|conferido" {
		t.Errorf("linha 2 = %q", rows[1])
	}
	if strings.Join(rows[2], "|") != "5940235|20|29/06/2026|jun.xlsx||M2|35" {
		t.Errorf("linha 3 = %q", rows[2])
	}
	if len(rows) != 3 {
		t.Errorf("sobrou conteúdo antigo: %d linhas: %q", len(rows), rows)
	}
	if typ, _ := f.GetCellType("Pedidos", "A2"); typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
		t.Errorf("Sales Doc. continuou texto")
	}
	if tb, _ := f.GetTables("Pedidos"); len(tb) != 1 || tb[0].Name != "TabelaPedidos" || tb[0].Range != "A1:H3" {
		t.Errorf("tabela = %+v", tb)
	}
	if dim, _ := f.GetSheetDimension("Pedidos"); dim != "A1:H3" {
		t.Errorf("dimensão da aba = %q", dim)
	}
	if v, _ := f.GetCellValue("Resumo", "A1"); v != "gráficos e notas" {
		t.Errorf("outra aba mudou: %q", v)
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("sem cópia .bak: %v", err)
	}
	info, _ := readPlanilha(path)
	if info.HeaderRow != 1 || info.Rows != 2 {
		t.Errorf("depois de organizar: %+v", info)
	}
}

func TestCanon(t *testing.T) {
	for a, b := range map[string]string{"Sales Doc.": "SALES_DOC", "Sales Document": "Sales Doc.", "Documento de vendas": "Sales Doc.",
		"Item": " item ", "Criado em": "Created On", "Arquivo": "Arquivo Origem", "AT_MCP2_PIT_M": "at mcp2 pit m"} {
		if canon(a) != canon(b) {
			t.Errorf("canon(%q)=%q, canon(%q)=%q", a, canon(a), b, canon(b))
		}
	}
	if canon("Item category") == canon("Item") {
		t.Error("Item category virou Item")
	}
}

func TestGravarDiretoSemCopia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Consolidado_CBA.xlsx")
	if _, err := writePlanilha(path, gravarReq{Columns: cols, Rows: [][]any{row(1.0, 10.0, "jun.xlsx", "1")}}, false); err != nil {
		t.Fatal(err)
	}
	res, err := writePlanilha(path, gravarReq{Columns: cols, Rows: [][]any{row(2.0, 10.0, "jun.xlsx", "2")}}, false)
	if err != nil || res.Added != 1 || res.Backup != "" {
		t.Fatalf("gravar sem cópia: %+v %v", res, err)
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Errorf("criou a cópia .bak com a opção desligada")
	}
	if info, _ := readPlanilha(path); info.Rows != 2 {
		t.Errorf("planilha tem %d itens, esperava 2", info.Rows)
	}
}

func TestOrganizarTiraAsCaracteristicasForaDaLista(t *testing.T) {
	// Gravada com "Incluir características fora da lista" ligada; depois a opção foi desligada.
	path := filepath.Join(t.TempDir(), "Consolidado_CBA.xlsx")
	all := append(append([]string{}, cols...), "CC_SDCOM_VKOND", "RD_MOD_LCK_REA")
	if _, err := writePlanilha(path, gravarReq{Columns: all, Rows: [][]any{
		append(row(5940235.0, 10.0, "jun.xlsx", "120"), "A1", "x"),
		append(row(5940235.0, 20.0, "jun.xlsx", "35"), "B2", "y"),
	}}, false); err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenFile(path)
	f.SetCellValue(dataSheet, "H1", "Obs") // uma coluna escrita à mão
	f.SetCellValue(dataSheet, "H2", "conferido")
	f.Save()
	f.Close()

	res, err := organizePlanilha(path, organizarReq{Columns: cols, Drop: []string{"CC_SDCOM_VKOND", "rd_mod_lck_rea", "AT_OUTRA"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Dropped, "|") != "CC_SDCOM_VKOND|RD_MOD_LCK_REA" || strings.Join(res.Kept, "|") != "Obs" || res.Rows != 2 {
		t.Fatalf("resultado = %+v", res)
	}
	f, _ = excelize.OpenFile(path)
	defer f.Close()
	rows, _ := f.GetRows(dataSheet)
	if strings.Join(rows[0], "|") != "Sales Doc.|Item|Created On|Arquivo Origem|AT_MCP2_PIT_M|Obs" || strings.Join(rows[1], "|") != "5940235|10|29/06/2026|jun.xlsx|120|conferido" {
		t.Errorf("planilha = %q", rows)
	}
	for _, r := range rows {
		if len(r) > 6 {
			t.Errorf("sobrou dado de característica tirada: %q", r)
		}
	}
	if info, _ := readPlanilha(path); info.Rows != 2 || len(info.Header) != 6 {
		t.Errorf("readPlanilha = %d itens, cabeçalho %q", info.Rows, info.Header)
	}
}
