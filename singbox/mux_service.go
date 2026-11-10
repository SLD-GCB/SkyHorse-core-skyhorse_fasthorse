package skyhorsebox

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-mux"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type streamNetConn struct {
	io.Reader
	io.Writer
	closer io.Closer
}

func (c *streamNetConn) Close() error                       { return c.closer.Close() }
func (c *streamNetConn) LocalAddr() net.Addr                { return dummyAddr("skyhorse-mux-local") }
func (c *streamNetConn) RemoteAddr() net.Addr               { return dummyAddr("skyhorse-mux-remote") }
func (c *streamNetConn) SetDeadline(t time.Time) error      { return nil }
func (c *streamNetConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *streamNetConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return "skyhorse-mux" }
func (a dummyAddr) String() string  { return string(a) }

// muxHandler routes streams opened by a sing-mux session directly through the
// kernel router. It replaces the old per-stream SOCKS5 CONNECT path.
type muxHandler struct {
	router  adapter.Router
	inbound adapter.InboundContext
}

func (h *muxHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	metadata := h.inbound
	metadata.Source = source
	metadata.Destination = destination
	h.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (h *muxHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	metadata := h.inbound
	metadata.Source = source
	metadata.Destination = destination
	h.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

func newMuxService(logger log.ContextLogger, router adapter.Router, inbound adapter.InboundContext) (*mux.Service, error) {
	return mux.NewService(mux.ServiceOptions{
		NewStreamContext: func(ctx context.Context, conn net.Conn) context.Context { return ctx },
		Logger:           logger,
		HandlerEx:        &muxHandler{router: router, inbound: inbound},
	})
}
