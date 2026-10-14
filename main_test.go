package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// start serves handler on a free port, like main does on 8765.
func start(t *testing.T) string {
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = handler(srv.Listener.Addr().(*net.TCPAddr).Port)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func fetch(t *testing.T, url, host string) (int, string) {
	req, _ := http.NewRequest("GET", url, nil)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPage(t *testing.T) {
	base := start(t)
	code, html := fetch(t, base+"/", "")
	if code != 200 || !strings.Contains(html, "const BRIDGE=true;") || !strings.Contains(html, `src="/mqtt.min.js"`) || strings.Contains(html, "cdn.jsdelivr.net") {
		t.Fatalf("page not patched for the relay: %d", code)
	}
	if code, js := fetch(t, base+"/mqtt.min.js", ""); code != 200 || !strings.Contains(js, "mqtt") {
		t.Fatalf("mqtt.min.js not served: %d", code)
	}
	if code, _ := fetch(t, base+"/other", ""); code != 404 {
		t.Fatalf("unknown path answered %d", code)
	}
	if code, _ := fetch(t, base+"/", "evil.example:80"); code != 403 {
		t.Fatalf("foreign Host header answered %d", code)
	}
}

func TestRelay(t *testing.T) {
	broker, err := net.Listen("tcp", "127.0.0.1:0") // stands in for the MQTT broker: echoes what it gets
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	go func() {
		if c, err := broker.Accept(); err == nil {
			io.Copy(c, c)
		}
	}()
	_, bport, _ := net.SplitHostPort(broker.Addr().String())
	ws := "ws" + strings.TrimPrefix(start(t), "http") + "/tcp/127.0.0.1/"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, ws+bport, &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Write(ctx, websocket.MessageBinary, []byte("ping"))
	if _, got, err := c.Read(ctx); err != nil || string(got) != "ping" {
		t.Fatalf("echo through the relay: %q %v", got, err)
	}

	if _, _, err := websocket.Dial(ctx, ws+bport, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}}); err == nil {
		t.Fatal("a page from another site was allowed to use the relay")
	}

	closed, _ := net.Listen("tcp", "127.0.0.1:0") // a port nobody listens on: the page must see the connection close
	_, dead, _ := net.SplitHostPort(closed.Addr().String())
	closed.Close()
	c, _, err = websocket.Dial(ctx, ws+dead, &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("relay stayed open although the broker is unreachable")
	}
}
