package main

import (
	"strconv"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// exportTable is the consolidated table as the window sends it for an Excel copy: the columns, which one holds
// dates (-1 for none), and each row as pairs of column index and value, only for the cells with something in them.
// With the characteristics outside the list there are 1,700 columns, mostly empty, so pairs keep it small.
type exportTable struct {
	Cols    []string `json:"cols"`
	DateCol int      `json:"dateCol"`
	Rows    [][]any  `json:"rows"`
}

// writeTableXlsx writes the Excel copy as a stream, so its size does not matter: one sheet, Consolidado, a filter on
// the header, the dates as dd/mm/yyyy and each column as wide as its first 500 rows need.
func writeTableXlsx(path string, t exportTable) error {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", dataSheet); err != nil {
		return err
	}
	if len(t.Cols) == 0 {
		return f.SaveAs(path)
	}
	dateFormat := "dd/mm/yyyy"
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &dateFormat})
	if err != nil {
		return err
	}
	last, _ := excelize.CoordinatesToCellName(len(t.Cols), len(t.Rows)+1)
	// Set before the stream writer starts: it keeps the filter and writes it after the rows.
	if err := f.AutoFilter(dataSheet, "A1:"+last, nil); err != nil {
		return err
	}
	sw, err := f.NewStreamWriter(dataSheet)
	if err != nil {
		return err
	}

	rows := make([][]any, len(t.Rows))
	width := make([]int, len(t.Cols))
	for i, c := range t.Cols {
		width[i] = utf8.RuneCountInString(c)
	}
	for r, pairs := range t.Rows {
		row := make([]any, len(t.Cols))
		for k := 0; k+1 < len(pairs); k += 2 {
			n, ok := pairs[k].(float64)
			i := int(n)
			if !ok || i < 0 || i >= len(row) {
				continue
			}
			v := pairs[k+1]
			if r < 500 {
				w := 10 // a date
				if i != t.DateCol {
					w = utf8.RuneCountInString(valueText(v))
				}
				width[i] = max(width[i], w)
			}
			if num, isNum := v.(float64); isNum && i == t.DateCol {
				v = excelize.Cell{StyleID: dateStyle, Value: num}
			}
			row[i] = v
		}
		rows[r] = row
	}
	widths := make([]float64, len(width))
	for i, c := range width {
		widths[i] = float64(min(48, c+2))
	}
	for _, r := range widthRuns(widths) {
		if err := sw.SetColWidth(r.from, r.to, r.width); err != nil {
			return err
		}
	}

	header := make([]any, len(t.Cols))
	for i, c := range t.Cols {
		header[i] = c
	}
	if err := sw.SetRow("A1", header); err != nil {
		return err
	}
	for r, row := range rows {
		ref, _ := excelize.CoordinatesToCellName(1, r+2)
		if err := sw.SetRow(ref, row); err != nil {
			return err
		}
	}
	if err := sw.Flush(); err != nil {
		return err
	}
	return f.SaveAs(path)
}

// exactWidths is how many columns get their own width. excelize goes over every column already set each time a width
// is set and for each cell it writes, so with the 1,700 characteristics outside the list, those after these share one
// width (the most common among them): the columns of the list, which are the ones read, keep theirs. Used by the
// Excel copy and by organizePlanilha.
const exactWidths = 40

type widthRun struct {
	from, to int // 1-based columns
	width    float64
}

// widthRuns groups the columns' widths into runs, neighbours with the same width together.
func widthRuns(widths []float64) []widthRun {
	w := append([]float64(nil), widths...)
	if len(w) > exactWidths {
		count := map[float64]int{}
		common := 0.0
		for _, x := range w[exactWidths:] {
			count[x]++
			if count[x] > count[common] || count[x] == count[common] && x < common {
				common = x
			}
		}
		for i := exactWidths; i < len(w); i++ {
			w[i] = common
		}
	}
	var runs []widthRun
	for i, x := range w {
		if n := len(runs); n > 0 && runs[n-1].width == x {
			runs[n-1].to = i + 1
			continue
		}
		runs = append(runs, widthRun{i + 1, i + 1, x})
	}
	return runs
}

func valueText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
