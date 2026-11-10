package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"

	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

// muxMagic marks a dedicated TLS connection carrying a sing-mux session
// instead of HTTP/2.
const muxMagic = "FHMX"

// DialRaw opens a dedicated TLS connection for sing-mux. It bypasses H2
// entirely so the mux protocol gets a full-duplex TCP byte stream.
func (c *Client) DialRaw(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(c.cfg.Server, fmt.Sprint(c.cfg.Port))
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if tcp, ok := raw.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4 << 20)
		_ = tcp.SetWriteBuffer(4 << 20)
		_ = tcp.SetNoDelay(true)
	}
	conn := obfs.Wrap(raw, c.cfg.Obfs)
	if err := gate.Seal(conn, c.cfg.Gate, c.cfg.Username); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: gate seal: %w", err)
	}
	if err := conn.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: gate flush: %w", err)
	}
	tlsConfig := c.tlsConfig.Clone()
	tlsConfig.NextProtos = nil
	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: tls handshake: %w", err)
	}
	if _, err := io.WriteString(tlsConn, muxMagic); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("kuaima: mux preface: %w", err)
	}
	return tlsConn, nil
}
