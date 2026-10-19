package main

// One text file with everything this program and the page did, for a person (or an assistant) to read:
//
//	2026-10-19 14:03:22.123 T+725.410 relay.end c=3 by=the_broker_or_the_network dur=130.002 err=EOF
//
// local time, seconds since the program started, tag.event, then key=value pairs (a value is quoted when it needs it).
// The page posts its own lines to /log, so both sides land in one file with one clock; the page sorts them when it exports.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const stampFmt = "2006-01-02 15:04:05.000"

var (
	startTime = time.Now()
	runID     = startTime.Format("0102-150405")
	pageSHA   = "?" // first digits of the hash of the page this program serves: says which version of the page is running
	safeVal   = regexp.MustCompile(`^[\w.:/+#$@<>-]+$`)
	elog      *eventLog // nil when there is no file (tests, or a folder that cannot be written)
)

type eventLog struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size int64
	max  int64
}

// openLog opens the file for appending. A file that is already over the limit is moved to path.1 first (one old file is kept).
func openLog(path string, max int64) (*eventLog, error) {
	l := &eventLog{path: path, max: max}
	if st, err := os.Stat(path); err == nil && st.Size() >= max {
		os.Remove(path + ".1")
		os.Rename(path, path+".1")
	}
	if err := l.reopen(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *eventLog) reopen() error {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	l.f, l.size = f, st.Size()
	return nil
}

// write appends lines that are already formatted (each ends with a newline).
func (l *eventLog) write(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	if l.size > 0 && l.size+int64(len(s)) > l.max {
		l.f.Close()
		os.Remove(l.path + ".1")
		os.Rename(l.path, l.path+".1")
		if err := l.reopen(); err != nil {
			l.f = nil
			return
		}
	}
	n, _ := l.f.WriteString(s)
	l.size += int64(n)
}

// tail returns the last n lines of the old file and the current one, in the order they were written.
func (l *eventLog) tail(n int) []string {
	lines := append(readLines(l.path+".1", 4<<20), readLines(l.path, 4<<20)...)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func readLines(path string, maxBytes int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	cut := false
	if st, err := f.Stat(); err == nil && st.Size() > maxBytes {
		f.Seek(st.Size()-maxBytes, io.SeekStart)
		cut = true
	}
	data, _ := io.ReadAll(f)
	s := string(data)
	if cut { // the first line is probably cut in the middle
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// evlog writes one event: tag.event and then key, value, key, value...
func evlog(tag string, kv ...any) {
	l := elog
	if l == nil {
		return
	}
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s T+%.3f %s", now.Format(stampFmt), now.Sub(startTime).Seconds(), tag)
	for i := 0; i < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		if i+1 < len(kv) {
			b.WriteString(fv(kv[i+1]))
		} else {
			b.WriteString("null")
		}
	}
	b.WriteByte('\n')
	l.write(b.String())
}

// fv formats a value: numbers, booleans and simple words as they are, anything else in quotes on one line.
func fv(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return qs(x)
	case []byte:
		return qb(x)
	case error:
		return qs(x.Error())
	case time.Duration:
		return strconv.FormatFloat(x.Seconds(), 'f', 3, 64)
	case float64:
		return strconv.FormatFloat(x, 'f', 3, 64)
	case bool:
		return strconv.FormatBool(x)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x)
	default:
		return qs(fmt.Sprint(x))
	}
}

// qs quotes a string unless it is a simple word, and cuts it after 300 characters saying how many were left out.
func qs(s string) string {
	r := []rune(s)
	if len(r) > 300 {
		return strconv.Quote(string(r[:300]) + fmt.Sprintf("…(+%d)", len(r)-300))
	}
	if s != "" && safeVal.MatchString(s) {
		return s
	}
	return strconv.Quote(s)
}

// qb is qs for a message payload: text as text, anything else as hexadecimal.
func qb(b []byte) string {
	text := utf8.Valid(b)
	for _, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			text = false
		}
	}
	if text {
		return qs(string(b))
	}
	n := min(len(b), 48)
	more := ""
	if len(b) > n {
		more = fmt.Sprintf(" +%d bytes", len(b)-n)
	}
	return strconv.Quote("hex:" + hex.EncodeToString(b[:n]) + more)
}

// netState lists the IPv4 addresses of the network interfaces that are up, so a Wi-Fi that changed or dropped shows in the log.
func netState() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return "error: " + err.Error()
	}
	var parts []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		var v4 []string
		for _, a := range addrs {
			if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
				v4 = append(v4, a.String())
			}
		}
		if len(v4) > 0 {
			parts = append(parts, ifc.Name+"="+strings.Join(v4, ","))
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "(no network)"
	}
	return strings.Join(parts, " | ")
}

// progInfo describes this program for the page, which puts it at the top of what it exports.
func progInfo(port int) map[string]any {
	host, _ := os.Hostname()
	exe, _ := os.Executable()
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	m := map[string]any{"run": runID, "startedAt": startTime.UnixMilli(), "now": time.Now().UnixMilli(), "pid": os.Getpid(), "host": host, "user": who,
		"exe": exe, "go": runtime.Version(), "os": runtime.GOOS + "/" + runtime.GOARCH, "page": pageSHA, "port": port, "net": netState()}
	if elog != nil {
		m["logPath"], m["logMax"] = elog.path, elog.max
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if strings.HasPrefix(s.Key, "vcs.") {
				m[s.Key] = s.Value
			}
		}
	}
	return m
}

// logRoutes: the page posts its lines to /log, reads the file back to export it, and asks /info what program it is talking to.
func logRoutes(mux *http.ServeMux, port int) {
	own := func(r *http.Request) bool { // a page from another site must not write to the log or read it
		o := r.Header.Get("Origin")
		return o == "" || o == fmt.Sprintf("http://127.0.0.1:%d", port) || o == fmt.Sprintf("http://localhost:%d", port)
	}
	mux.HandleFunc("POST /log", func(w http.ResponseWriter, r *http.Request) {
		if !own(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		if elog != nil {
			var b strings.Builder
			for _, ln := range strings.Split(string(body), "\n") {
				ln = strings.TrimSpace(strings.Map(func(c rune) rune { // one line each, no control characters
					if c < 0x20 && c != '\t' {
						return -1
					}
					return c
				}, ln))
				if r := []rune(ln); len(r) > 4000 {
					ln = string(r[:4000]) + "…"
				}
				if ln != "" {
					b.WriteString(ln)
					b.WriteByte('\n')
				}
			}
			elog.write(b.String())
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /log", func(w http.ResponseWriter, r *http.Request) {
		if !own(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if n <= 0 {
			n = 2000
		}
		n = min(n, 20000)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if elog != nil {
			io.WriteString(w, strings.Join(elog.tail(n), "\n")+"\n")
		}
	})
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		if !own(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(progInfo(port))
	})
}
