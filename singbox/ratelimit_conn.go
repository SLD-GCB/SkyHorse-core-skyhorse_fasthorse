package skyhorsebox

import (
	"context"
	"net"
	"sync"

	"golang.org/x/time/rate"
)

// writeLimitConn caps the aggregate write rate of one transport. Both the
// FastHorse H2 client and server multiplex every stream over a single TCP
// connection, so limiting this conn throttles the whole session.
type writeLimitConn struct {
	net.Conn
	limiter *rate.Limiter
	mu      sync.Mutex
}

func newWriteLimitConn(conn net.Conn, bytesPerSecond int) net.Conn {
	if bytesPerSecond <= 0 {
		return conn
	}
	burst := bytesPerSecond / 10
	if burst < 64<<10 {
		burst = 64 << 10
	}
	if burst > 4<<20 {
		burst = 4 << 20
	}
	return &writeLimitConn{
		Conn:    conn,
		limiter: rate.NewLimiter(rate.Limit(bytesPerSecond), burst),
	}
}

func (c *writeLimitConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	written := 0
	for written < len(p) {
		chunk := len(p) - written
		if chunk > 64<<10 {
			chunk = 64 << 10
		}
		if err := c.limiter.WaitN(context.Background(), chunk); err != nil {
			return written, err
		}
		n, err := c.Conn.Write(p[written : written+chunk])
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
