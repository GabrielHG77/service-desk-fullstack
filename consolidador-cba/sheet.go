package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The Data sheet is read here rather than in the window. An .xlsx is a zip of XML files; the sheet's XML (80 MB for
// a month from SAP) is scanned once, as a stream, keeping only the columns the consolidation uses. The window gets a
// compact table back (each distinct text and number once, the cells as codes) and pivots it as before (processSheet
// in ui/core.js). Read this way a month takes under a second; the SheetJS library took 10 s and froze the window.

// sourceColumns are the Data columns the consolidation reads (SRC in ui/core.js).
var sourceColumns = []string{"Sales Doc.", "Item", "Created On", "SOrg.", "Material", "Order Quantity", "Internal char no.", "Characteristic Value", "Val from"}

// requiredColumns pick out the data sheet when it has another name (REQUIRED in ui/core.js).
var requiredColumns = []string{"Sales Doc.", "Item", "Internal char no."}

const (
	msgCorrupt  = "Não foi possível abrir o arquivo. Ele pode estar corrompido ou não ser um Excel válido."
	msgPassword = "O arquivo está protegido por senha. Abra no Excel, tire a senha e salve de novo."
)

// fileProblem is something wrong with the file itself, said to the user; reading it again does not help.
type fileProblem string

func (e fileProblem) Error() string { return string(e) }

// cellValue is a value as the window sees it: text, number or nothing. sst is its index in the shared strings, or -1.
type cellValue struct {
	kind byte // 0 nothing, 's' text, 'n' number
	s    string
	n    float64
	sst  int
}

// rowCell is a value with its column (0 = A).
type rowCell struct {
	col int
	cellValue
}

func jsSpace(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' }

// blank is blank() in core.js: nothing, or text with only spaces.
func (c cellValue) blank() bool {
	return c.kind == 0 || c.kind == 's' && strings.TrimFunc(c.s, jsSpace) == ""
}

// text is the value as JavaScript's String() writes it.
func (c cellValue) text() string {
	switch c.kind {
	case 's':
		return c.s
	case 'n':
		if a := math.Abs(c.n); a == 0 || a >= 1e-6 && a < 1e21 {
			return strconv.FormatFloat(c.n, 'f', -1, 64)
		}
		return strconv.FormatFloat(c.n, 'g', -1, 64)
	}
	return ""
}

func (c cellValue) json() any {
	switch c.kind {
	case 's':
		return c.s
	case 'n':
		return c.n
	}
	return nil
}

// normHeader is norm() in core.js: trimmed, inner spaces as one, lower case.
func normHeader(s string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(s, jsSpace), " "))
}

func anyFilled(row []rowCell) bool {
	for _, c := range row {
		if !c.blank() {
			return true
		}
	}
	return false
}

func hasRequired(head []rowCell) bool {
	for _, name := range requiredColumns {
		found := false
		for _, c := range head {
			if normHeader(c.text()) == normHeader(name) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sheetTable is the data sheet for the window: the header (only the source columns found) and, row by row, a code per
// column: -1 nothing, i >= 0 the text strs[i], i <= -2 the number nums[-2-i].
type sheetTable struct {
	head     []cellValue
	codes    []int32
	strs     []string
	nums     []float64
	strIdx   map[string]int32
	numIdx   map[float64]int32
	sstCode  []int32 // shared string -> code, so the common case skips hashing the text
	date1904 bool
	note     string
}

func (t *sheetTable) code(c cellValue) int32 {
	switch c.kind {
	case 's':
		if c.sst >= 0 && t.sstCode[c.sst] >= 0 {
			return t.sstCode[c.sst]
		}
		k, ok := t.strIdx[c.s]
		if !ok {
			k = int32(len(t.strs))
			t.strIdx[c.s] = k
			t.strs = append(t.strs, c.s)
		}
		if c.sst >= 0 {
			t.sstCode[c.sst] = k
		}
		return k
	case 'n':
		k, ok := t.numIdx[c.n]
		if !ok {
			k = int32(len(t.nums))
			t.numIdx[c.n] = k
			t.nums = append(t.nums, c.n)
		}
		return -2 - k
	}
	return -1
}

func (t *sheetTable) rows() int {
	if len(t.head) == 0 {
		return 0
	}
	return len(t.codes) / len(t.head)
}

// encode writes the table for the window: the size of a JSON part (header, texts, numbers, note), the JSON, spaces
// up to a multiple of 4 bytes, and the codes as little-endian int32 (an Int32Array on the other side, no parsing).
func (t *sheetTable) encode(w io.Writer) error {
	var head []any
	if t.head != nil {
		head = make([]any, len(t.head))
		for i, c := range t.head {
			head[i] = c.json()
		}
	}
	meta, err := json.Marshal(map[string]any{
		"head": head, "rows": t.rows(), "strs": t.strs, "nums": t.nums, "date1904": t.date1904, "note": t.note,
	})
	if err != nil {
		return err
	}
	buf := make([]byte, 0, 8+len(meta)+4*len(t.codes))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(meta)))
	buf = append(buf, meta...)
	for len(buf)%4 != 0 {
		buf = append(buf, ' ')
	}
	for _, c := range t.codes {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(c))
	}
	_, err = w.Write(buf)
	return err
}

