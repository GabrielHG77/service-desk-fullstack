package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// the console says who ended a connection, so a drop of the broker or the network can be told from the page closing
func TestRelayLogsWhoEndedIt(t *testing.T) {
	l := useLog(t, 1<<20)
	atomic.StoreInt64(&connSeq, 0) // the connections of this test are numbered 1 and 2
	broker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	hangUp := make(chan bool, 1)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := broker.Accept()
			if err != nil {
				return
			}
			if i == 0 { // first connection: the page says something and the broker hangs up
				c.Read(make([]byte, 1))
				c.Close()
			} else { // second: the broker keeps it open until the test is over
				<-hangUp
				c.Close()
			}
		}
	}()
	_, bport, _ := net.SplitHostPort(broker.Addr().String())
	ws := "ws" + strings.TrimPrefix(start(t), "http") + "/tcp/127.0.0.1/"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := capture(func() {
		c, _, err := websocket.Dial(ctx, ws+bport, &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
		if err != nil {
			t.Error(err)
			return
		}
		c.Write(ctx, websocket.MessageBinary, []byte("x"))
		if _, _, err := c.Read(ctx); err == nil {
			t.Error("page stayed open after the broker hung up")
		}
		time.Sleep(200 * time.Millisecond)
	})
	if !strings.Contains(out, "ended by the broker or the network after") {
		t.Errorf("a broker hang-up was not logged as such:\n%s", out)
	}
	all := strings.Join(l.tail(100), "\n")
	for _, want := range []string{" relay.ws_open c=1 to=127.0.0.1:", " relay.dial c=1 to=127.0.0.1:", ` relay.end c=1 by="the broker or the network" secs=`, " up_bytes=1 ", "local=127.0.0.1:"} {
		if !strings.Contains(all, want) {
			t.Errorf("the event log is missing %q:\n%s", want, all)
		}
	}

	out = capture(func() {
		c, _, err := websocket.Dial(ctx, ws+bport, &websocket.DialOptions{Subprotocols: []string{"mqtt"}})
		if err != nil {
			t.Error(err)
			return
		}
		time.Sleep(100 * time.Millisecond)
		c.Close(websocket.StatusNormalClosure, "bye")
		time.Sleep(300 * time.Millisecond)
	})
	hangUp <- true
	if !strings.Contains(out, "ended by the page after") {
		t.Errorf("the page closing was not logged as such:\n%s", out)
	}
	if all := strings.Join(l.tail(100), "\n"); !strings.Contains(all, ` relay.end c=2 by="the page" secs=`) {
		t.Errorf("the event log does not say the page ended connection 2:\n%s", all)
	}
}
