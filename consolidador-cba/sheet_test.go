package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// dataBook writes a workbook with the given sheets, each a list of rows.
func dataBook(t *testing.T, sheets map[string][][]any, order ...string) string {
	t.Helper()
	f := excelize.NewFile()
	for i, name := range order {
		if i == 0 {
			f.SetSheetName("Sheet1", name)
		} else {
			f.NewSheet(name)
		}
		for r, row := range sheets[name] {
			for c, v := range row {
				ref, _ := excelize.CoordinatesToCellName(c+1, r+1)
				f.SetCellValue(name, ref, v)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "dados.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// decoded is what the window gets: the header and the rows with the codes turned back into values.
func decoded(t *testing.T, tb *sheetTable) (head []any, rows [][]any, note string) {
	t.Helper()
	var b bytes.Buffer
	if err := tb.encode(&b); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	n := binary.LittleEndian.Uint32(data)
	var meta struct {
		Head []any
		Rows int
		Strs []string
		Nums []float64
		Note string
	}
	if err := json.Unmarshal(data[4:4+n], &meta); err != nil {
		t.Fatal(err)
	}
	at := 4 + int(n) + (4-int(n)%4)%4
	for r := 0; r < meta.Rows; r++ {
		var out []any
		for c := range meta.Head {
			code := int32(binary.LittleEndian.Uint32(data[at+4*(r*len(meta.Head)+c):]))
			switch {
			case code == -1:
				out = append(out, nil)
			case code >= 0:
				out = append(out, meta.Strs[code])
			default:
				out = append(out, meta.Nums[-2-code])
			}
		}
		rows = append(rows, out)
	}
	return meta.Head, rows, meta.Note
}

func TestLeituraDaAbaData(t *testing.T) {
	path := dataBook(t, map[string][][]any{"Data": {
		{"Sales Doc.", "Item", "SU", "Created On", "Internal char no.", "Characteristic Value", "Val from", "Obs"},
		{5940235, "000010", "PC", 46202, "AT_MCP2_PIT_M", nil, 1.25, "x"},
		{nil, nil, "PC"}, // nothing the consolidation reads: left out
		{5940235, "000010", "PC", 46202, "AT_MCP2_RD_VOT", "380 V & mais", nil},
	}}, "Data")
	tb, err := readDataSheet(path, "Data")
	if err != nil {
		t.Fatal(err)
	}
	head, rows, note := decoded(t, tb)
	want := []any{"Sales Doc.", "Item", "Created On", "Internal char no.", "Characteristic Value", "Val from"}
	if !reflect.DeepEqual(head, want) || note != "" {
		t.Fatalf("head = %v, note %q", head, note)
	}
	wantRows := [][]any{
		{5940235.0, "000010", 46202.0, "AT_MCP2_PIT_M", nil, 1.25},
		{5940235.0, "000010", 46202.0, "AT_MCP2_RD_VOT", "380 V & mais", nil},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Errorf("rows = %v", rows)
	}
	if len(tb.strs) != 4 { // "000010" and "AT_..." once each, even in two rows
		t.Errorf("texts = %v", tb.strs)
	}
}

func TestLeituraAbaRenomeadaEErros(t *testing.T) {
	path := dataBook(t, map[string][][]any{
		"Capa":  {{"Relatório"}},
		"Junho": {{}, {"  SALES DOC. ", "Item", "Internal char no.", "Val from"}, {1, 10, "AT_A", 2}},
	}, "Capa", "Junho")
	tb, err := readDataSheet(path, "Data")
	if err != nil {
		t.Fatal(err)
	}
	if _, rows, note := decoded(t, tb); len(rows) != 1 || !strings.Contains(note, `A aba se chama "Junho" em vez de "Data"`) {
		t.Errorf("renamed sheet: rows %v, note %q", rows, note)
	}

	none := dataBook(t, map[string][][]any{"Resumo": {{"Sales Doc.", "Item"}}}, "Resumo")
	dir := t.TempDir()
	senha, corrompido := filepath.Join(dir, "senha.xlsx"), filepath.Join(dir, "corrompido.xlsx")
	os.WriteFile(senha, []byte{0xd0, 0xcf, 0x11, 0xe0, 1, 2, 3}, 0o644)
	os.WriteFile(corrompido, []byte("PK isto não é um zip"), 0o644)
	for path, want := range map[string]string{
		none:       `Não tem a aba "Data", e nenhuma outra aba tem as colunas Sales Doc., Item e Internal char no. Abas do arquivo: Resumo.`,
		senha:      msgPassword,
		corrompido: msgCorrupt,
	} {
		_, err := readDataSheet(path, "Data")
		if _, ok := err.(fileProblem); !ok || err.Error() != want {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
	if _, err := readDataSheet(filepath.Join(dir, "nao-existe.xlsx"), "Data"); err == nil || isFileProblem(err) {
		t.Errorf("missing file should be a system error: %v", err)
	}
}

func isFileProblem(err error) bool { _, ok := err.(fileProblem); return ok }

func TestTextoDoXML(t *testing.T) {
	for in, want := range map[string]string{
		"a &amp; b &lt;c&gt; &quot;d&quot; &apos;e&apos;": `a & b <c> "d" 'e'`,
		"&#233;&#xE9; &unknown; & solto":                  "éé &unknown; & solto",
		"linha_x000D_quebra":                              "linha\rquebra",
		"_x005F_x0041_":                                   "_x0041_",
	} {
		if got := string(appendText(nil, []byte(in))); got != want {
			t.Errorf("appendText(%q) = %q, want %q", in, got, want)
		}
	}
	if c, ok := columnIndex([]byte("AB12")); !ok || c != 27 {
		t.Errorf("columnIndex(AB12) = %d", c)
	}
	if v, ok := attr([]byte(`c r="B2" s="3" t='s'`), "t"); !ok || string(v) != "s" {
		t.Errorf("attr t = %q", v)
	}
	if v, ok := attr([]byte(`sheet name="A &amp; B" sheetId="1" r:id="rId3"`), "id"); !ok || string(v) != "rId3" {
		t.Errorf("attr r:id = %q", v)
	}
}

// The spreadsheet card (readPlanilha, fast reader) and the writer (findData, excelize) must see the same data.
func TestPlanilhaLidaIgualAoGravador(t *testing.T) {
	path := dataBook(t, map[string][][]any{
		"Resumo":      {{"gráficos"}},
		"Consolidado": {{"Relatório BR10"}, {}, {"Sales Doc.", "Item", "Created On", "Arquivo Origem", "Obs"}, {"0005940235", 10, 46202, " jun.xlsx ", "ok"}, {}, {5975910, "000470", 46223, "jul.xlsx"}},
	}, "Resumo", "Consolidado")
	info, err := readPlanilha(path)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenFile(path)
	defer f.Close()
	lay, _ := findData(f)
	if info.Sheet != lay.sheet || info.HeaderRow != lay.headerRow || !reflect.DeepEqual(info.Header, lay.header) {
		t.Fatalf("card: %s row %d %v, writer: %s row %d %v", info.Sheet, info.HeaderRow, info.Header, lay.sheet, lay.headerRow, lay.header)
	}
	want := map[string]string{"5940235|10": "jun.xlsx", "5975910|470": "jul.xlsx"}
	if !reflect.DeepEqual(info.Keys, want) || info.Rows != 2 || !reflect.DeepEqual(info.Sheets, []string{"Resumo", "Consolidado"}) {
		t.Errorf("keys %v rows %d sheets %v", info.Keys, info.Rows, info.Sheets)
	}
}

func TestCopiaEmExcel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copia.xlsx")
	table := exportTable{
		Cols:    []string{"Sales Doc.", "Item", "Created On", "AT_MCP2_PIT_M", "AT_LONGA"},
		DateCol: 2,
		Rows: [][]any{
			{0.0, 5940235.0, 1.0, 10.0, 2.0, 46202.0, 3.0, "1,2"},
			{4.0, strings.Repeat("x", 80), 0.0, 5975910.0, 1.0, 470.0},
		},
	}
	if err := writeTableXlsx(path, table); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, _ := f.GetRows(dataSheet)
	want := [][]string{
		{"Sales Doc.", "Item", "Created On", "AT_MCP2_PIT_M", "AT_LONGA"},
		{"5940235", "10", "29/06/2026", "1,2"},
		{"5975910", "470", "", "", strings.Repeat("x", 80)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %q", rows)
	}
	if v, _ := f.GetCellValue(dataSheet, "C2", excelize.Options{RawCellValue: true}); v != "46202" {
		t.Errorf("the date is not a number: %q", v)
	}
	if w, _ := f.GetColWidth(dataSheet, "E"); w != 48 {
		t.Errorf("width of a long column = %v, want 48", w)
	}
	if w, _ := f.GetColWidth(dataSheet, "C"); w != 12 {
		t.Errorf("width of the date column = %v, want 12", w)
	}
	names := f.GetDefinedName()
	if len(names) != 1 || names[0].Name != "_xlnm._FilterDatabase" || !strings.HasSuffix(names[0].RefersTo, "$A$1:$E$3") {
		t.Errorf("filter = %+v", names)
	}
}

func TestLargurasDasColunas(t *testing.T) {
	chars := []int{10, 10, 60, 3}
	for i := 0; i < 300; i++ {
		chars = append(chars, 12+i%3) // the characteristics outside the list
	}
	runs := widthRuns(chars)
	if runs[0] != (widthRun{1, 2, 12}) || runs[1] != (widthRun{3, 3, 48}) || runs[2] != (widthRun{4, 4, 5}) {
		t.Errorf("first runs = %v", runs[:3])
	}
	if last := runs[len(runs)-1]; last.from != exactWidths+1 || last.to != len(chars) || last.width != 14 {
		t.Errorf("shared width after %d columns = %+v", exactWidths, last)
	}
	if len(runs) > exactWidths+1 {
		t.Errorf("%d calls to set widths", len(runs))
	}
}
