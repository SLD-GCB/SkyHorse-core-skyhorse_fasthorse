package obfs

import (
	"crypto/sha256"
	"errors"
	"net"
	"sync"
	"time"
)

// Multi-key Salamander wrapper. Each packet is prefixed with a one-byte key
// index so the server can accept the current and previous generation without
// guessing (Salamander itself has no authentication tag).
const rotateHeaderSize = 1

type rotatePacketConn struct {
	conn    net.PacketConn
	obfs    []obfuscator
	server  bool
	rotate  time.Duration
	started time.Time

	mu      sync.Mutex
	addrKey map[string]byte

	readBuf    []byte
	readMutex  sync.Mutex
	writeBuf   []byte
	writeMutex sync.Mutex
}

// WrapClientKeys wraps a client packet conn with rotating Salamander keys.
func WrapClientKeys(conn net.PacketConn, keys [][]byte, rotate time.Duration) (net.PacketConn, error) {
	obfsList, err := makeObfsList(keys)
	if err != nil {
		return nil, err
	}
	return &rotatePacketConn{
		conn: conn, obfs: obfsList, rotate: rotate, started: time.Now(),
		addrKey: make(map[string]byte), readBuf: make([]byte, udpBufferSize), writeBuf: make([]byte, udpBufferSize),
	}, nil
}

// WrapServerKeys wraps a server packet conn and accepts every key in keys.
func WrapServerKeys(conn net.PacketConn, keys [][]byte) (net.PacketConn, error) {
	obfsList, err := makeObfsList(keys)
	if err != nil {
		return nil, err
	}
	return &rotatePacketConn{
		conn: conn, obfs: obfsList, server: true,
		addrKey: make(map[string]byte), readBuf: make([]byte, udpBufferSize), writeBuf: make([]byte, udpBufferSize),
	}, nil
}

func makeObfsList(keys [][]byte) ([]obfuscator, error) {
	if len(keys) == 0 {
		return nil, errors.New("obfs: at least one key is required")
	}
	out := make([]obfuscator, 0, len(keys))
	for _, key := range keys {
		sum := sha256.Sum256(key)
		ob, err := newSalamanderObfuscator(sum[:])
		if err != nil {
			return nil, err
		}
		out = append(out, ob)
	}
	return out, nil
}

func (c *rotatePacketConn) keyIndex() byte {
	if c.server || c.rotate <= 0 || len(c.obfs) < 2 {
		return 0
	}
	step := int64(time.Since(c.started) / c.rotate)
	return byte(step % int64(len(c.obfs)))
}

func (c *rotatePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.readMutex.Lock()
	defer c.readMutex.Unlock()
	for {
		n, addr, err := c.conn.ReadFrom(c.readBuf)
		if err != nil {
			return 0, addr, err
		}
		if n <= rotateHeaderSize {
			continue
		}
		index := c.readBuf[0]
		if int(index) >= len(c.obfs) {
			continue
		}
		out := c.obfs[index].Deobfuscate(c.readBuf[rotateHeaderSize:n], p)
		if out <= 0 {
			continue
		}
		if c.server {
			c.mu.Lock()
			c.addrKey[addr.String()] = index
			c.mu.Unlock()
		}
		return out, addr, nil
	}
}

func (c *rotatePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	index := c.keyIndex()
	if c.server {
		c.mu.Lock()
		if saved, ok := c.addrKey[addr.String()]; ok {
			index = saved
		}
		c.mu.Unlock()
	}
	if int(index) >= len(c.obfs) {
		index = 0
	}
	out := c.obfs[index].Obfuscate(p, c.writeBuf[1:])
	if out <= 0 {
		return 0, errors.New("obfs: packet too large")
	}
	c.writeBuf[0] = index
	if _, err := c.conn.WriteTo(c.writeBuf[:out+1], addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *rotatePacketConn) Close() error                       { return c.conn.Close() }
func (c *rotatePacketConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *rotatePacketConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *rotatePacketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *rotatePacketConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
