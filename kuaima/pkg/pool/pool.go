// Package pool 管理快马客户端连接池：多入口、多端口、按吞吐自动扩缩。
package pool

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"skyhorse.local/kuaima/internal/config"
	"skyhorse.local/kuaima/pkg/client"
	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

type entryState struct {
	entry config.Entry
	ports []int
	next  int
}

type connection struct {
	client   *client.Client
	entry    string
	port     int
	inflight atomic.Int64
	healthy  atomic.Bool
	draining atomic.Bool
	bytes    atomic.Uint64
	rate     atomic.Uint64 // bytes/s，EWMA

	lastBytes  uint64
	lastFailed time.Time
	rotateAt   time.Time
	peakRate   atomic.Uint64
	lowTicks   int
}

// Pool 客户端连接池。
type Pool struct {
	cfg     config.ClientConfig
	certPEM string
	obfs    obfs.Config
	gate    gate.Config
	entries []entryState

	mu       sync.Mutex
	conns    []*connection
	target   float64 // bytes/s，0 表示不自动扩缩
	lastGrow time.Time
	lastDrop time.Time
	stop     chan struct{}
	wg       sync.WaitGroup
}

// New 按配置创建连接池并启动吞吐采样。
func New(ctx context.Context, cfg config.ClientConfig) (*Pool, error) {
	certPEM, err := os.ReadFile(cfg.CertificatePath)
	if err != nil {
		return nil, fmt.Errorf("pool: read certificate: %w", err)
	}
	pool := &Pool{
		cfg:     cfg,
		certPEM: string(certPEM),
		obfs: obfs.Config{
			Password:  cfg.ObfsPassword,
			KeyID:     byte(cfg.ObfsKeyID),
			Rotation:  config.ObfsRotation(cfg.ObfsRotationSeconds),
			Tolerance: config.ObfsTolerance(cfg.ObfsToleranceSeconds),
			PadTo:     cfg.ObfsPadTo,
		},
		gate:   gate.Config{Password: cfg.ObfsPassword},
		target: cfg.TargetMbps * 1_000_000 / 8,
		stop:   make(chan struct{}),
	}
	for _, entry := range cfg.Entries {
		ports, err := config.Ports(entry.PortRange)
		if err != nil {
			return nil, err
		}
		pool.entries = append(pool.entries, entryState{entry: entry, ports: ports})
	}
	for index := range pool.entries {
		for count := 0; count < cfg.Connections; count++ {
			if err := pool.addLocked(index); err != nil {
				pool.Close()
				return nil, err
			}
		}
	}
	pool.wg.Add(1)
	go pool.sampleLoop(ctx)
	return pool, nil
}

func (p *Pool) buildLocked(entryIndex int) (*connection, error) {
	state := &p.entries[entryIndex]
	port := state.ports[state.next%len(state.ports)]
	state.next++
	gateConfig := p.gate
	gateConfig.NodeID = state.entry.NodeID
	if gateConfig.NodeID == "" {
		gateConfig.NodeID = p.cfg.NodeID
	}
	cli, err := client.New(context.Background(), client.Config{
		Server:         state.entry.Server,
		Port:           uint16(port),
		ServerName:     p.cfg.ServerName,
		Username:       p.cfg.Username,
		Password:       p.cfg.Password,
		CertificatePEM: p.certPEM,
		Obfs:           p.obfs,
		Gate:           gateConfig,
		ReceiveWindow:  uint64(p.cfg.ReceiveWindowMB) << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("pool: create client %s:%d: %w", state.entry.Server, port, err)
	}
	if err := cli.Start(); err != nil {
		return nil, fmt.Errorf("pool: start client %s:%d: %w", state.entry.Server, port, err)
	}
	conn := &connection{client: cli, entry: state.entry.Server, port: port}
	conn.healthy.Store(true)
	if p.cfg.PortRotateSeconds > 0 {
		// 加 10% 抖动，避免所有连接同一秒切换。
		factor := 0.9 + rand.Float64()*0.2
		rotate := time.Duration(float64(p.cfg.PortRotateSeconds)*factor) * time.Second
		conn.rotateAt = time.Now().Add(rotate)
	}
	return conn, nil
}

func (p *Pool) addLocked(entryIndex int) error {
	conn, err := p.buildLocked(entryIndex)
	if err != nil {
		return err
	}
	p.conns = append(p.conns, conn)
	return nil
}

// DialContext 选择余量最大的连接，失败时换下一条。
func (p *Pool) DialContext(ctx context.Context, target string) (net.Conn, error) {
	p.mu.Lock()
	attempts := len(p.conns)
	p.mu.Unlock()
	lastErr := fmt.Errorf("pool: no healthy connection")
	for i := 0; i < attempts; i++ {
		conn := p.pick()
		if conn == nil {
			return nil, lastErr
		}
		conn.inflight.Add(1)
		stream, err := conn.client.DialContext(ctx, target)
		if err != nil {
			conn.inflight.Add(-1)
			p.handleFailure(conn)
			lastErr = err
			continue
		}
		return &meteredConn{Conn: stream, conn: conn, pool: p}, nil
	}
	return nil, lastErr
}

func (p *Pool) handleFailure(conn *connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	conn.healthy.Store(false)
	conn.lastFailed = time.Now()
	if p.cfg.DisableRotateFailure {
		return
	}
	// Do not close a connection with streams in flight: one failed stream must
	// not tear down the others. Replace it only after it is idle.
	if conn.inflight.Load() > 0 {
		return
	}
	for i, item := range p.conns {
		if item != conn {
			continue
		}
		entryIndex := p.entryIndexLocked(conn.entry)
		if entryIndex < 0 {
			return
		}
		replacement, err := p.buildLocked(entryIndex)
		if err != nil {
			return
		}
		p.conns[i] = replacement
		_ = conn.client.Close()
		return
	}
}

func (p *Pool) pick() *connection {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *connection
	bestScore := 0.0
	for _, conn := range p.conns {
		if !conn.healthy.Load() || conn.draining.Load() {
			continue
		}
		// 在途越少越好；吞吐越高，单流余量通常越大。
		score := float64(conn.inflight.Load()+1) / (float64(conn.rate.Load())/1_000_000 + 0.2)
		if best == nil || score < bestScore {
			best = conn
			bestScore = score
		}
	}
	return best
}

func (p *Pool) sampleLoop(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			tick++
			p.sample()
			p.rotate()
			if tick%5 == 0 {
				p.scale()
			}
		}
	}
}

