package websocket

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type MessageType int

const (
	MessageText   MessageType = 1
	MessageBinary MessageType = 2
)

type StatusCode int

const (
	StatusNormalClosure StatusCode = 1000
	StatusInternalError StatusCode = 1011
)

type AcceptOptions struct{ InsecureSkipVerify bool }
type DialOptions struct{ HTTPHeader http.Header }

type Conn struct {
	net       net.Conn
	r         *bufio.Reader
	client    bool
	readLimit int64
	rmu, wmu  sync.Mutex
	closed    bool
}

func Accept(w http.ResponseWriter, r *http.Request, _ *AcceptOptions) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("websocket: missing upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, fmt.Errorf("websocket: missing key")
	}
	h, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("websocket: hijacking unavailable")
	}
	nc, rw, err := h.Hijack()
	if err != nil {
		return nil, err
	}
	accept := acceptKey(key)
	if _, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		nc.Close()
		return nil, err
	}
	if err = rw.Flush(); err != nil {
		nc.Close()
		return nil, err
	}
	return &Conn{net: nc, r: rw.Reader, client: false, readLimit: 1 << 20}, nil
}
func Dial(ctx context.Context, raw string, opt *DialOptions) (*Conn, *http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "ws" {
		return nil, nil, fmt.Errorf("websocket: only ws supported offline")
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	d := net.Dialer{}
	nc, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		nc.Close()
		return nil, nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	bw := bufio.NewWriter(nc)
	fmt.Fprintf(bw, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, u.Host, key)
	if opt != nil {
		for k, vs := range opt.HTTPHeader {
			for _, v := range vs {
				fmt.Fprintf(bw, "%s: %s\r\n", k, v)
			}
		}
	}
	fmt.Fprint(bw, "\r\n")
	if err = bw.Flush(); err != nil {
		nc.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(nc)
	req := &http.Request{Method: "GET"}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		nc.Close()
		return nil, resp, fmt.Errorf("websocket: handshake status %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		nc.Close()
		return nil, resp, fmt.Errorf("websocket: bad accept")
	}
	return &Conn{net: nc, r: br, client: true, readLimit: 1 << 20}, resp, nil
}
func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}
func (c *Conn) SetReadLimit(n int64) { c.readLimit = n }
func (c *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if err := setDeadline(ctx, c.net, true); err != nil {
			return 0, nil, err
		}
		typ, p, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch typ {
		case 1:
			return MessageText, p, nil
		case 2:
			return MessageBinary, p, nil
		case 8:
			return 0, nil, io.EOF
		case 9:
			_ = c.writeFrame(10, p)
			continue
		case 10:
			continue
		default:
			return 0, nil, fmt.Errorf("websocket: unsupported opcode %d", typ)
		}
	}
}
func (c *Conn) Write(ctx context.Context, t MessageType, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := setDeadline(ctx, c.net, false); err != nil {
		return err
	}
	return c.writeFrame(byte(t), p)
}
func (c *Conn) Close(code StatusCode, reason string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p, uint16(code))
	copy(p[2:], reason)
	_ = c.writeFrame(8, p)
	return c.net.Close()
}
func setDeadline(ctx context.Context, nc net.Conn, read bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var t time.Time
	if dl, ok := ctx.Deadline(); ok {
		t = dl
	}
	if read {
		return nc.SetReadDeadline(t)
	}
	return nc.SetWriteDeadline(t)
}
func (c *Conn) readFrame() (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(c.r, h); err != nil {
		return 0, nil, err
	}
	if h[0]&0x80 == 0 {
		return 0, nil, errors.New("websocket: fragmented frames unsupported")
	}
	op := h[0] & 0xf
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		if _, e := io.ReadFull(c.r, b[:]); e != nil {
			return 0, nil, e
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, e := io.ReadFull(c.r, b[:]); e != nil {
			return 0, nil, e
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if c.readLimit > 0 && int64(n) > c.readLimit {
		return 0, nil, fmt.Errorf("websocket: message too large")
	}
	var mask [4]byte
	if masked {
		if _, e := io.ReadFull(c.r, mask[:]); e != nil {
			return 0, nil, e
		}
	}
	p := make([]byte, int(n))
	if _, e := io.ReadFull(c.r, p); e != nil {
		return 0, nil, e
	}
	if masked {
		for i := range p {
			p[i] ^= mask[i&3]
		}
	}
	return op, p, nil
}
func (c *Conn) writeFrame(op byte, p []byte) error {
	if c.closed && op != 8 {
		return io.ErrClosedPipe
	}
	var hdr [14]byte
	hdr[0] = 0x80 | op
	mask := c.client
	pos := 2
	n := len(p)
	if n < 126 {
		hdr[1] = byte(n)
	} else if n <= 65535 {
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		pos = 4
	} else {
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		pos = 10
	}
	if mask {
		hdr[1] |= 0x80
		var m [4]byte
		if _, e := rand.Read(m[:]); e != nil {
			return e
		}
		copy(hdr[pos:pos+4], m[:])
		pos += 4
		cp := append([]byte(nil), p...)
		for i := range cp {
			cp[i] ^= m[i&3]
		}
		p = cp
	}
	if _, e := c.net.Write(hdr[:pos]); e != nil {
		return e
	}
	if len(p) > 0 {
		_, e := c.net.Write(p)
		return e
	}
	return nil
}
