// MQTT Panel for Windows: serves index.html on 127.0.0.1 and relays each Websocket from the page to a plain TCP MQTT
// broker, so the broker's normal port (1883) works with no Mosquitto and nothing to install.
package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	lib, _ := assets.ReadFile("mqtt.min.js")
	serve := func(typ string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", typ)
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serve("text/html; charset=utf-8", html))
	mux.HandleFunc("GET /mqtt.min.js", serve("text/javascript", lib))
	mux.HandleFunc("GET /tcp/{host}/{port}", func(w http.ResponseWriter, r *http.Request) {
		relay(w, r, r.PathValue("host"), r.PathValue("port"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// a web page from another site must not reach the relay (DNS rebinding): only our own address is accepted
		if r.Host != fmt.Sprintf("127.0.0.1:%d", port) && r.Host != fmt.Sprintf("localhost:%d", port) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// relay copies bytes between the page's Websocket and a TCP connection to the broker.
// shortcut: plain TCP only (no TLS, so not port 8883), add tls.Dial when a broker needs it.
func relay(w http.ResponseWriter, r *http.Request, host, port string) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mqtt"}}) // refuses pages from other origins
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 8*time.Second)
	if err != nil {
		log.Printf("%s:%s unreachable: %v", host, port, err)
		ws.Close(websocket.StatusInternalError, "cannot reach broker")
		return
	}
	defer conn.Close()
	log.Printf("connected to %s", conn.RemoteAddr())
	up, down := &pktLog{who: "-> " + conn.RemoteAddr().String(), want: 1}, &pktLog{who: "<- " + conn.RemoteAddr().String(), want: 2}
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	go func() { // broker -> page
		defer stop()
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			down.feed(buf[:n])
			if n > 0 && ws.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for { // page -> broker
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		up.feed(data)
		if _, err := conn.Write(data); err != nil {
			return
		}
	}
}

func open(url string) {
	if runtime.GOOS != "windows" {
		exec.Command("xdg-open", url).Start()
		return
	}
	for _, dir := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData")} {
		if dir == "" {
			continue
		}
		for _, exe := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`} {
			p := filepath.Join(dir, exe)
			if _, err := os.Stat(p); err == nil && exec.Command(p, "--app="+url).Start() == nil {
				return // its own window, without tabs or address bar
			}
		}
	}
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() // default browser
}

func main() {
	port := flag.Int("port", 8765, "local port; the saved buttons belong to this address, so keep it fixed")
	noOpen := flag.Bool("no-open", false, "do not open the window")
	flag.Parse()
	log.SetFlags(log.Ltime)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	url := "http://" + addr + "/"
	ln, err := net.Listen("tcp", addr)
	if err != nil { // most likely already running: just show its window again
		fmt.Println("MQTT Panel ja esta aberto em", url, "-", err)
		if !*noOpen {
			open(url)
		}
		return
	}
	fmt.Println("MQTT Panel rodando em", url, "- feche esta janela para sair")
	fmt.Println("O que o app manda para o broker (topico, QoS, payload) e o que ele responde aparece aqui.")
	if !*noOpen {
		go open(url)
	}
	log.Fatal(http.Serve(ln, handler(*port)))
}