// readDataSheet reads the sheet named want (or, without it, the first sheet with the consolidation's columns).
// Problems with the file come back as fileProblem; a file that cannot be read (locked, gone) as the system error.
func readDataSheet(p, want string) (*sheetTable, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	// .xlsx and .xlsm are zip ("PK"); with a password Excel saves another format (D0 CF 11 E0).
	if bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0}) {
		return nil, fileProblem(msgPassword)
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		return nil, fileProblem(msgCorrupt)
	}
	pk, err := packageOf(data)
	if err != nil {
		return nil, fileProblem(msgCorrupt)
	}
	t, err := readPackage(pk, want)
	if err != nil {
		if _, ok := err.(fileProblem); ok {
			return nil, err
		}
		return nil, fileProblem(msgCorrupt)
	}
	return t, nil
}

type sheetRef struct{ name, part string }

func readPackage(pk *xlsxPackage, want string) (*sheetTable, error) {
	sheets, date1904, sst, err := pk.book()
	if err != nil {
		return nil, err
	}

	pick, note := -1, ""
	for i, s := range sheets {
		if s.name == want {
			pick = i
			break
		}
	}
	if pick < 0 {
		for i, s := range sheets {
			if normHeader(s.name) == normHeader(want) {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		// No sheet with that name (someone saved the month with the sheet renamed): the first one with the columns.
		for i, s := range sheets {
			f := pk.file(s.part)
			if f == nil {
				continue
			}
			var head []rowCell
			err := eachRow(f, sst, 20, func(_ int, row []rowCell) bool {
				if !anyFilled(row) {
					return true
				}
				head = append(head, row...)
				return false
			})
			if err != nil {
				return nil, err
			}
			if hasRequired(head) {
				pick = i
				note = fmt.Sprintf(`A aba se chama "%s" em vez de "%s". Foi lida assim mesmo, porque tem as colunas certas.`, s.name, want)
				break
			}
		}
	}
	if pick < 0 {
		names := make([]string, len(sheets))
		for i, s := range sheets {
			names[i] = s.name
		}
		list := strings.Join(names, ", ")
		if list == "" {
			list = "nenhuma"
		}
		return nil, fileProblem(fmt.Sprintf(`Não tem a aba "%s", e nenhuma outra aba tem as colunas Sales Doc., Item e Internal char no. Abas do arquivo: %s.`, want, list))
	}

	t := &sheetTable{strs: []string{}, nums: []float64{}, strIdx: map[string]int32{}, numIdx: map[float64]int32{},
		sstCode: make([]int32, len(sst)), date1904: date1904, note: note}
	for i := range t.sstCode {
		t.sstCode[i] = -1
	}
	f := pk.file(sheets[pick].part)
	if f == nil {
		return t, nil // a sheet with nothing in it
	}
	var slot []int // column -> position in the table, -1 for a column the consolidation does not read
	var vals []cellValue
	err = eachRow(f, sst, 0, func(_ int, row []rowCell) bool {
		if t.head == nil {
			// The first row with something in it is the header, as in processSheet.
			if !anyFilled(row) {
				return true
			}
			t.head = []cellValue{}
			for _, name := range sourceColumns {
				at := -1 // the leftmost column with this name, as indexOf finds it
				for j, c := range row {
					if normHeader(c.text()) == normHeader(name) && (at < 0 || c.col < row[at].col) {
						at = j
					}
				}
				if at < 0 {
					continue
				}
				for len(slot) <= row[at].col {
					slot = append(slot, -1)
				}
				slot[row[at].col] = len(t.head)
				h := row[at].cellValue
				h.sst = -1
				t.head = append(t.head, h)
			}
			vals = make([]cellValue, len(t.head))
			return true
		}
		for i := range vals {
			vals[i] = cellValue{sst: -1}
		}
		for _, c := range row {
			if c.col < len(slot) && slot[c.col] >= 0 {
				vals[slot[c.col]] = c.cellValue
			}
		}
		filled := false
		for _, v := range vals {
			if !v.blank() {
				filled = true
				break
			}
		}
		if !filled {
			return true // nothing the consolidation reads: processSheet would skip it
		}
		for _, v := range vals {
			t.codes = append(t.codes, t.code(v))
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ---------- the sheet XML ----------

const (
	cellNumber = iota
	cellShared
	cellText // t="str", a formula's text
	cellInline
	cellBool
	cellError
	cellDate
)

// eachRow streams the rows of a sheet, up to row maxRow when it is above 0, until fn returns false. fn gets the
// row's number (1-based) and its cells; the slice is reused for the next row.
func eachRow(f *zip.File, sst []string, maxRow int, fn func(int, []rowCell) bool) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	x := newXMLScanner(rc)
	var (
		row          []rowCell
		rowNum, col  int
		typ          int
		val, inline  []byte
		hasV         bool
		inV, inIs    bool
		inT          bool
		phonetic     int
		inSheetData  bool
		cellOpen     bool
		finishedCell = func() {
			c := cellValue{sst: -1}
			switch typ {
			case cellShared:
				if n, ok := atoi(bytes.TrimSpace(val)); ok && n < len(sst) {
					c = cellValue{kind: 's', s: sst[n], sst: n}
				}
			case cellText:
				if hasV {
					c = cellValue{kind: 's', s: string(appendText(nil, val)), sst: -1}
				}
			case cellInline:
				c = cellValue{kind: 's', s: string(inline), sst: -1}
			case cellBool:
				// SheetJS gives true/false, which every step of the consolidation treats as the text "true"/"false".
				if hasV {
					s := "false"
					if v := string(bytes.TrimSpace(val)); v == "1" || v == "true" {
						s = "true"
					}
					c = cellValue{kind: 's', s: s, sst: -1}
				}
			case cellDate:
				if n, ok := isoSerial(string(bytes.TrimSpace(val))); ok {
					c = cellValue{kind: 'n', n: n, sst: -1}
				}
			case cellNumber:
				if v := bytes.TrimSpace(val); len(v) > 0 {
					if n, err := strconv.ParseFloat(string(v), 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
						c = cellValue{kind: 'n', n: n, sst: -1}
					}
				}
			}
			if c.kind != 0 {
				row = append(row, rowCell{col, c})
			}
		}
	)
	for {
		if err := x.next(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if inV {
			val = append(val, x.text...)
		} else if inT {
			inline = appendText(inline, x.text)
		}
		if cdata, ok := x.cdata(); ok {
			if inV {
				val = append(val, cdata...)
			} else if inT {
				inline = append(inline, cdata...)
			}
			continue
		}
		name, closing, empty := x.name()
		if !inSheetData {
			if string(name) == "sheetData" && !closing {
				if empty {
					return nil
				}
				inSheetData = true
			}
			continue
		}
		switch string(name) {
		case "row":
			if closing {
				if !fn(rowNum, row) {
					return nil
				}
				continue
			}
			rowNum++
			if v, ok := attr(x.tag, "r"); ok {
				if n, ok := atoi(v); ok {
					rowNum = n
				}
			}
			if maxRow > 0 && rowNum > maxRow {
				return nil
			}
			row, col = row[:0], -1
			if empty && !fn(rowNum, row) {
				return nil
			}
		case "c":
			if closing {
				if cellOpen {
					finishedCell()
					cellOpen = false
				}
				continue
			}
			col++
			if v, ok := attr(x.tag, "r"); ok {
				if c, ok := columnIndex(v); ok {
					col = c
				}
			}
			typ = cellNumber
			if v, ok := attr(x.tag, "t"); ok {
				switch string(v) {
				case "s":
					typ = cellShared
				case "str":
					typ = cellText
				case "inlineStr":
					typ = cellInline
				case "b":
					typ = cellBool
				case "e":
					typ = cellError
				case "d":
					typ = cellDate
				}
			}
			val, inline, hasV = val[:0], inline[:0], false
			cellOpen = !empty
		case "v":
			if !closing {
				hasV = true
				inV = !empty
			} else {
				inV = false
			}
		case "is":
			inIs = !closing && !empty
		case "t":
			if inIs && phonetic == 0 {
				inT = !closing && !empty
			}
		case "rPh":
			if !empty {
				if closing {
					phonetic--
				} else {
					phonetic++
				}
			}
		case "sheetData":
			if closing {
				return nil
			}
		}
	}
}

// readSharedStrings reads xl/sharedStrings.xml: each <si> is a text, the <t> pieces of its runs joined, without the
// phonetic guide (<rPh>), as SheetJS reads it.
func readSharedStrings(f *zip.File) ([]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	x := newXMLScanner(rc)
	var (
		out       []string
		buf       []byte
		inSI, inT bool
		phonetic  int
	)
	for {
		if err := x.next(); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		if inT {
			buf = appendText(buf, x.text)
		}
		if cdata, ok := x.cdata(); ok {
			if inT {
				buf = append(buf, cdata...)
			}
			continue
		}
		name, closing, empty := x.name()
		switch string(name) {
		case "si":
			switch {
			case empty:
				out = append(out, "")
			case closing:
				out = append(out, string(buf))
				inSI = false
			default:
				inSI, buf = true, buf[:0]
			}
		case "t":
			if inSI && phonetic == 0 {
				inT = !closing && !empty
			}
		case "rPh":
			if !empty {
				if closing {
					phonetic--
				} else {
					phonetic++
				}
			}
		}
	}
}

// isoSerial turns a t="d" cell (ISO 8601) into Excel's day number, as SheetJS does without cellDates.
func isoSerial(s string) (float64, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			y, m, d := t.Date()
			days := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours() / 24
			secs := float64(t.Hour()*3600+t.Minute()*60+t.Second()) + float64(t.Nanosecond())/1e9
			return days + secs/86400, true
		}
	}
	return 0, false
}

func atoi(b []byte) (int, bool) {
	if len(b) == 0 || len(b) > 9 {
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// columnIndex reads the column of a reference: "C2" -> 2.
func columnIndex(ref []byte) (int, bool) {
	n, i := 0, 0
	for ; i < len(ref); i++ {
		c := ref[i] | 0x20 // lower case
		if c < 'a' || c > 'z' {
			break
		}
		n = n*26 + int(c-'a'+1)
	}
	if i == 0 || n > 16384 {
		return 0, false
	}
	return n - 1, true
}

// ---------- a small XML scanner ----------

// xmlScanner splits XML into tags and the text before each one. The spreadsheet parts are generated by programs, so
// this is enough: no DTDs, and '>' appears unescaped only in text, comments and CDATA.
type xmlScanner struct {
	r    *bufio.Reader
	text []byte // the text before the tag, still escaped
	tag  []byte // the tag without < and >
}

func newXMLScanner(r io.Reader) *xmlScanner {
	return &xmlScanner{r: bufio.NewReaderSize(r, 256<<10)}
}

func (x *xmlScanner) readTo(dst []byte, delim byte) ([]byte, error) {
	dst = dst[:0]
	for {
		s, err := x.r.ReadSlice(delim)
		dst = append(dst, s...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return dst, err
		}
		return dst[:len(dst)-1], nil
	}
}

var (
	commentOpen, commentClose = []byte("!--"), []byte("--")
	cdataOpen, cdataClose     = []byte("![CDATA["), []byte("]]")
)

// next moves to the next tag; io.EOF at the end.
func (x *xmlScanner) next() error {
	var err error
	if x.text, err = x.readTo(x.text, '<'); err != nil {
		return err
	}
	if x.tag, err = x.readTo(x.tag, '>'); err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	// A comment or a CDATA section may hold '>': read on to its real end.
	for _, d := range [...][2][]byte{{commentOpen, commentClose}, {cdataOpen, cdataClose}} {
		if !bytes.HasPrefix(x.tag, d[0]) {
			continue
		}
		for len(x.tag) < len(d[0])+len(d[1]) || !bytes.HasSuffix(x.tag, d[1]) {
			more, err := x.readTo(nil, '>')
			if err != nil {
				return io.ErrUnexpectedEOF
			}
			x.tag = append(append(x.tag, '>'), more...)
		}
	}
	return nil
}

// cdata returns the content of a CDATA section, when the tag is one.
func (x *xmlScanner) cdata() ([]byte, bool) {
	if !bytes.HasPrefix(x.tag, cdataOpen) {
		return nil, false
	}
	return x.tag[len(cdataOpen) : len(x.tag)-len(cdataClose)], true
}

// name returns the tag's name without a namespace prefix, and whether it closes (</x>) or is empty (<x/>).
func (x *xmlScanner) name() (name []byte, closing, empty bool) {
	t := x.tag
	if len(t) > 0 && t[0] == '/' {
		closing, t = true, t[1:]
	}
	if len(t) > 0 && t[len(t)-1] == '/' {
		empty, t = true, t[:len(t)-1]
	}
	end := 0
	for end < len(t) && !isSpace(t[end]) {
		end++
	}
	name = t[:end]
	if i := bytes.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return name, closing, empty
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// attr returns an attribute of a tag (still escaped), by its name without a namespace prefix.
// A byte loop: it runs twice for each of the two million cells of a month.
func attr(tag []byte, key string) ([]byte, bool) {
	i := 0
	for i < len(tag) && !isSpace(tag[i]) {
		i++ // the tag's name
	}
	for {
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		k := i
		for i < len(tag) && tag[i] != '=' && !isSpace(tag[i]) {
			i++
		}
		name := tag[k:i]
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			return nil, false
		}
		i++
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '"' && tag[i] != '\'' {
			return nil, false
		}
		q := tag[i]
		i++
		v := i
		for i < len(tag) && tag[i] != q {
			i++
		}
		if i >= len(tag) {
			return nil, false
		}
		val := tag[v:i]
		i++
		if j := bytes.IndexByte(name, ':'); j >= 0 {
			name = name[j+1:]
		}
		if string(name) == key {
			return val, true
		}
	}
}

// appendText appends XML text with its escapes resolved: entities (&amp; &#38;) and then _xHHHH_, the way Excel
// writes control characters. Same order as SheetJS's unescapexml.
func appendText(dst, s []byte) []byte {
	start := len(dst)
	for len(s) > 0 {
		i := bytes.IndexByte(s, '&')
		if i < 0 {
			dst = append(dst, s...)
			break
		}
		dst = append(dst, s[:i]...)
		s = s[i:]
		r, n := entity(s)
		if n == 0 {
			dst = append(dst, '&')
			s = s[1:]
			continue
		}
		dst = utf8.AppendRune(dst, r)
		s = s[n:]
	}
	if bytes.Contains(dst[start:], []byte("_x")) || bytes.Contains(dst[start:], []byte("_X")) {
		dst = append(dst[:start], decodeX(dst[start:])...)
	}
	return dst
}

// entity reads an entity at the start of s and returns its character and length, or length 0.
func entity(s []byte) (rune, int) {
	semi := bytes.IndexByte(s, ';')
	if semi < 2 || semi > 10 {
		return 0, 0
	}
	switch e := string(s[1:semi]); e {
	case "amp":
		return '&', semi + 1
	case "lt":
		return '<', semi + 1
	case "gt":
		return '>', semi + 1
	case "quot":
		return '"', semi + 1
	case "apos":
		return '\'', semi + 1
	default:
		base, digits := 10, e[1:]
		if e[0] != '#' || digits == "" {
			return 0, 0
		}
		if digits[0] == 'x' || digits[0] == 'X' {
			base, digits = 16, digits[1:]
		}
		n, err := strconv.ParseUint(digits, base, 32)
		if err != nil || digits == "" {
			return 0, 0
		}
		return rune(n), semi + 1
	}
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'f' }

func decodeX(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == '_' && i+6 < len(b) && b[i+1]|0x20 == 'x' && b[i+6] == '_' &&
			isHex(b[i+2]) && isHex(b[i+3]) && isHex(b[i+4]) && isHex(b[i+5]) {
			n, _ := strconv.ParseUint(string(b[i+2:i+6]), 16, 32)
			out = utf8.AppendRune(out, rune(n))
			i += 7
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// ---------- the parts of the package ----------

type xlsxPackage struct {
	files map[string]*zip.File // by lower-case name: some programs write the names in another case
}

// packageOf opens an .xlsx read whole into memory. The files live on a network drive: one read of 8 MB is much faster
// there than the zip reader's thousands of small reads at different places.
func packageOf(data []byte) (*xlsxPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	return newPackage(zr), nil
}

// book reads what every reading needs: the sheets in order (with their parts), the date system and the shared texts.
func (pk *xlsxPackage) book() ([]sheetRef, bool, []string, error) {
	wb := "xl/workbook.xml"
	if rels, err := pk.rels(""); err == nil {
		for _, r := range rels {
			if strings.HasSuffix(r.typ, "/officeDocument") {
				wb = r.target
			}
		}
	}
	sheets, date1904, err := pk.workbook(wb)
	if err != nil {
		return nil, false, nil, err
	}
	rels, _ := pk.rels(wb)
	sstPart := path.Join(path.Dir(wb), "sharedStrings.xml")
	byID := map[string]string{}
	for _, r := range rels {
		byID[r.id] = r.target
		if strings.HasSuffix(r.typ, "/sharedStrings") {
			sstPart = r.target
		}
	}
	for i := range sheets {
		sheets[i].part = byID[sheets[i].part]
	}
	var sst []string
	if f := pk.file(sstPart); f != nil {
		if sst, err = readSharedStrings(f); err != nil {
			return nil, false, nil, err
		}
	}
	return sheets, date1904, sst, nil
}

// queryTableOn returns the name of an Excel table on the sheet that a Power Query query fills, or "".
func (pk *xlsxPackage) queryTableOn(sheetPart string) string {
	rels, _ := pk.rels(sheetPart)
	for _, r := range rels {
		if !strings.HasSuffix(r.typ, "/table") {
			continue
		}
		trels, _ := pk.rels(r.target)
		for _, tr := range trels {
			if !strings.HasSuffix(tr.typ, "/queryTable") {
				continue
			}
			name := path.Base(r.target)
			if f := pk.file(r.target); f != nil {
				eachTag(f, func(x *xmlScanner, tag []byte) {
					if string(tag) == "table" {
						if v, ok := attr(x.tag, "name"); ok {
							name = string(appendText(nil, v))
						}
					}
				})
			}
			return name
		}
	}
	return ""
}

// hasTables says whether the workbook has any Excel table (Inserir > Tabela) at all.
func (pk *xlsxPackage) hasTables() bool {
	for name := range pk.files {
		if strings.Contains(name, "tables/") && strings.HasSuffix(name, ".xml") {
			return true
		}
	}
	return false
}

func newPackage(zr *zip.Reader) *xlsxPackage {
	pk := &xlsxPackage{files: map[string]*zip.File{}}
	for _, f := range zr.File {
		pk.files[strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), "/"))] = f
	}
	return pk
}

func (pk *xlsxPackage) file(name string) *zip.File {
	return pk.files[strings.ToLower(strings.TrimPrefix(name, "/"))]
}

type relationship struct{ id, typ, target string }

// rels reads the relationships of a part ("" for the package), with the targets as full part names.
func (pk *xlsxPackage) rels(part string) ([]relationship, error) {
	dir, base := path.Split(part)
	f := pk.file(dir + "_rels/" + base + ".rels")
	if f == nil {
		return nil, nil
	}
	var out []relationship
	err := eachTag(f, func(x *xmlScanner, name []byte) {
		if string(name) != "Relationship" {
			return
		}
		get := func(k string) string { v, _ := attr(x.tag, k); return string(appendText(nil, v)) }
		if get("TargetMode") == "External" {
			return
		}
		target := get("Target")
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(dir, target)
		}
		out = append(out, relationship{id: get("Id"), typ: get("Type"), target: target})
	})
	return out, err
}

// workbook reads the sheets, in order (part holds the relationship id), and whether dates count from 1904.
func (pk *xlsxPackage) workbook(part string) ([]sheetRef, bool, error) {
	f := pk.file(part)
	if f == nil {
		return nil, false, errors.New("sem workbook.xml")
	}
	var sheets []sheetRef
	date1904 := false
	err := eachTag(f, func(x *xmlScanner, name []byte) {
		switch string(name) {
		case "sheet":
			n, _ := attr(x.tag, "name")
			id, _ := attr(x.tag, "id") // r:id
			sheets = append(sheets, sheetRef{name: string(appendText(nil, n)), part: string(id)})
		case "workbookPr":
			if v, ok := attr(x.tag, "date1904"); ok {
				date1904 = string(v) == "1" || string(v) == "true"
			}
		}
	})
	return sheets, date1904, err
}

// eachTag calls fn for each opening tag of a small part.
func eachTag(f *zip.File, fn func(*xmlScanner, []byte)) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	x := newXMLScanner(rc)
	for {
		if err := x.next(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if name, closing, _ := x.name(); !closing {
			fn(x, name)
		}
	}
}
