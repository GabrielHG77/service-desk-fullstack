package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
)

// pktLog prints the MQTT packets that cross the relay, so "the command does nothing" can be checked against what
// really went to the broker. Passwords are never printed. The console gets what matters while testing; the event log
// file gets every packet, including the keepalive pings and everything the broker delivers.
type pktLog struct {
	who         string // "-> 192.168.0.50:1883" (page to broker) or "<- ..." (broker to page), for the console
	want        byte   // type of the first packet of this stream: 1 CONNECT from the page, 2 CONNACK from the broker
	buf         []byte
	seen        bool
	off         bool   // not MQTT, or too large: stop looking at this stream
	c           int64  // connection number in the event log
	dir         string // "page>broker" or "broker>page" in the event log
	bytes, pkts int64  // seen so far (atomic): the relay reports them when the connection ends
}

var pktNames = [...]string{1: "CONNECT", 2: "CONNACK", 3: "PUBLISH", 4: "PUBACK", 5: "PUBREC", 6: "PUBREL", 7: "PUBCOMP", 8: "SUBSCRIBE",
	9: "SUBACK", 10: "UNSUBSCRIBE", 11: "UNSUBACK", 12: "PINGREQ", 13: "PINGRESP", 14: "DISCONNECT"}

func u16(p []byte, o int) int { return int(p[o])<<8 | int(p[o+1]) }

func (l *pktLog) ev(typ string, kv ...any) {
	evlog("relay.pkt", append([]any{"c", l.c, "dir", l.dir, "type", typ}, kv...)...)
}

func (l *pktLog) stop(why string) {
	l.off, l.buf = true, nil
	evlog("relay.pkt_off", "level", "warn", "c", l.c, "dir", l.dir, "why", why)
}

func (l *pktLog) feed(b []byte) {
	atomic.AddInt64(&l.bytes, int64(len(b)))
	if l.off {
		return
	}
	l.buf = append(l.buf, b...)
	for len(l.buf) > 1 {
		if !l.seen {
			if l.buf[0]>>4 != l.want {
				l.stop(fmt.Sprintf("the first byte 0x%02x is not the packet an MQTT stream starts with (%d)", l.buf[0], l.want))
				return
			}
			l.seen = true
		}
		n, i := 0, 1
		for mul := 1; ; mul *= 128 { // remaining length: 1 to 4 bytes, 7 bits each
			if i >= len(l.buf) {
				return
			}
			n += int(l.buf[i]&127) * mul
			i++
			if l.buf[i-1]&128 == 0 {
				break
			}
			if i > 4 {
				l.stop("the remaining length is longer than 4 bytes")
				return
			}
		}
		if n > 1<<20 {
			l.stop("a packet larger than 1 MB")
			return
		}
		if len(l.buf) < i+n {
			return
		}
		l.show(l.buf[0], l.buf[i:i+n])
		l.buf = l.buf[i+n:]
	}
}

func (l *pktLog) show(h byte, p []byte) {
	atomic.AddInt64(&l.pkts, 1)
	defer func() { // a malformed packet must never stop the relay
		if recover() != nil {
			evlog("relay.pkt_malformed", "level", "warn", "c", l.c, "dir", l.dir, "type", int(h>>4), "bytes", len(p))
		}
	}()
	flags := h & 15
	switch h >> 4 {
	case 1: // CONNECT
		o := 2 + u16(p, 0) // after the protocol name
		level, connectFlags, keepalive := p[o], p[o+1], u16(p, o+2)
		o += 4
		id := string(p[o+2 : o+2+u16(p, o)])
		l.ev("CONNECT", "client_id", id, "keepalive", keepalive, "clean", connectFlags&2 != 0, "will", connectFlags&4 != 0,
			"user", connectFlags&0x80 != 0, "password_sent", connectFlags&0x40 != 0, "mqtt_level", level)
		log.Printf("%s CONNECT mqtt-level=%d clientId=%+q keepalive=%ds user=%v", l.who, level, id, keepalive, connectFlags&0x80 != 0)
	case 2: // CONNACK
		codes := []string{"accepted", "refused: protocol version", "refused: client id", "refused: broker unavailable", "refused: bad user or password", "refused: not authorized"}
		s := fmt.Sprint("code ", p[1])
		if int(p[1]) < len(codes) {
			s = codes[p[1]]
		}
		kv := []any{"session_present", p[0]&1 == 1, "code", p[1], "result", s}
		if p[1] != 0 {
			kv = append(kv, "level", "error")
		}
		l.ev("CONNACK", kv...)
		log.Printf("%s CONNACK %s", l.who, s)
	case 3: // PUBLISH
		qos := int(flags >> 1 & 3)
		o := 2 + u16(p, 0)
		topic := string(p[2:o])
		if qos > 0 {
			o += 2 // packet id
		}
		payload := p[o:]
		l.ev("PUBLISH", "topic", topic, "qos", qos, "retain", flags&1 == 1, "dup", flags&8 != 0, "bytes", len(payload), "payload", payload)
		if l.want == 2 {
			return // what the broker delivers to the page is shown in the app's Status tab
		}
		more := ""
		if len(payload) > 80 {
			payload, more = payload[:80], "..."
		}
		log.Printf("%s PUBLISH %+q qos=%d retain=%v payload=%+q%s (%d bytes)", l.who, topic, qos, flags&1 == 1, payload, more, len(p[o:]))
	case 4:
		l.ev("PUBACK", "id", u16(p, 0))
		log.Printf("%s PUBACK", l.who)
	case 8: // SUBSCRIBE: packet id, then topic filter and requested qos, repeated
		s := ""
		for o := 2; o+2 < len(p); {
			n := u16(p, o)
			s += fmt.Sprintf(" %+q(qos %d)", string(p[o+2:o+2+n]), p[o+2+n])
			o += 3 + n
		}
		l.ev("SUBSCRIBE", "id", u16(p, 0), "filters", strings.TrimSpace(s))
		log.Printf("%s SUBSCRIBE%s", l.who, s)
	case 9: // SUBACK: 0 to 2 = granted, 128 = refused
		refused := ""
		kv := []any{"id", u16(p, 0), "codes", fmt.Sprint(p[2:])}
		if bytes.IndexByte(p[2:], 0x80) >= 0 {
			refused = " (128 = the broker refused)"
			kv = append(kv, "refused", true, "level", "warn")
		}
		l.ev("SUBACK", kv...)
		log.Printf("%s SUBACK %v%s", l.who, p[2:], refused)
	case 14:
		l.ev("DISCONNECT")
		log.Printf("%s DISCONNECT", l.who)
	default: // PUBREC, PUBREL, PUBCOMP, UNSUBSCRIBE, UNSUBACK and the keepalive PINGREQ and PINGRESP: only in the event log
		name := fmt.Sprint("type_", h>>4)
		if t := int(h >> 4); t < len(pktNames) && pktNames[t] != "" {
			name = pktNames[t]
		}
		l.ev(name, "bytes", len(p))
	}
}
