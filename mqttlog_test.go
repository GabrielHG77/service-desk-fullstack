package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// capture runs f and returns what the log printed.
func capture(f func()) string {
	var out bytes.Buffer
	log.SetOutput(&out)
	log.SetFlags(0)
	defer log.SetOutput(os.Stderr)
	f()
	return out.String()
}

func TestPktLog(t *testing.T) {
	connect := []byte{0x10, 26, 0, 4, 'M', 'Q', 'T', 'T', 4, 0xC2, 0, 60, 0, 6, 'a', 'b', 'c', 'd', 'e', 'f', 0, 2, 'g', 'b', 0, 2, 'p', 'w'}
	publish := append([]byte{0x30, 22, 0, 16}, "mc8/command/playtrue"...) // qos 0, no retain
	publishQ1 := []byte{0x33, 6, 0, 1, 't', 0, 7, 'x'}                    // qos 1, retain
	up := &pktLog{who: "->", want: 1}
	out := capture(func() {
		for _, b := range append(append(connect, publish...), publishQ1...) { // one byte at a time: packets may be split anywhere
			up.feed([]byte{b})
		}
	})
	for _, want := range []string{
		`CONNECT mqtt-level=4 clientId="abcdef" keepalive=60s user=true`,
		`PUBLISH "mc8/command/play" qos=0 retain=false payload="true" (4 bytes)`,
		`PUBLISH "t" qos=1 retain=true payload="x" (1 bytes)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pw") {
		t.Errorf("password was printed:\n%s", out)
	}

	down := &pktLog{who: "<-", want: 2}
	if out := capture(func() { down.feed([]byte{0x20, 2, 0, 5}) }); !strings.Contains(out, "CONNACK refused: not authorized") {
		t.Errorf("connack not decoded: %q", out)
	}

	junk := &pktLog{who: "<-", want: 2} // an HTTP answer is not MQTT: say nothing and do not break
	if out := capture(func() { junk.feed([]byte("HTTP/1.1 400 Bad Request\r\n\r\n")) }); out != "" || !junk.off {
		t.Errorf("non-MQTT stream was logged: %q", out)
	}
}
