package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

var cols = []string{"Sales Doc.", "Item", "Created On", "Arquivo Origem", "AT_MCP2_PIT_M"}

func row(doc, item any, file, pit string) []any { return []any{doc, item, 46202.0, file, pit} }

func TestPlanilhaGravaSoNovosEPreservaOResto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Consolidado_CBA.xlsx")

	// 1. planilha nova
	res, err := writePlanilha(path, gravarReq{Columns: cols, Rows: [][]any{row(5940235.0, 10.0, "jun.xlsx", "120"), row(5940235.0, 20.0, "jun.xlsx", "35")}})
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
	}})
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
	res, err = writePlanilha(path, gravarReq{Columns: cols, Replace: true, Rows: [][]any{row(5940235.0, 10.0, "jun.xlsx", "125")}})
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
	if _, err := writePlanilha(filepath.Join(t.TempDir(), "x.xlsx"), gravarReq{Columns: []string{"Material"}, Rows: [][]any{{"A"}}}); err == nil {
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
