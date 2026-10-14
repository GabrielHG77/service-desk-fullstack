package main

import (
	"fmt"
	"log"
)

// pktLog prints the MQTT packets that cross the relay, so "the command does nothing" can be checked against what
// really went to the broker. Passwords are never printed.
type pktLog struct {
	who  string // "-> 192.168.0.50:1883" (page to broker) or "<- ..." (broker to page)
	want byte   // type of the first packet of this stream: 1 CONNECT from the page, 2 CONNACK from the broker
	buf  []byte
	seen bool
	off  bool // not MQTT, or too large: stop looking at this stream
}

func u16(p []byte, o int) int { return int(p[o])<<8 | int(p[o+1]) }

func (l *pktLog) feed(b []byte) {
	if l.off {
		return
	}
	l.buf = append(l.buf, b...)
	for len(l.buf) > 1 {
		if !l.seen {
			if l.buf[0]>>4 != l.want {
				l.off, l.buf = true, nil
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
				l.off, l.buf = true, nil
				return
			}
		}
		if n > 1<<20 {
			l.off, l.buf = true, nil
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
	defer func() { recover() }() // a malformed packet must never stop the relay
	flags := h & 15
	switch h >> 4 {
	case 1: // CONNECT
		o := 2 + u16(p, 0) // after the protocol name
		level, connectFlags, keepalive := p[o], p[o+1], u16(p, o+2)
		o += 4
		id := string(p[o+2 : o+2+u16(p, o)])
		log.Printf("%s CONNECT mqtt-level=%d clientId=%+q keepalive=%ds user=%v", l.who, level, id, keepalive, connectFlags&0x80 != 0)
	case 2: // CONNACK
		codes := []string{"accepted", "refused: protocol version", "refused: client id", "refused: broker unavailable", "refused: bad user or password", "refused: not authorized"}
		s := fmt.Sprint("code ", p[1])
		if int(p[1]) < len(codes) {
			s = codes[p[1]]
		}
		log.Printf("%s CONNACK %s", l.who, s)
	case 3: // PUBLISH
		qos := int(flags >> 1 & 3)
		o := 2 + u16(p, 0)
		topic := string(p[2:o])
		if qos > 0 {
			o += 2 // packet id
		}
		payload := p[o:]
		more := ""
		if len(payload) > 80 {
			payload, more = payload[:80], "..."
		}
		log.Printf("%s PUBLISH %+q qos=%d retain=%v payload=%+q%s (%d bytes)", l.who, topic, qos, flags&1 == 1, payload, more, len(p[o:]))
	case 4:
		log.Printf("%s PUBACK", l.who)
	case 14:
		log.Printf("%s DISCONNECT", l.who)
	}
}