func (p *Pool) sample() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.conns {
		total := conn.bytes.Load()
		delta := total - conn.lastBytes
		conn.lastBytes = total
		old := conn.rate.Load()
		// EWMA，1 秒采样。
		rate := (old*3 + delta) / 4
		conn.rate.Store(rate)
		peak := conn.peakRate.Load()
		if rate > peak {
			conn.peakRate.Store(rate)
			peak = rate
		}
		if peak > 0 && rate < peak/3 {
			conn.lowTicks++
		} else {
			conn.lowTicks = 0
		}
		if !p.cfg.DisableRotateDegrade && conn.lowTicks >= 5 {
			// 吞吐掉到峰值的三分之一并持续 5 秒，标记为待替换。
			conn.draining.Store(true)
		}
		if !conn.healthy.Load() && time.Since(conn.lastFailed) > time.Minute {
			conn.healthy.Store(true)
			conn.draining.Store(false)
		}
		if !conn.healthy.Load() && conn.inflight.Load() == 0 {
			conn.draining.Store(true)
		}
	}
}

func (p *Pool) scale() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.target <= 0 {
		return
	}
	var total float64
	for _, conn := range p.conns {
		total += float64(conn.rate.Load())
	}
	minConns := p.cfg.Connections * len(p.entries)
	now := time.Now()

	if total < p.target*0.85 && len(p.conns) < p.cfg.MaxConnections && now.Sub(p.lastGrow) > 10*time.Second {
		// 选连接最少的入口加一条。
		index := 0
		counts := make([]int, len(p.entries))
		for _, conn := range p.conns {
			for i := range p.entries {
				if p.entries[i].entry.Server == conn.entry {
					counts[i]++
				}
			}
		}
		for i := range counts {
			if counts[i] < counts[index] {
				index = i
			}
		}
		if err := p.addLocked(index); err == nil {
			p.lastGrow = now
		}
		return
	}

	if total > p.target*0.95 && len(p.conns) > minConns && now.Sub(p.lastDrop) > 30*time.Second {
		for i, conn := range p.conns {
			if conn.inflight.Load() == 0 {
				_ = conn.client.Close()
				p.conns = append(p.conns[:i], p.conns[i+1:]...)
				p.lastDrop = now
				return
			}
		}
	}
}

// rotate 把到期的空闲连接换成同入口的下一个端口。
func (p *Pool) rotate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for i, conn := range p.conns {
		if conn.draining.Load() {
			// 已经标记排空或降级，空闲后立即替换。
		} else {
			if p.cfg.PortRotateSeconds <= 0 {
				continue
			}
			if conn.rotateAt.IsZero() || now.Before(conn.rotateAt) {
				continue
			}
			conn.draining.Store(true)
		}
		if conn.inflight.Load() > 0 {
			continue
		}
		entryIndex := p.entryIndexLocked(conn.entry)
		if entryIndex < 0 {
			continue
		}
		replacement, err := p.buildLocked(entryIndex)
		if err != nil {
			continue
		}
		p.conns[i] = replacement
		_ = conn.client.Close()
	}
}

func (p *Pool) entryIndexLocked(server string) int {
	for i, state := range p.entries {
		if state.entry.Server == server {
			return i
		}
	}
	return -1
}

// Close 关闭所有连接。
func (p *Pool) Close() error {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.conns {
		_ = conn.client.Close()
	}
	p.conns = nil
	return nil
}

// Stats 返回每条连接的入口、端口、在途流和吞吐。
func (p *Pool) Stats() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.conns))
	for _, conn := range p.conns {
		out = append(out, fmt.Sprintf("%s:%d inflight=%d rate=%.2fMbps healthy=%v",
			conn.entry, conn.port, conn.inflight.Load(),
			float64(conn.rate.Load())*8/1_000_000, conn.healthy.Load()))
	}
	return out
}

type meteredConn struct {
	net.Conn
	conn   *connection
	pool   *Pool
	once   sync.Once
	failed sync.Once
}

func (c *meteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.conn.bytes.Add(uint64(n))
	}
	if err != nil && err != io.EOF {
		c.reportFailure()
	}
	return n, err
}

func (c *meteredConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.conn.bytes.Add(uint64(n))
	}
	if err != nil {
		c.reportFailure()
	}
	return n, err
}

func (c *meteredConn) reportFailure() {
	c.failed.Do(func() {
		go c.pool.handleFailure(c.conn)
	})
}

func (c *meteredConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.conn.inflight.Add(-1) })
	return err
}
