// MCP Bench for Windows: serves index.html on 127.0.0.1 and relays each Websocket from the page to a plain TCP MQTT
// broker, so the broker's normal port (1883) works with no Mosquitto and nothing to install.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

//go:embed index.html mqtt.min.js
var assets embed.FS

// swap stops the program when index.html and this file drift apart, instead of serving a page that cannot use the relay.
func swap(b []byte, old, new string) []byte {
	if !bytes.Contains(b, []byte(old)) {
		log.Fatalf("index.html no longer contains %q", old)
	}
	return bytes.Replace(b, []byte(old), []byte(new), 1)
}

func handler(port int) http.Handler {
	html, _ := assets.ReadFile("index.html")
	html = swap(html, "const BRIDGE=false;", "const BRIDGE=true;")
	html = swap(html, `src="https://cdn.jsdelivr.net/npm/mqtt@5/dist/mqtt.min.js"`, `src="/mqtt.min.js"`) // works without internet
	sum := sha256.Sum256(html)
	pageSHA = hex.EncodeToString(sum[:4])
	lib, _ := assets.ReadFile("mqtt.min.js")
	serve := func(typ string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", typ)
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(body)
		}
	}
	mux := http.NewServeMux()
	page := serve("text/html; charset=utf-8", html)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		evlog("sys.page_load", "remote", r.RemoteAddr, "ua", r.UserAgent())
		page(w, r)
	})
	mux.HandleFunc("GET /mqtt.min.js", serve("text/javascript", lib))
	mux.HandleFunc("GET /tcp/{host}/{port}", func(w http.ResponseWriter, r *http.Request) {
		relay(w, r, r.PathValue("host"), r.PathValue("port"))
	})
	logRoutes(mux, port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// a web page from another site must not reach the relay (DNS rebinding): only our own address is accepted
		if r.Host != fmt.Sprintf("127.0.0.1:%d", port) && r.Host != fmt.Sprintf("localhost:%d", port) {
			evlog("sys.http_denied", "level", "warn", "host", r.Host, "path", r.URL.Path, "remote", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

var connSeq int64 // number of the connection, to follow one through the event log

// relay copies bytes between the page's Websocket and a TCP connection to the broker.
// shortcut: plain TCP only (no TLS, so not port 8883), add tls.Dial when a broker needs it.
func relay(w http.ResponseWriter, r *http.Request, host, port string) {
	id := atomic.AddInt64(&connSeq, 1)
	to := net.JoinHostPort(host, port)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mqtt"}}) // refuses pages from other origins
	if err != nil {
		evlog("relay.ws_denied", "level", "warn", "c", id, "to", to, "origin", r.Header.Get("Origin"), "host", r.Host, "err", err)
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	evlog("relay.ws_open", "c", id, "to", to, "remote", r.RemoteAddr, "origin", r.Header.Get("Origin"))
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", to, 8*time.Second)
	if err != nil {
		log.Printf("%s:%s unreachable: %v", host, port, err)
		evlog("relay.dial_failed", "level", "error", "c", id, "to", to, "secs", time.Since(t0), "err", err, "net", netState())
		ws.Close(websocket.StatusInternalError, "cannot reach broker")
		return
	}
	defer conn.Close()
	log.Printf("connected to %s", conn.RemoteAddr())
	evlog("relay.dial", "c", id, "to", conn.RemoteAddr().String(), "local", conn.LocalAddr().String(), "secs", time.Since(t0))
	up := &pktLog{who: "-> " + conn.RemoteAddr().String(), want: 1, c: id, dir: "page>broker"}
	down := &pktLog{who: "<- " + conn.RemoteAddr().String(), want: 2, c: id, dir: "broker>page"}
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	start, once := time.Now(), sync.Once{}
	lastDown := start.UnixNano()        // when the broker last sent anything
	end := func(by string, err error) { // says who ended it and when: a broker or network drop looks different from the page closing
		once.Do(func() {
			log.Printf("connection to %s ended by %s after %s: %v", conn.RemoteAddr(), by, time.Since(start).Round(time.Second), err)
			evlog("relay.end", "c", id, "by", by, "secs", time.Since(start), "err", err, "up_bytes", atomic.LoadInt64(&up.bytes), "down_bytes", atomic.LoadInt64(&down.bytes),
				"up_pkts", atomic.LoadInt64(&up.pkts), "down_pkts", atomic.LoadInt64(&down.pkts), "local", conn.LocalAddr().String(), "net", netState())
		})
	}
	go func() { // every 30 s: what moved and how long the broker has been silent (an idle connection gets a ping answer about every 60 s)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		var lu, ld int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			u, d := atomic.LoadInt64(&up.bytes), atomic.LoadInt64(&down.bytes)
			silent := time.Since(time.Unix(0, atomic.LoadInt64(&lastDown)))
			kv := []any{"c", id, "up_bytes", u - lu, "down_bytes", d - ld, "broker_silent_secs", silent}
			if silent > 100*time.Second {
				kv = append(kv, "level", "warn")
			}
			evlog("relay.stats", kv...)
			lu, ld = u, d
		}
	}()
	go func() { // broker -> page
		defer stop()
		defer func() {
			if p := recover(); p != nil {
				evlog("sys.panic", "level", "error", "where", "relay broker to page", "c", id, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				end("a bug in this program", fmt.Errorf("panic: %v", p))
			}
		}()
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&lastDown, time.Now().UnixNano())
			}
			down.feed(buf[:n])
			if n > 0 {
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					end("the page", werr)
					return
				}
			}
			if err != nil {
				end("the broker or the network", err)
				return
			}
		}
	}()
	for { // page -> broker
		_, data, err := ws.Read(ctx)
		if err != nil {
			end("the page", err)
			return
		}
		up.feed(data)
		if _, err := conn.Write(data); err != nil {
			end("the broker or the network", err)
			return
		}
	}
}

