package skyhorsebox

import (
	"bufio"
	"net"
	"time"
)

// bufferedConn preserves bytes already peeked from the TLS stream before
// handing it to the H2 server.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedConn) CloseWrite() error {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return nil
}
func (c *bufferedConn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(t) }
func (c *bufferedConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *bufferedConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }
