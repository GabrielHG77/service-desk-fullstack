package main

// The consolidated spreadsheet, kept up to date the way FatorImportacao keeps ConsolidadoPath.xlsx: an update
// adds only the order items that are not there yet (Sales Doc. + Item is the key), a copy of the file is made
// before writing, and rewriting a file's items is an explicit action.
//
// The spreadsheet can be one the app did not make: the data is looked for in any sheet (Consolidado first) and
// in any of the first rows, and columns are matched by name the way canon compares them. Only that sheet is
// written; other sheets and columns typed by hand are left as they are. organizePlanilha puts such a sheet in
// the app's layout: names in row 1, the app's columns in the app's order, everything else after them.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

const dataSheet = "Consolidado"

// headerScan is how far down a sheet the row with the column names is looked for (title rows above it are common).
const headerScan = 20

type planilhaInfo struct {
	Path      string            `json:"path"`
	Exists    bool              `json:"exists"`
	Sheet     string            `json:"sheet"`     // sheet with the data; "" = none has Sales Doc. and Item yet
	HeaderRow int               `json:"headerRow"` // 1-based row with the column names
	Header    []string          `json:"header"`    // column names as they are in the file
	Sheets    []string          `json:"sheets"`
	Tables    []string          `json:"tables"` // Excel tables (Inserir > Tabela) on the data sheet
	Rows      int               `json:"rows"`
	Keys      map[string]string `json:"keys"` // "salesdoc|item" -> Arquivo Origem
}

type gravarReq struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Replace bool     `json:"replace"` // Regravar: overwrite the items already there instead of skipping them
	Backup  *bool    `json:"backup"`  // the screen's choice, so a switch flipped just before is not lost; nil = the saved one
}