func open(url string) {
	if runtime.GOOS != "windows" {
		evlog("sys.browser", "how", "xdg-open")
		exec.Command("xdg-open", url).Start()
		return
	}
	for _, dir := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData")} {
		if dir == "" {
			continue
		}
		for _, exe := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`} {
			p := filepath.Join(dir, exe)
			if _, err := os.Stat(p); err == nil && exec.Command(p, "--app="+url, "--window-size=1360,860").Start() == nil {
				evlog("sys.browser", "how", "app window", "path", p)
				return // its own window, without tabs or address bar
			}
		}
	}
	evlog("sys.browser", "level", "warn", "how", "default browser", "note", "Edge and Chrome were not found")
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() // default browser
}

// startLog opens the event log (next to the program, or the temp folder when that cannot be written) and records who and where this is.
func startLog(path string, max int64, port int, addr string) {
	if path == "" {
		exe, _ := os.Executable()
		path = filepath.Join(filepath.Dir(exe), "mqtt-panel.log")
	}
	l, err := openLog(path, max)
	if err != nil {
		fmt.Println("Nao consegui criar", path, "-", err)
		path = filepath.Join(os.TempDir(), "mqtt-panel.log")
		if l, err = openLog(path, max); err != nil {
			fmt.Println("Sem arquivo de registro:", err)
			return
		}
	}
	elog = l
	fmt.Println("Registro de tudo que acontece (para analisar):", path)
	info := progInfo(port)
	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := []any{"listen", addr}
	for _, k := range keys {
		kv = append(kv, k, info[k])
	}
	evlog("sys.start", kv...)
}

type evWriter struct{}

func (evWriter) Write(p []byte) (int, error) { // errors of the web server end up in the event log too
	evlog("sys.http_error", "level", "error", "msg", strings.TrimSpace(string(p)))
	return len(p), nil
}

// watch notes when this program did not run for a while (the PC went to sleep, or froze) and when the network of the PC changed.
func watch() {
	last, net0 := time.Now(), netState()
	for range time.Tick(5 * time.Second) {
		now := time.Now()
		if gap := now.Sub(last); gap > 15*time.Second {
			evlog("sys.gap", "level", "warn", "secs", gap, "note", "this program did not run for that long: the PC was asleep or frozen")
		}
		last = now
		if n := netState(); n != net0 {
			evlog("sys.net_change", "level", "warn", "from", net0, "to", n)
			net0 = n
		}
	}
}

func main() {
	port := flag.Int("port", 8765, "local port; the saved buttons belong to this address, so keep it fixed")
	noOpen := flag.Bool("no-open", false, "do not open the window")
	logPath := flag.String("log", "", "file where everything that happens is written (default: mqtt-panel.log next to the program)")
	logMax := flag.Int64("logmax", 8<<20, "size in bytes at which that file is moved to <file>.1")
	flag.Parse()
	log.SetFlags(log.Ltime)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	url := "http://" + addr + "/"
	ln, err := net.Listen("tcp", addr)
	if err != nil { // most likely already running: just show its window again
		fmt.Println("MCP Bench ja esta aberto em", url, "-", err)
		if !*noOpen {
			open(url)
		}
		return
	}
	h := handler(*port)
	startLog(*logPath, *logMax, *port, addr)
	defer func() {
		if p := recover(); p != nil {
			evlog("sys.panic", "level", "error", "where", "main", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			panic(p)
		}
	}()
	fmt.Println("MCP Bench rodando em", url, "- feche esta janela para sair")
	fmt.Println("O que o app manda para o broker (topico, QoS, payload) e o que ele responde aparece aqui.")
	if !*noOpen {
		go open(url)
	}
	go watch()
	srv := &http.Server{Handler: h, ErrorLog: log.New(io.MultiWriter(os.Stderr, evWriter{}), "", 0)}
	err = srv.Serve(ln)
	evlog("sys.exit", "level", "error", "err", err)
	log.Fatal(err)
}
