// Package boxapi is the public surface the sing-box adapter uses. It re-exports
// the protocol core that lives under internal/, so the kernel module can import
// it without crossing Go's internal package boundary.
package boxapi

import (
	"crypto/sha256"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	quic "github.com/apernet/quic-go"

	"tianma/internal/client"
	"tianma/internal/congestion"
	"tianma/internal/guard"
	"tianma/internal/obfs"
	"tianma/internal/policy"
	"tianma/internal/proto"
	"tianma/internal/server"
	"tianma/internal/wire"
)

// Wire constants.
const (
	URLHost       = proto.URLHost
	URLPath       = proto.URLPath
	HeaderAuth    = proto.HeaderAuth
	HeaderPadding = proto.HeaderPadding
	StatusAuthOK  = proto.StatusAuthOK
)

// Config is the client configuration.
type Config = client.Config

// H3Client carries the tunnel inside HTTP/3 CONNECT streams.
type H3Client = client.H3Client

// NewH3 builds an HTTP/3 tunnel client.
func NewH3(config Config) (*H3Client, error) {
	return client.NewH3(config)
}

// NewGateServer wraps a server packet conn with the dynamic gate.
func NewGateServer(conn net.PacketConn, secret []byte) net.PacketConn {
	return wire.NewGateServer(conn, secret)
}

// NewGateClient wraps a client packet conn with the dynamic gate.
func NewGateClient(conn net.PacketConn, secret []byte) net.PacketConn {
	return wire.NewGateClient(conn, secret)
}

// WrapObfs wraps a packet conn with Salamander obfuscation.
func WrapObfs(conn net.PacketConn, password string) (net.PacketConn, error) {
	if password == "" {
		return conn, nil
	}
	key := sha256.Sum256([]byte(password))
	return obfs.WrapPacketConnSalamander(conn, key[:])
}

// WrapObfsKeys wraps a packet conn with rotating Salamander keys. Server mode
// accepts all keys; client mode rotates on the given interval.
func WrapObfsKeys(conn net.PacketConn, passwords []string, rotate time.Duration, server bool) (net.PacketConn, error) {
	keys := make([][]byte, 0, len(passwords))
	for _, password := range passwords {
		if password != "" {
			keys = append(keys, []byte(password))
		}
	}
	if len(keys) == 0 {
		return conn, nil
	}
	if server {
		return obfs.WrapServerKeys(conn, keys)
	}
	return obfs.WrapClientKeys(conn, keys, rotate)
}

// ServerTLS loads the listener certificate.
func ServerTLS(certificatePath, keyPath string) (*tls.Config, error) {
	return wire.ServerTLS(certificatePath, keyPath)
}

// Token derives the exporter-bound auth token.
func Token(state *tls.ConnectionState, uuid, password string) ([]byte, error) {
	return wire.Token(state, uuid, password)
}

// QUICConfig returns the transport parameters.
func QUICConfig() *quic.Config {
	return wire.QUICConfig()
}

// DecodeToken parses the auth header value.
func DecodeToken(value string) (string, []byte, error) {
	return proto.DecodeToken(value)
}

// UseBrutal installs the adaptive fixed-rate sender on a connection.
func UseBrutal(conn *quic.Conn, bytesPerSecond uint64, disableLossCompensation bool) {
	congestion.UseBrutal(conn, bytesPerSecond, disableLossCompensation)
}

// HeaderCCRX is the bandwidth declaration header.
const HeaderCCRX = proto.HeaderCCRX

// UseBBR installs BBR with the loss guard.
func UseBBR(conn *quic.Conn, profile string) {
	congestion.UseBBR(conn, profile)
}

// PolicyConfig is the destination policy configuration.
type PolicyConfig = policy.Config

// Policy is a compiled destination policy.
type Policy = policy.Policy

// NewPolicy compiles a destination policy.
func NewPolicy(config PolicyConfig) (*Policy, error) {
	return policy.New(config)
}

// GuardConfig is the abuse boundary configuration.
type GuardConfig = guard.Config

// NewGuard compiles the abuse boundary configuration.
func NewGuard(config GuardConfig) *guard.Guard {
	return guard.New(config)
}

// Guard is the compiled abuse boundary.
type Guard = guard.Guard

// QuotaConfig is the per-user traffic and bandwidth allowance.
type QuotaConfig = guard.QuotaConfig

// Quota tracks per-user usage.
type Quota = guard.Quota

// NewQuota compiles the allowance.
func NewQuota(config QuotaConfig) *Quota {
	return guard.NewQuota(config)
}

// Memory is the global memory water level.
type Memory = guard.Memory

// NewMemory builds the water level in bytes.
func NewMemory(limit int64) *Memory {
	return guard.NewMemory(limit)
}

// MasqueradeConfig decides what an unauthenticated visitor sees.
type MasqueradeConfig = server.MasqueradeConfig

// NewMasquerade builds the handler for unauthenticated traffic.
func NewMasquerade(config MasqueradeConfig) (http.Handler, error) {
	return server.NewMasquerade(config)
}

// SelfSigned reports whether the certificate is self-signed.
func SelfSigned(path string) (bool, error) {
	return wire.SelfSigned(path)
}
