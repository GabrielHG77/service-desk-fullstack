package main

// The consolidated spreadsheet, kept up to date the way FatorImportacao keeps ConsolidadoPath.xlsx:
// an update adds only the order items that are not there yet (Sales Doc. + Item is the key), a copy of the
// file is made before writing, and rewriting a file's items is an explicit action. Only the Consolidado sheet
// is written; other sheets and columns typed by hand are left as they are.

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

	"github.com/xuri/excelize/v2"
)

const dataSheet = "Consolidado"

type planilhaInfo struct {
	Path   string            `json:"path"`
	Exists bool              `json:"exists"`
	Rows   int               `json:"rows"`
	Keys   map[string]string `json:"keys"` // "salesdoc|item" -> Arquivo Origem
}

type gravarReq struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Replace bool     `json:"replace"` // Regravar: overwrite the items already there instead of skipping them
}

type gravarResult struct {
	Path    string `json:"path"`
	Backup  string `json:"backup"`
	Added   int    `json:"added"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
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

func indexOf(list []string, name string) int {
	for i, s := range list {
		if strings.EqualFold(s, name) {
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

// sheetRows reads the Consolidado sheet with raw values ("46202", not "29/06/2026").
// It returns nil rows when the workbook has no such sheet yet.
func sheetRows(f *excelize.File) ([][]string, error) {
	if idx, err := f.GetSheetIndex(dataSheet); err != nil || idx < 0 {
		return nil, nil
	}
	return f.GetRows(dataSheet, excelize.Options{RawCellValue: true})
}

func keyColumns(path string, header []string) (int, int, error) {
	iDoc, iItem := indexOf(header, "Sales Doc."), indexOf(header, "Item")
	if iDoc < 0 || iItem < 0 {
		return 0, 0, fmt.Errorf("a aba %s de %s não tem as colunas Sales Doc. e Item na primeira linha", dataSheet, filepath.Base(path))
	}
	return iDoc, iItem, nil
}

func readPlanilha(path string) (planilhaInfo, error) {
	info := planilhaInfo{Path: path, Keys: map[string]string{}}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return info, nil
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return info, fmt.Errorf("não foi possível abrir %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	info.Exists = true
	rows, err := sheetRows(f)
	if err != nil || len(rows) == 0 {
		return info, err
	}
	header := trimmed(rows[0])
	iDoc, iItem, err := keyColumns(path, header)
	if err != nil {
		return info, err
	}
	iFile := indexOf(header, "Arquivo Origem")
	for _, r := range rows[1:] {
		if k := itemKey(cell(r, iDoc), cell(r, iItem)); k != "" {
			info.Keys[k] = strings.TrimSpace(cell(r, iFile))
			info.Rows++
		}
	}
	return info, nil
}

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

func writePlanilha(path string, req gravarReq) (gravarResult, error) {
	res := gravarResult{Path: path}
	iDocReq, iItemReq := indexOf(req.Columns, "Sales Doc."), indexOf(req.Columns, "Item")
	if iDocReq < 0 || iItemReq < 0 {
		return res, errors.New("a lista de colunas precisa ter Sales Doc. e Item para gravar na planilha")
	}
	_, err := os.Stat(path)
	exists := err == nil
	var f *excelize.File
	if exists {
		// Excel keeps the file open for writing while it shows it: find out before touching anything.
		if w, err := os.OpenFile(path, os.O_RDWR, 0); err != nil {
			return res, err
		} else {
			w.Close()
		}
		res.Backup = backupPath(path)
		if err := copyFile(path, res.Backup); err != nil {
			return res, fmt.Errorf("não foi possível fazer a cópia de segurança: %w", err)
		}
		if f, err = excelize.OpenFile(path); err != nil {
			return res, fmt.Errorf("não foi possível abrir %s: %w", filepath.Base(path), err)
		}
	} else {
		f = excelize.NewFile()
		f.SetSheetName("Sheet1", dataSheet)
	}
	defer f.Close()
	if idx, err := f.GetSheetIndex(dataSheet); err != nil || idx < 0 {
		if _, err := f.NewSheet(dataSheet); err != nil {
			return res, err
		}
	}
	rows, err := sheetRows(f)
	if err != nil {
		return res, err
	}

	var header []string
	if len(rows) > 0 {
		header = trimmed(rows[0])
	}
	fresh := len(header) == 0 || (len(rows) == 1 && strings.Join(header, "") == "")
	if fresh {
		header, rows = nil, nil
	}
	// The spreadsheet's own column order stays; columns it does not have yet go to the end.
	col := map[string]int{}
	for i, h := range header {
		if h != "" {
			col[strings.ToLower(h)] = i + 1
		}
	}
	for _, c := range req.Columns {
		if _, ok := col[strings.ToLower(c)]; !ok {
			header = append(header, c)
			col[strings.ToLower(c)] = len(header)
			name, _ := excelize.CoordinatesToCellName(len(header), 1)
			f.SetCellStr(dataSheet, name, c)
		}
	}
	if fresh {
		f.SetPanes(dataSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		for i, c := range header {
			name, _ := excelize.ColumnNumberToName(i + 1)
			f.SetColWidth(dataSheet, name, name, math.Min(48, math.Max(10, float64(len(c))+4)))
		}
	}

	existing := map[string]int{}
	if len(rows) > 1 {
		iDoc, iItem, err := keyColumns(path, header)
		if err != nil {
			return res, err
		}
		for i, r := range rows[1:] {
			if k := itemKey(cell(r, iDoc), cell(r, iItem)); k != "" {
				existing[k] = i + 2
			}
		}
	}

	numFmt := "dd/mm/yyyy"
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &numFmt})
	if err != nil {
		return res, err
	}
	next := len(rows) + 1
	if next < 2 {
		next = 2
	}
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
			name, _ := excelize.CoordinatesToCellName(col[strings.ToLower(c)], row)
			switch v := values[j].(type) {
			case nil:
				if found {
					f.SetCellValue(dataSheet, name, nil)
				}
			case float64:
				f.SetCellFloat(dataSheet, name, v, -1, 64)
				if strings.EqualFold(c, "Created On") {
					f.SetCellStyle(dataSheet, name, name, dateStyle)
				}
			case string:
				f.SetCellStr(dataSheet, name, v)
			default:
				f.SetCellValue(dataSheet, name, fmt.Sprint(v))
			}
		}
	}

	// Saved beside the original and then swapped, so a failure never leaves half a spreadsheet.
	tmp := filepath.Join(filepath.Dir(path), "~cba-gravando-"+filepath.Base(path))
	if err := f.SaveAs(tmp); err != nil {
		os.Remove(tmp)
		return res, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return res, err
	}
	return res, nil
}
