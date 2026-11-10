package wire

import (
	"io"
	"net"
	"sync"
	"time"
)

type streamConn struct {
	reader  io.Reader
	writer  io.Writer
	closeFn func()
	once    sync.Once
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.writer.Write(p) }

func (c *streamConn) Close() error {
	c.once.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
	})
	return nil
}

// CloseWrite closes the request side only. mux transports use half-close to
// signal the end of their preface while keeping the response side readable.
func (c *streamConn) CloseWrite() error {
	if c.closeFn != nil {
		c.closeFn()
	}
	return nil
}

func (c *streamConn) LocalAddr() net.Addr              { return streamAddr("kuaima-local") }
func (c *streamConn) RemoteAddr() net.Addr             { return streamAddr("kuaima-remote") }
func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

type streamAddr string

func (a streamAddr) Network() string { return "kuaima" }
func (a streamAddr) String() string  { return string(a) }
