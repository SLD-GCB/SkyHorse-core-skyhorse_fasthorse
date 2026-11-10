// Package server 实现快马服务端：端口段监听 + obfs + 动态门 + TLS + H2。
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"skyhorse.local/kuaima/internal/config"
	"skyhorse.local/kuaima/internal/govern"
	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
	"skyhorse.local/kuaima/pkg/wire"
)

// Server 快马服务端。
type Server struct {
	cfg       config.ServerConfig
	tlsConfig *tls.Config

	obfsConfig atomic.Pointer[obfs.Config]
	verifier   atomic.Pointer[gate.Verifier]
	govern     atomic.Pointer[govern.Govern]
	h2         *wire.Server

	mu        sync.Mutex
	listeners []net.Listener
}

// New 创建服务端。
func New(cfg config.ServerConfig) (*Server, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertificatePath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("server: load certificate: %w", err)
	}
	if cfg.ServerName != "" {
		leaf := cert.Leaf
		if leaf == nil {
			leaf, err = x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				return nil, fmt.Errorf("server: parse certificate: %w", err)
			}
		}
		if err := leaf.VerifyHostname(cfg.ServerName); err != nil {
			return nil, fmt.Errorf("server: certificate does not match server_name %q: %w", cfg.ServerName, err)
		}
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	}
	obfsConfig := obfs.Config{
		Password:  cfg.ObfsPassword,
		Rotation:  config.ObfsRotation(cfg.ObfsRotationSeconds),
		Tolerance: config.ObfsTolerance(cfg.ObfsToleranceSeconds),
		PadTo:     cfg.ObfsPadTo,
		Keys:      []obfs.Key{{ID: 0, Password: cfg.ObfsPassword}},
	}
	for _, key := range cfg.ObfsKeys {
		if key.Password == "" || byte(key.ID) == 0 {
			continue
		}
		obfsConfig.Keys = append(obfsConfig.Keys, obfs.Key{ID: byte(key.ID), Password: key.Password})
	}
	server := &Server{
		cfg:       cfg,
		tlsConfig: tlsConfig,
		h2:        wire.NewServer(tlsConfig),
	}
	server.obfsConfig.Store(&obfsConfig)
	server.verifier.Store(gate.NewVerifier(gate.Config{Password: cfg.ObfsPassword, NodeID: cfg.NodeID}))
	server.govern.Store(govern.New(cfg))
	server.h2.Authorize = server.authorize
	server.h2.MaxStreams = uint32(cfg.Guard.MaxStreamsPerConn)
	server.h2.OnStream = func(ctx context.Context) (func(), error) {
		return server.govern.Load().AcquireStream(wire.UserFromContext(ctx))
	}
	return server, nil
}

// Reload 热加载用户、混淆密钥和配额；已有连接继续用完。
func (s *Server) Reload(cfg config.ServerConfig) {
	obfsConfig := obfs.Config{
		Password:  cfg.ObfsPassword,
		Rotation:  config.ObfsRotation(cfg.ObfsRotationSeconds),
		Tolerance: config.ObfsTolerance(cfg.ObfsToleranceSeconds),
		PadTo:     cfg.ObfsPadTo,
		Keys:      []obfs.Key{{ID: 0, Password: cfg.ObfsPassword}},
	}
	for _, key := range cfg.ObfsKeys {
		if key.Password == "" || byte(key.ID) == 0 {
			continue
		}
		obfsConfig.Keys = append(obfsConfig.Keys, obfs.Key{ID: byte(key.ID), Password: key.Password})
	}
	s.obfsConfig.Store(&obfsConfig)
	s.verifier.Store(gate.NewVerifier(gate.Config{Password: cfg.ObfsPassword, NodeID: cfg.NodeID}))
	s.govern.Store(govern.New(cfg))
	s.h2.MaxStreams = uint32(cfg.Guard.MaxStreamsPerConn)
}

// Run 监听端口段，阻塞直到 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	ports, err := config.Ports(s.cfg.PortRange)
	if err != nil {
		return err
	}
	for _, port := range ports {
		address := net.JoinHostPort(s.cfg.Listen, fmt.Sprintf("%d", port))
		ln, err := net.Listen("tcp", address)
		if err != nil {
			s.closeListeners()
			return fmt.Errorf("server: listen %s: %w", address, err)
		}
		s.mu.Lock()
		s.listeners = append(s.listeners, ln)
		s.mu.Unlock()
		go s.acceptLoop(ctx, ln)
	}
	<-ctx.Done()
	s.closeListeners()
	return ctx.Err()
}

func (s *Server) closeListeners() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ln := range s.listeners {
		_ = ln.Close()
	}
	s.listeners = nil
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		release, ok := s.govern.Load().AcquireHandshake()
		if !ok {
			_ = raw.Close()
			continue
		}
		go func() {
			defer release()
			s.handleConn(ctx, raw)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, raw net.Conn) {
	ip := remoteIP(raw)
	conn, user, err := wire.Accept(raw, *s.obfsConfig.Load(), s.verifier.Load(), s.tlsConfig)
	if err != nil {
		s.fail(raw)
		return
	}
	defer conn.Close()

	g := s.govern.Load()
	release, err := g.Acquire(ip, user)
	if err != nil {
		s.fail(conn)
		return
	}
	defer release()

	s.h2.ServeConnContext(wire.WithUser(ctx, user), conn)
}

// fail 统一失败行为：drop 让探测者看到超时，reset 看到 RST，close 看到 FIN。
func (s *Server) fail(conn net.Conn) {
	switch s.cfg.FailureMode {
	case "reset":
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	case "close":
		_ = conn.Close()
	default:
		delay := time.Duration(s.cfg.FailureDelayMS)*time.Millisecond + time.Duration(rand.Intn(1000))*time.Millisecond
		time.Sleep(delay)
		_ = conn.Close()
	}
}

func (s *Server) authorize(ctx context.Context, r *http.Request) error {
	gateUser := wire.UserFromContext(ctx)
	basicUser, err := s.govern.Load().VerifyBasic(r)
	if err != nil {
		return err
	}
	if basicUser != gateUser {
		return fmt.Errorf("server: gate user %q != basic user %q", gateUser, basicUser)
	}
	authority := r.Header.Get("-connect-authority")
	if authority == "" {
		authority = r.Host
	}
	if authority == "" {
		authority = r.URL.Host
	}
	return s.govern.Load().AllowTarget(authority)
}

func remoteIP(conn net.Conn) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	return host
}
