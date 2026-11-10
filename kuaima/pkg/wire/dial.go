package wire

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

// Dial 建立 TCP -> obfs -> 动态门 -> TLS 的连接。
func Dial(ctx context.Context, address string, obfsConfig obfs.Config, gateConfig gate.Config, username string, tlsConfig *tls.Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	conn := obfs.Wrap(raw, obfsConfig)
	if err := gate.Seal(conn, gateConfig, username); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: gate seal: %w", err)
	}
	if err := conn.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: gate flush: %w", err)
	}
	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: tls handshake: %w", err)
	}
	return tlsConn, nil
}

// Accept 接收 TCP -> obfs -> 动态门 -> TLS 的连接，返回用户名。
func Accept(raw net.Conn, obfsConfig obfs.Config, verifier *gate.Verifier, tlsConfig *tls.Config) (net.Conn, string, error) {
	conn := obfs.Wrap(raw, obfsConfig)
	username, err := verifier.Verify(conn)
	if err != nil {
		return nil, "", fmt.Errorf("kuaima: gate verify: %w", err)
	}
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		return nil, "", fmt.Errorf("kuaima: tls handshake: %w", err)
	}
	return tlsConn, username, nil
}
