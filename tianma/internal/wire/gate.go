package wire

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync"
	"time"
)

const (
	gateMagic = "TMG1"
	gateSize  = 4 + 8 + 16 + 16
	gateTTL   = 2 * time.Minute
)

var gateLabel = []byte("tianma-h3-gate-v1")

func gateKey(secret []byte) []byte {
	sum := sha256.Sum256(append(append([]byte(nil), gateLabel...), secret...))
	return sum[:]
}

func gateSeal(secret []byte) []byte {
	record := make([]byte, gateSize)
	copy(record[:4], gateMagic)
	binary.BigEndian.PutUint64(record[4:12], uint64(time.Now().Unix()))
	_, _ = rand.Read(record[12:28])
	mac := hmac.New(sha256.New, gateKey(secret))
	mac.Write(record[:28])
	copy(record[28:], mac.Sum(nil)[:16])
	return record
}

func gateVerify(record, secret []byte) bool {
	if len(record) != gateSize || string(record[:4]) != gateMagic {
		return false
	}
	ts := int64(binary.BigEndian.Uint64(record[4:12]))
	if delta := time.Since(time.Unix(ts, 0)); delta < -time.Minute || delta > gateTTL {
		return false
	}
	mac := hmac.New(sha256.New, gateKey(secret))
	mac.Write(record[:28])
	return hmac.Equal(mac.Sum(nil)[:16], record[28:])
}

// GateServer drops every packet until the peer sends a valid gate record.
type GateServer struct {
	net.PacketConn
	secret []byte

	mu      sync.Mutex
	allowed map[string]time.Time
	replay  map[string]time.Time
}

// NewGateServer wraps a server packet conn.
func NewGateServer(conn net.PacketConn, secret []byte) *GateServer {
	return &GateServer{
		PacketConn: conn,
		secret:     append([]byte(nil), secret...),
		allowed:    make(map[string]time.Time),
		replay:     make(map[string]time.Time),
	}
}

func (c *GateServer) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil {
			return n, addr, err
		}
		if n == gateSize && string(p[:4]) == gateMagic {
			if c.accept(p[:n], addr, time.Now()) {
				c.allow(addr)
			}
			continue
		}
		if c.isAllowed(addr, time.Now()) {
			return n, addr, nil
		}
	}
}

func (c *GateServer) accept(record []byte, addr net.Addr, now time.Time) bool {
	if !gateVerify(record, c.secret) {
		return false
	}
	nonce := string(record[12:28])
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, expiry := range c.replay {
		if now.After(expiry) {
			delete(c.replay, key)
		}
	}
	if _, seen := c.replay[nonce]; seen {
		return false
	}
	c.replay[nonce] = now.Add(gateTTL)
	return true
}

func (c *GateServer) allow(addr net.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowed[addr.String()] = time.Now().Add(gateTTL)
}

func (c *GateServer) isAllowed(addr net.Addr, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry, ok := c.allowed[addr.String()]
	if !ok || now.After(expiry) {
		delete(c.allowed, addr.String())
		return false
	}
	return true
}

// GateClient sends one gate record before the first packet.
type GateClient struct {
	net.PacketConn
	secret []byte
	once   sync.Once
}

// NewGateClient wraps a client packet conn.
func NewGateClient(conn net.PacketConn, secret []byte) *GateClient {
	return &GateClient{PacketConn: conn, secret: append([]byte(nil), secret...)}
}

func (c *GateClient) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.once.Do(func() {
		_, _ = c.PacketConn.WriteTo(gateSeal(c.secret), addr)
	})
	return c.PacketConn.WriteTo(p, addr)
}
