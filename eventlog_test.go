package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// useLog points the event log at a temporary file for one test.
func useLog(t *testing.T, max int64) *eventLog {
	t.Helper()
	l, err := openLog(filepath.Join(t.TempDir(), "x.log"), max)
	if err != nil {
		t.Fatal(err)
	}
	old := elog
	elog = l
	t.Cleanup(func() { elog = old; l.f.Close() })
	return l
}

func TestEvlogFormat(t *testing.T) {
	l := useLog(t, 1<<20)
	evlog("x.y", "a", 1, "b", "two words", "c", []byte("hi"), "d", errors.New("boom"), "e", true, "f", 2*time.Second+500*time.Millisecond, "g", nil, "h", "")
	got := l.tail(10)
	want := regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} T\+\d+\.\d{3} x\.y a=1 b="two words" c=hi d=boom e=true f=2\.500 g=null h=""$`)
	if len(got) != 1 || !want.MatchString(got[0]) {
		t.Fatalf("line not as expected: %q", got)
	}
}

func TestEvlogValues(t *testing.T) {
	l := useLog(t, 1<<20)
	evlog("v", "long", strings.Repeat("é", 400), "bin", []byte{0xff, 0x00, 'a'}, "nl", "a\nb", "odd", "key")
	got := l.tail(10)
	if len(got) != 1 {
		t.Fatalf("an event must be one line, got %d: %q", len(got), got)
	}
	for _, want := range []string{`…(+100)"`, `bin="hex:ff0061"`, `nl="a\nb"`, ` odd=key`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("missing %q in %q", want, got[0])
		}
	}
	if n := strings.Count(got[0], "é"); n != 300 {
		t.Errorf("a long value is cut after 300 characters, kept %d", n)
	}
}

func TestEventLogRotates(t *testing.T) {
	l := useLog(t, 400)
	for i := 0; i < 20; i++ {
		evlog("n.n", "i", i, "pad", strings.Repeat("x", 20))
	}
	if _, err := os.Stat(l.path + ".1"); err != nil {
		t.Fatal("the old file was not kept")
	}
	if st, _ := os.Stat(l.path); st.Size() > 400 {
		t.Errorf("the file grew past its limit: %d", st.Size())
	}
	got := l.tail(100)
	idx := regexp.MustCompile(` i=(\d+) `)
	prev := -1
	for _, ln := range got {
		m := idx.FindStringSubmatch(ln)
		if m == nil {
			t.Fatalf("not an event line: %q", ln)
		}
		if i, _ := strconv.Atoi(m[1]); i <= prev {
			t.Fatalf("lines out of order across the two files: %d after %d", i, prev)
		} else {
			prev = i
		}
	}
	if prev != 19 {
		t.Errorf("the last event is missing, last seen %d", prev)
	}
	if got := l.tail(3); len(got) != 3 {
		t.Errorf("tail(3) returned %d lines", len(got))
	}
}