type gravarResult struct {
	Path    string `json:"path"`
	Sheet   string `json:"sheet"`
	Backup  string `json:"backup"`
	Added   int    `json:"added"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
}

type organizarReq struct {
	Columns []string `json:"columns"` // the app's columns, in the app's order
	Backup  *bool    `json:"backup"`
}

type organizarResult struct {
	Path     string   `json:"path"`
	Sheet    string   `json:"sheet"`
	Backup   string   `json:"backup"`
	Rows     int      `json:"rows"`
	Renamed  int      `json:"renamed"`
	Added    []string `json:"added"`    // app columns the sheet did not have
	Kept     []string `json:"kept"`     // columns the app does not know, now after the app's
	TopRows  int      `json:"topRows"`  // rows above the names that were removed
	Formulas int      `json:"formulas"` // formulas turned into their values (their references would point elsewhere)
}

// ---------- names and keys ----------

var accents = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "ê", "e", "è", "e",
	"í", "i", "ì", "i", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ú", "u", "ü", "u", "ç", "c", "ñ", "n")

// aliases are other names the same base columns get (SAP field names, Portuguese, the Power Query output).
// ui/core.js has the same table.
var aliases = map[string]string{
	"salesdocument": "salesdoc", "documentodevendas": "salesdoc", "docvendas": "salesdoc", "ordemdevenda": "salesdoc",
	"ordemdevendas": "salesdoc", "vbeln": "salesdoc",
	"itm": "item", "posnr": "item", "itemsd": "item", "itemdaordem": "item",
	"criadoem": "createdon", "datadecriacao": "createdon", "erdat": "createdon",
	"origem": "arquivoorigem", "arquivo": "arquivoorigem", "arquivodeorigem": "arquivoorigem",
	"salesorg": "sorg", "salesorganization": "sorg", "orgvendas": "sorg", "vkorg": "sorg",
	"quantidade": "orderquantity", "qtd": "orderquantity", "qtde": "orderquantity", "kwmeng": "orderquantity",
	"matnr": "material",
}

// canon is the form two column names are compared in: "Sales Doc.", "SALES_DOC" and "Sales Document" are one column.
func canon(name string) string {
	var b strings.Builder
	for _, r := range accents.Replace(strings.ToLower(strings.TrimSpace(name))) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if a, ok := aliases[b.String()]; ok {
		return a
	}
	return b.String()
}

// keyPart normalizes a Sales Doc. or Item the same way ui/app.js does: "0005940235", 5940235 and
// "5940235" are the same order.
func keyPart(v any) string {
	s := strings.TrimSpace(fmt.Sprint(v))
	if v == nil || s == "" {
		return ""
	}
	if f, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64); err == nil && f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strings.TrimLeft(s, "0")
}

func itemKey(doc, item any) string {
	d, i := keyPart(doc), keyPart(item)
	if d == "" && i == "" {
		return ""
	}
	return d + "|" + i
}

func trimmed(row []string) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = strings.TrimSpace(c)
	}
	return out
}

func indexCanon(list []string, name string) int {
	c := canon(name)
	for i, s := range list {
		if s != "" && canon(s) == c {
			return i
		}
	}
	return -1
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

func hasKeyColumns(header []string) bool {
	return indexCanon(header, "Sales Doc.") >= 0 && indexCanon(header, "Item") >= 0
}

// ---------- finding the data ----------

type layout struct {
	sheet     string
	headerRow int        // 1-based
	header    []string   // trimmed
	rows      [][]string // every row of the sheet, raw values ("46202", not "29/06/2026")
}

// findData looks for the sheet and row with the column names: Consolidado first, then the other sheets in
// order, in each of their first rows. nil means no sheet has Sales Doc. and Item.
func findData(f *excelize.File) (*layout, error) {
	sheets := f.GetSheetList()
	for i, s := range sheets {
		if strings.EqualFold(s, dataSheet) {
			sheets[0], sheets[i] = sheets[i], sheets[0]
			break
		}
	}
	for _, sheet := range sheets {
		r, err := headerRowOf(f, sheet)
		if err != nil {
			return nil, err
		}
		if r == 0 {
			continue
		}
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, err
		}
		return &layout{sheet: sheet, headerRow: r, header: trimmed(rows[r-1]), rows: rows}, nil
	}
	return nil, nil
}

// headerRowOf reads only the first rows of a sheet, so big sheets with other things are cheap to skip.
func headerRowOf(f *excelize.File, sheet string) (int, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for n := 1; n <= headerScan && rows.Next(); n++ {
		cols, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return 0, err
		}
		if hasKeyColumns(trimmed(cols)) {
			return n, nil
		}
	}
	return 0, nil
}

func tablesOn(f *excelize.File, sheet string) []excelize.Table {
	t, _ := f.GetTables(sheet)
	return t
}

// readPlanilha says what the spreadsheet already has. It runs at every start and refresh, so it reads the file
// with the fast reader (sheet.go), the same way findData finds the data for writing; excelize, which loads the
// whole sheet, is only used for the names of Excel tables, when the workbook has any.
func readPlanilha(path string) (planilhaInfo, error) {
	info := planilhaInfo{Path: path, Keys: map[string]string{}, Header: []string{}, Sheets: []string{}, Tables: []string{}}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return info, nil
	}
	fail := func(err error) (planilhaInfo, error) {
		return info, fmt.Errorf("não foi possível abrir %s: %w", filepath.Base(path), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail(err)
	}
	pk, err := packageOf(data)
	if err != nil {
		return fail(err)
	}
	sheets, _, sst, err := pk.book()
	if err != nil {
		return fail(err)
	}
	info.Exists = true
	for _, s := range sheets {
		info.Sheets = append(info.Sheets, s.name)
	}
	// Consolidado first, then the others, in the order findData looks at them.
	for i, s := range sheets {
		if strings.EqualFold(s.name, dataSheet) {
			sheets[0], sheets[i] = sheets[i], sheets[0]
			break
		}
	}
	for _, s := range sheets {
		part := pk.file(s.part)
		if part == nil {
			continue
		}
		err := eachRow(part, sst, headerScan, func(n int, row []rowCell) bool {
			if cols := trimmed(rowTexts(row)); hasKeyColumns(cols) {
				info.HeaderRow, info.Header = n, cols
				return false
			}
			return true
		})
		if err != nil {
			return fail(err)
		}
		if info.HeaderRow == 0 {
			continue
		}
		info.Sheet = s.name
		iDoc, iItem, iFile := indexCanon(info.Header, "Sales Doc."), indexCanon(info.Header, "Item"), indexCanon(info.Header, "Arquivo Origem")
		err = eachRow(part, sst, 0, func(n int, row []rowCell) bool {
			if n <= info.HeaderRow {
				return true
			}
			r := rowTexts(row)
			if k := itemKey(cell(r, iDoc), cell(r, iItem)); k != "" {
				info.Keys[k] = strings.TrimSpace(cell(r, iFile))
				info.Rows++
			}
			return true
		})
		if err != nil {
			return fail(err)
		}
		if pk.hasTables() {
			if f, err := excelize.OpenFile(path); err == nil {
				for _, t := range tablesOn(f, s.name) {
					info.Tables = append(info.Tables, t.Name)
				}
				f.Close()
			}
		}
		return info, nil
	}
	return info, nil
}

// rowTexts lays a row out by column, as excelize's GetRows does: "" in the gaps, nothing after the last value.
func rowTexts(row []rowCell) []string {
	var out []string
	for _, c := range row {
		t := c.text()
		if t == "" {
			continue
		}
		for len(out) < c.col {
			out = append(out, "")
		}
		if c.col < len(out) {
			out[c.col] = t
		} else {
			out = append(out, t)
		}
	}
	return out
}

// ---------- writing ----------

func backupPath(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".bak" + filepath.Ext(path)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// openForWriting checks Excel is not holding the file, copies it to .bak.xlsx when backup is on, and opens it.
func openForWriting(path string, backup bool) (*excelize.File, string, error) {
	if w, err := os.OpenFile(path, os.O_RDWR, 0); err != nil {
		return nil, "", err
	} else {
		w.Close()
	}
	copyPath := ""
	if backup {
		copyPath = backupPath(path)
		if err := copyFile(path, copyPath); err != nil {
			return nil, "", fmt.Errorf("não foi possível fazer a cópia de segurança: %w", err)
		}
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("não foi possível abrir %s: %w", filepath.Base(path), err)
	}
	return f, copyPath, nil
}

// saveSwapped saves beside the original and then swaps, so a failure never leaves half a spreadsheet.
func saveSwapped(f *excelize.File, path string) error {
	tmp := filepath.Join(filepath.Dir(path), "~cba-gravando-"+filepath.Base(path))
	if err := f.SaveAs(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func cellName(col, row int) string {
	n, _ := excelize.CoordinatesToCellName(col, row)
	return n
}

func dateStyleOf(f *excelize.File) (int, error) {
	numFmt := "dd/mm/yyyy"
	return f.NewStyle(&excelize.Style{CustomNumFmt: &numFmt})
}

// growTables makes the Excel tables that start at the header row reach the last row and column, so rows added
// below them are inside the table (a pivot table or chart built on the table sees them).
func growTables(f *excelize.File, sheet string, headerRow, lastCol, lastRow int) error {
	for _, t := range tablesOn(f, sheet) {
		c1, r1, c2, r2, err := tableBounds(t.Range)
		if err != nil || r1 != headerRow {
			continue
		}
		if c2 >= lastCol && r2 >= lastRow {
			continue
		}
		t.Range = cellName(c1, r1) + ":" + cellName(max(c2, lastCol), max(r2, lastRow))
		if err := f.DeleteTable(t.Name); err != nil {
			return err
		}
		if err := f.AddTable(sheet, &t); err != nil {
			return err
		}
	}
	return nil
}

// setUsedRange writes the sheet's dimension (A1 to the last cell): Power Query and other readers trust it to
// know how far the data goes, and excelize keeps the one the file came with.
func setUsedRange(f *excelize.File, sheet string, cols, rows int) error {
	return f.SetSheetDimension(sheet, "A1:"+cellName(max(cols, 1), max(rows, 1)))
}

func tableBounds(ref string) (c1, r1, c2, r2 int, err error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return 0, 0, 0, 0, errors.New("intervalo inválido")
	}
	if c1, r1, err = excelize.CellNameToCoordinates(parts[0]); err != nil {
		return
	}
	c2, r2, err = excelize.CellNameToCoordinates(parts[1])
	return
}

// writePlanilha adds (or, with Replace, rewrites) the rows in the spreadsheet itself; backup first copies it to .bak.xlsx.
func writePlanilha(path string, req gravarReq, backup bool) (gravarResult, error) {
	res := gravarResult{Path: path}
	iDocReq, iItemReq := indexCanon(req.Columns, "Sales Doc."), indexCanon(req.Columns, "Item")
	if iDocReq < 0 || iItemReq < 0 {
		return res, errors.New("a lista de colunas precisa ter Sales Doc. e Item para gravar na planilha")
	}
	var f *excelize.File
	var err error
	if _, statErr := os.Stat(path); statErr == nil {
		if f, res.Backup, err = openForWriting(path, backup); err != nil {
			return res, err
		}
	} else {
		f = excelize.NewFile()
		f.SetSheetName("Sheet1", dataSheet)
	}
	defer f.Close()

	lay, err := findData(f)
	if err != nil {
		return res, err
	}
	fresh := lay == nil
	if fresh {
		// No sheet has the data yet: it goes to a Consolidado sheet (made if the workbook has none).
		if idx, _ := f.GetSheetIndex(dataSheet); idx < 0 {
			if _, err := f.NewSheet(dataSheet); err != nil {
				return res, err
			}
		}
		lay = &layout{sheet: dataSheet, headerRow: 1}
		if rows, _ := f.GetRows(dataSheet); len(rows) > 0 && strings.Join(trimmed(rows[0]), "") != "" {
			return res, fmt.Errorf("a aba %s de %s tem dados sem as colunas Sales Doc. e Item; escolha outra planilha ou apague essa aba", dataSheet, filepath.Base(path))
		}
	}
	res.Sheet = lay.sheet
	header := lay.header
	// The spreadsheet's own column order stays; columns it does not have yet go to the end.
	col := map[string]int{}
	for i, h := range header {
		if h != "" {
			col[canon(h)] = i + 1
		}
	}
	for _, c := range req.Columns {
		if _, ok := col[canon(c)]; !ok {
			header = append(header, c)
			col[canon(c)] = len(header)
			f.SetCellStr(lay.sheet, cellName(len(header), lay.headerRow), c)
		}
	}
	if fresh {
		f.SetPanes(lay.sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		for i, c := range header {
			name, _ := excelize.ColumnNumberToName(i + 1)
			f.SetColWidth(lay.sheet, name, name, math.Min(48, math.Max(10, float64(len(c))+4)))
		}
	}

	existing := map[string]int{}
	iDoc, iItem := indexCanon(header, "Sales Doc."), indexCanon(header, "Item")
	for i := lay.headerRow; i < len(lay.rows); i++ {
		if k := itemKey(cell(lay.rows[i], iDoc), cell(lay.rows[i], iItem)); k != "" {
			existing[k] = i + 1
		}
	}

	dateStyle, err := dateStyleOf(f)
	if err != nil {
		return res, err
	}
	next := max(len(lay.rows), lay.headerRow) + 1
	for _, values := range req.Rows {
		if len(values) != len(req.Columns) {
			return res, errors.New("linha com número de colunas diferente do cabeçalho")
		}
		key := itemKey(values[iDocReq], values[iItemReq])
		if key == "" {
			continue
		}
		row, found := existing[key]
		switch {
		case found && !req.Replace:
			res.Skipped++
			continue
		case found:
			res.Updated++
		default:
			row = next
			next++
			existing[key] = row
			res.Added++
		}
		for j, c := range req.Columns {
			name := cellName(col[canon(c)], row)
			switch v := values[j].(type) {
			case nil:
				if found {
					f.SetCellValue(lay.sheet, name, nil)
				}
			case float64:
				f.SetCellFloat(lay.sheet, name, v, -1, 64)
				if canon(c) == "createdon" {
					f.SetCellStyle(lay.sheet, name, name, dateStyle)
				}
			case string:
				f.SetCellStr(lay.sheet, name, v)
			default:
				f.SetCellValue(lay.sheet, name, fmt.Sprint(v))
			}
		}
	}
	if err := growTables(f, lay.sheet, lay.headerRow, len(header), next-1); err != nil {
		return res, err
	}
	width := len(header)
	for _, r := range lay.rows {
		width = max(width, len(r))
	}
	if err := setUsedRange(f, lay.sheet, width, max(len(lay.rows), next-1)); err != nil {
		return res, err
	}
	return res, saveSwapped(f, path)
}

// ---------- the app's layout ----------

var dateText = []string{"02/01/2006", "02.01.2006", "2006-01-02", "02/01/06", "02.01.06"}

// typed reads a cell as the value it holds: a number stays a number, text stays text, and for the base columns
// the app's own types are applied (Sales Doc., Item and Order Quantity as numbers, Created On as a date).
func typed(f *excelize.File, sheet, name, raw, column string) (v any, formula bool) {
	if raw == "" {
		return nil, false
	}
	if fx, _ := f.GetCellFormula(sheet, name); fx != "" {
		formula = true
	}
	num, numErr := strconv.ParseFloat(raw, 64)
	switch canon(column) {
	case "salesdoc", "item", "orderquantity":
		if p, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(raw), ",", ".", 1), 64); err == nil {
			return p, formula
		}
		return raw, formula
	case "createdon":
		if numErr == nil {
			return num, formula
		}
		for _, layout := range dateText {
			if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
				return float64(t.Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours() / 24), formula
			}
		}
		return raw, formula
	}
	if numErr == nil {
		if t, _ := f.GetCellType(sheet, name); t == excelize.CellTypeNumber || t == excelize.CellTypeDate || t == excelize.CellTypeUnset || (formula && t != excelize.CellTypeSharedString && t != excelize.CellTypeInlineString) {
			return num, formula
		}
	}
	return raw, formula
}

func organizePlanilha(path string, req organizarReq, backup bool) (organizarResult, error) {
	res := organizarResult{Path: path, Added: []string{}, Kept: []string{}}
	if len(req.Columns) == 0 || indexCanon(req.Columns, "Sales Doc.") < 0 || indexCanon(req.Columns, "Item") < 0 {
		return res, errors.New("a lista de colunas precisa ter Sales Doc. e Item")
	}
	f, copyPath, err := openForWriting(path, backup)
	if err != nil {
		return res, err
	}
	defer f.Close()
	res.Backup = copyPath
	lay, err := findData(f)
	if err != nil {
		return res, err
	}
	if lay == nil {
		return res, fmt.Errorf("nenhuma aba de %s tem as colunas Sales Doc. e Item", filepath.Base(path))
	}
	res.Sheet, res.TopRows = lay.sheet, lay.headerRow-1

	// New order: the app's columns, then the ones the app does not know, in the order they were in.
	newHeader := append([]string{}, req.Columns...)
	from := make([]int, 0, len(req.Columns)) // old index (0-based) for each new column, -1 = new column
	for _, c := range req.Columns {
		i := indexCanon(lay.header, c)
		from = append(from, i)
		if i < 0 {
			res.Added = append(res.Added, c)
		} else if lay.header[i] != c {
			res.Renamed++
		}
	}
	for i, h := range lay.header {
		if h != "" && indexCanon(req.Columns, h) < 0 {
			newHeader = append(newHeader, h)
			from = append(from, i)
			res.Kept = append(res.Kept, h)
		}
	}

	// Read every value with its type before moving anything.
	data := [][]any{}
	for r := lay.headerRow; r < len(lay.rows); r++ {
		raw := lay.rows[r]
		row := make([]any, len(newHeader))
		empty := true
		for j, i := range from {
			if i < 0 || i >= len(raw) || strings.TrimSpace(raw[i]) == "" {
				continue
			}
			v, fx := typed(f, lay.sheet, cellName(i+1, r+1), raw[i], newHeader[j])
			if fx {
				res.Formulas++
			}
			row[j], empty = v, false
		}
		if !empty {
			data = append(data, row)
		}
	}
	res.Rows = len(data)

	tables := tablesOn(f, lay.sheet)
	for _, t := range tables {
		if err := f.DeleteTable(t.Name); err != nil {
			return res, err
		}
	}
	if merged, err := f.GetMergeCells(lay.sheet); err == nil {
		for _, m := range merged {
			f.UnmergeCell(lay.sheet, m.GetStartAxis(), m.GetEndAxis())
		}
	}

	// Rewrite the sheet in place (other sheets and charts point at it by name): first empty every cell that had
	// something, then write the names and the rows in their new places.
	for r, row := range lay.rows {
		for c, v := range row {
			if v != "" {
				name := cellName(c+1, r+1)
				f.SetCellFormula(lay.sheet, name, "")
				f.SetCellValue(lay.sheet, name, nil)
			}
		}
	}
	if err := f.SetCellStyle(lay.sheet, "A1", cellName(len(newHeader), len(data)+1), 0); err != nil {
		return res, err
	}
	put := func(name string, v any) {
		switch x := v.(type) {
		case float64:
			f.SetCellFloat(lay.sheet, name, x, -1, 64)
		case string:
			f.SetCellStr(lay.sheet, name, x)
		}
	}
	for c, h := range newHeader {
		put(cellName(c+1, 1), h)
	}
	for r, row := range data {
		for c, v := range row {
			if v != nil {
				put(cellName(c+1, r+2), v)
			}
		}
	}
	dateStyle, err := dateStyleOf(f)
	if err != nil {
		return res, err
	}
	if i := indexCanon(newHeader, "Created On"); i >= 0 && len(data) > 0 {
		f.SetCellStyle(lay.sheet, cellName(i+1, 2), cellName(i+1, len(data)+1), dateStyle)
	}
	f.SetPanes(lay.sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	for i, c := range newHeader {
		name, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(lay.sheet, name, name, math.Min(48, math.Max(10, float64(len(c))+4)))
	}
	if err := setUsedRange(f, lay.sheet, len(newHeader), len(data)+1); err != nil {
		return res, err
	}
	// The Excel tables come back with their names (what pivot tables and formulas refer to), over the new range.
	for _, t := range tables {
		t.Range = "A1:" + cellName(len(newHeader), max(len(data)+1, 2))
		if err := f.AddTable(lay.sheet, &t); err != nil {
			return res, fmt.Errorf("não foi possível refazer a tabela %s: %w", t.Name, err)
		}
	}
	return res, saveSwapped(f, path)
}
