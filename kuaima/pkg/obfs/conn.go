package obfs

import (
	"io"
	"net"
	"sync"
)

// Conn 把 net.Conn 包成混淆流。
type Conn struct {
	net.Conn
	reader *Reader
	writer *Writer
	mu     sync.Mutex
}

// Wrap 用混淆层包装连接。
func Wrap(conn net.Conn, cfg Config) *Conn {
	return &Conn{
		Conn:   conn,
		reader: NewReader(conn, cfg),
		writer: NewWriter(conn, cfg),
	}
}

func (c *Conn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *Conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.writer.Write(p)
	if err != nil {
		return n, err
	}
	// TLS 握手记录很小，不主动 flush 会一直留在缓冲区里。
	return n, c.writer.Flush()
}

// Flush 把当前缓冲块写出。
func (c *Conn) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writer.Flush()
}

// CloseWrite 尽量关闭底层写方向。
func (c *Conn) CloseWrite() error {
	if err := c.Flush(); err != nil {
		return err
	}
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// Upstream 返回底层连接，便于替换。
func (c *Conn) Upstream() any { return c.Conn }

var _ io.ReadWriteCloser = (*Conn)(nil)