func TestLogRoutes(t *testing.T) {
	l := useLog(t, 1<<20)
	base := start(t)
	post := func(origin, body string) int {
		req, _ := http.NewRequest("POST", base+"/log", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post("http://evil.example", "2026-01-01 00:00:00.000 T+1.000 page.evil\n"); c != http.StatusForbidden {
		t.Fatalf("a page from another site could write to the log: %d", c)
	}
	if got := l.tail(10); len(got) != 0 {
		t.Fatalf("a refused post still wrote: %q", got)
	}
	body := "2026-01-01 00:00:00.000 T+1.000 page.test a=1\n\n  2026-01-01 00:00:00.001 T+1.001 page.test with\x00control\x01chars  \n" + strings.Repeat("z", 5000) + "\n"
	if c := post(base, body); c != http.StatusNoContent {
		t.Fatalf("a post from the page was refused: %d", c)
	}
	got := l.tail(10)
	if len(got) != 3 || got[0] != "2026-01-01 00:00:00.000 T+1.000 page.test a=1" || got[1] != "2026-01-01 00:00:00.001 T+1.001 page.test withcontrolchars" || len([]rune(got[2])) != 4001 {
		t.Fatalf("lines not kept as expected (one line each, no control characters, long ones cut): %q", got)
	}
	if code, txt := fetch(t, base+"/log?lines=2", ""); code != 200 || strings.Count(strings.TrimSpace(txt), "\n") != 1 || !strings.HasPrefix(txt, got[1]) {
		t.Fatalf("GET /log?lines=2: %d %q", code, txt)
	}
	req, _ := http.NewRequest("GET", base+"/log", nil)
	req.Header.Set("Origin", "http://evil.example")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a page from another site could read the log: %v %v", resp, err)
	}
	code, info := fetch(t, base+"/info", "")
	var m map[string]any
	if err := json.Unmarshal([]byte(info), &m); code != 200 || err != nil {
		t.Fatalf("/info: %d %v", code, err)
	}
	if m["logPath"] != l.path || m["run"] != runID || len(m["page"].(string)) != 8 || m["startedAt"].(float64) < 1e12 || m["os"] == "" {
		t.Errorf("/info is missing something: %v", m)
	}
}

func TestPacketsInEventLog(t *testing.T) {
	l := useLog(t, 1<<20)
	up := &pktLog{who: "->", want: 1, c: 7, dir: "page>broker"}
	down := &pktLog{who: "<-", want: 2, c: 7, dir: "broker>page"}
	connect := []byte{0x10, 26, 0, 4, 'M', 'Q', 'T', 'T', 4, 0xC2, 0, 60, 0, 6, 'a', 'b', 'c', 'd', 'e', 'f', 0, 2, 'g', 'b', 0, 2, 'p', 'w'}
	console := capture(func() {
		up.feed(connect)
		up.feed([]byte{0xC0, 0})                                    // PINGREQ
		down.feed([]byte{0x20, 2, 0, 5})                            // CONNACK refused: not authorized
		down.feed([]byte{0xD0, 0})                                  // PINGRESP
		down.feed([]byte{0x31, 7, 0, 3, 'a', '/', 'b', 'o', 'n'})   // PUBLISH, retained, from the broker
		down.feed([]byte{0x90, 4, 0, 1, 0, 0x80})                   // SUBACK: one granted, one refused
		down.feed([]byte{0x30, 7, 0, 3, 'a', '/', 'c', 0xff, 0x00}) // a binary payload
		up.feed([]byte{0xE0, 0})                                    // DISCONNECT
	})
	all := strings.Join(l.tail(100), "\n")
	for _, want := range []string{
		`relay.pkt c=7 dir=page>broker type=CONNECT client_id=abcdef keepalive=60 clean=true will=false user=true password_sent=true mqtt_level=4`,
		`relay.pkt c=7 dir=page>broker type=PINGREQ bytes=0`,
		`relay.pkt c=7 dir=broker>page type=CONNACK session_present=false code=5 result="refused: not authorized" level=error`,
		`relay.pkt c=7 dir=broker>page type=PINGRESP bytes=0`,
		`relay.pkt c=7 dir=broker>page type=PUBLISH topic=a/b qos=0 retain=true dup=false bytes=2 payload=on`,
		`relay.pkt c=7 dir=broker>page type=SUBACK id=1 codes="[0 128]" refused=true level=warn`,
		`topic=a/c qos=0 retain=false dup=false bytes=2 payload="hex:ff00"`,
		`relay.pkt c=7 dir=page>broker type=DISCONNECT`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, `"pw"`) || strings.Contains(all, "=pw") {
		t.Errorf("the password reached the log:\n%s", all)
	}
	if strings.Contains(console, "PINGREQ") || strings.Contains(console, "PINGRESP") || strings.Contains(console, "a/b") {
		t.Errorf("the console got what only the event log is for:\n%s", console)
	}
	if up.pkts != 3 || down.pkts != 5 || up.bytes != int64(len(connect))+2+2 {
		t.Errorf("counters: up %d packets %d bytes, down %d packets", up.pkts, up.bytes, down.pkts)
	}
}
