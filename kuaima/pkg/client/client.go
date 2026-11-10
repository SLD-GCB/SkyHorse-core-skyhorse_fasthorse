// Package client 用纯 Go 实现快马的 TCP + obfs + 动态门 + TLS + H2 CONNECT。
package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/posener/h2conn"
	"golang.org/x/net/http2"

	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

// Config 客户端配置。
type Config struct {
	Server         string
	Port           uint16
	ServerName     string
	Username       string
	Password       string
	CertificatePEM string
	Insecure       bool
	Pin            string
	Obfs           obfs.Config
	Gate           gate.Config
	ReceiveWindow  uint64
	UploadBytesPerSecond int
}

// Client 维护一条 H2 连接，所有 CONNECT 流复用它。
type Client struct {
	cfg       Config
	tlsConfig *tls.Config
	transport *http2.Transport
}

// New 创建客户端。真正的连接在第一次 DialContext 时建立。
func New(ctx context.Context, cfg Config) (*Client, error) {
	tlsConfig, err := clientTLS(cfg)
	if err != nil {
		return nil, err
	}
	transport := &http2.Transport{
		TLSClientConfig: tlsConfig,
		ReadIdleTimeout: 10 * time.Second,
		PingTimeout:     5 * time.Second,
		DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
			raw, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if tcp, ok := raw.(*net.TCPConn); ok {
				_ = tcp.SetReadBuffer(4 << 20)
				_ = tcp.SetWriteBuffer(4 << 20)
			}
			conn := obfs.Wrap(raw, cfg.Obfs)
			if err := gate.Seal(conn, cfg.Gate, cfg.Username); err != nil {
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
			return newWriteLimitConn(tlsConn, cfg.UploadBytesPerSecond), nil
		},
	}
	return &Client{cfg: cfg, tlsConfig: tlsConfig, transport: transport}, nil
}

func clientTLS(cfg Config) (*tls.Config, error) {
	serverName := cfg.ServerName
	if serverName == "" {
		serverName = cfg.Server
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2"},
		ServerName: serverName,
	}
	if cfg.CertificatePEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CertificatePEM)) {
			return nil, fmt.Errorf("kuaima: bad certificate PEM")
		}
		tlsConfig.RootCAs = pool
	}
	if cfg.Insecure || cfg.Pin != "" {
		tlsConfig.InsecureSkipVerify = true
	}
	if cfg.Pin != "" {
		expected, err := decodePin(cfg.Pin)
		if err != nil {
			return nil, err
		}
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("kuaima: no certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			if !equal(sum[:], expected) {
				return fmt.Errorf("kuaima: certificate pin mismatch")
			}
			return nil
		}
	}
	if cfg.CertificatePEM == "" && !cfg.Insecure && cfg.Pin == "" {
		return nil, fmt.Errorf("kuaima: certificate or insecure is required")
	}
	return tlsConfig, nil
}

func decodePin(pin string) ([]byte, error) {
	pin = strings.TrimSpace(pin)
	if raw, err := hex.DecodeString(strings.ReplaceAll(pin, ":", "")); err == nil && len(raw) == 32 {
		return raw, nil
	}
	raw, err := base64.StdEncoding.DecodeString(pin)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("kuaima: bad pin")
	}
	return raw, nil
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// Start 纯 Go 实现无需启动步骤。
func (c *Client) Start() error { return nil }

// Close 关闭空闲连接。
func (c *Client) Close() error {
	c.transport.CloseIdleConnections()
	return nil
}

// DialContext 在共享 H2 连接上打开一条 CONNECT 流。
func (c *Client) DialContext(ctx context.Context, target string) (net.Conn, error) {
	authorization := "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(c.cfg.Username+":"+c.cfg.Password))
	h2 := &h2conn.Client{
		Method: "CONNECT",
		Header: http.Header{
			"-connect-authority":  {target},
			"Proxy-Authorization": {authorization},
		},
		Client: &http.Client{Transport: c.transport},
	}
	conn, resp, err := h2.Connect(ctx, "https://"+net.JoinHostPort(c.cfg.Server, fmt.Sprint(c.cfg.Port)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: connect failed: HTTP %d", resp.StatusCode)
	}
	return &streamConn{Conn: conn, local: dummyAddr("kuaima-local"), remote: dummyAddr("kuaima-remote")}, nil
}

type streamConn struct {
	*h2conn.Conn
	local  net.Addr
	remote net.Addr
}

func (s *streamConn) LocalAddr() net.Addr              { return s.local }
func (s *streamConn) RemoteAddr() net.Addr             { return s.remote }
func (s *streamConn) SetDeadline(time.Time) error      { return nil }
func (s *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (s *streamConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return "kuaima" }
func (d dummyAddr) String() string  { return string(d) }
