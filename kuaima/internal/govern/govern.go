// Package govern 提供快马最小治理：用户校验、目的地策略、每 IP 连接数。
package govern

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"skyhorse.local/kuaima/internal/config"
)

// Govern 服务端治理状态。
type Govern struct {
	users   map[string]string
	policy  config.Policy
	maxConn int

	maxUserConns   int
	maxUserStreams int
	handshakes     chan struct{}

	mu          sync.Mutex
	conns       map[string]int
	userConns   map[string]int
	userStreams map[string]int
}

// New 创建治理对象。
func New(cfg config.ServerConfig) *Govern {
	users := make(map[string]string, len(cfg.Users))
	for _, user := range cfg.Users {
		if user.Name == "" || user.Password == "" {
			continue
		}
		users[user.Name] = user.Password
	}
	return &Govern{
		users:          users,
		policy:         cfg.Policy,
		maxConn:        cfg.Guard.MaxConnectionsPerIP,
		maxUserConns:   cfg.Guard.MaxConnectionsPerUser,
		maxUserStreams: cfg.Guard.MaxStreamsPerUser,
		handshakes:     make(chan struct{}, cfg.Guard.MaxHandshakes),
		conns:          make(map[string]int),
		userConns:      make(map[string]int),
		userStreams:    make(map[string]int),
	}
}

// VerifyBasic 校验 Proxy-Authorization 的 Basic 凭据，返回用户名。
func (g *Govern) VerifyBasic(r *http.Request) (string, error) {
	value := strings.TrimSpace(r.Header.Get("Proxy-Authorization"))
	if value == "" {
		return "", fmt.Errorf("govern: missing proxy authorization")
	}
	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", fmt.Errorf("govern: not basic auth")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", fmt.Errorf("govern: bad basic auth")
	}
	name, password, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", fmt.Errorf("govern: bad basic auth")
	}
	expected, exists := g.users[name]
	if !exists || expected != password {
		return "", fmt.Errorf("govern: bad credentials")
	}
	return name, nil
}

// Acquire 获取一条连接配额，同时限制 IP 和用户。
func (g *Govern) Acquire(ip, user string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conns[ip] >= g.maxConn {
		return nil, fmt.Errorf("govern: too many connections from %s", ip)
	}
	if g.maxUserConns > 0 && g.userConns[user] >= g.maxUserConns {
		return nil, fmt.Errorf("govern: too many connections for user %s", user)
	}
	g.conns[ip]++
	g.userConns[user]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.conns[ip] <= 1 {
			delete(g.conns, ip)
		} else {
			g.conns[ip]--
		}
		if g.userConns[user] <= 1 {
			delete(g.userConns, user)
		} else {
			g.userConns[user]--
		}
	}, nil
}

// AcquireStream 获取一条流配额。
func (g *Govern) AcquireStream(user string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maxUserStreams > 0 && g.userStreams[user] >= g.maxUserStreams {
		return nil, fmt.Errorf("govern: too many streams for user %s", user)
	}
	g.userStreams[user]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.userStreams[user] <= 1 {
			delete(g.userStreams, user)
		} else {
			g.userStreams[user]--
		}
	}, nil
}

// AcquireHandshake 获取一个握手并发名额，非阻塞。
func (g *Govern) AcquireHandshake() (func(), bool) {
	select {
	case g.handshakes <- struct{}{}:
		return func() { <-g.handshakes }, true
	default:
		return nil, false
	}
}

// AllowTarget 检查目标是否符合策略。
func (g *Govern) AllowTarget(authority string) error {
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		return fmt.Errorf("govern: bad target %q", authority)
	}
	if len(g.policy.AllowedPorts) > 0 {
		allowed := false
		for _, port := range g.policy.AllowedPorts {
			if portText == fmt.Sprintf("%d", port) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("govern: port %s not allowed", portText)
		}
	}
	ips, err := resolve(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if ip.IsLoopback() && !g.policy.AllowLoopback {
			return fmt.Errorf("govern: loopback target denied")
		}
		if isPrivate(ip) && !g.policy.AllowPrivate {
			return fmt.Errorf("govern: private target denied")
		}
	}
	return nil
}

func resolve(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("govern: resolve %s: %w", host, err)
	}
	return ips, nil
}

func isPrivate(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// 100.64.0.0/10 CGNAT
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}
