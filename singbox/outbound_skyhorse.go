package skyhorsebox

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-mux"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	core "tianma/boxapi"
)

// RegisterOutbound adds the protocol to an outbound registry.
func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[OutboundOptions](registry, Type, NewOutbound)
}

// Outbound is a SkyHorse client node inside the kernel.
type Outbound struct {
	outbound.Adapter
	h3          []*core.H3Client
	h3Load      []atomic.Int64
	h3once      sync.Once
	h3mu        sync.Mutex
	h3Cfg       core.Config
	h3Addrs     []string
	client      *core.H3Client
	h3Next      int
	h3Rotate    time.Time
	h3Every     time.Duration
	h3Max       int
	h3Limit     int
	h3Bytes     []atomic.Int64
	h3Bad       []atomic.Bool
	h3Rate      []float64
	h3Sample    time.Time
	h3Full      int
	udpSessions atomic.Int64
	mux         *mux.Client
	hopEvery    time.Duration
	hopStop     chan struct{}
	hopOnce     sync.Once
	hopWG       sync.WaitGroup
	sem         chan struct{}
}

// NewOutbound builds the client without dialing anything.
func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options OutboundOptions) (adapter.Outbound, error) {
	if options.Server == "" || options.ServerPort == 0 {
		return nil, E.New("skyhorse: server and server_port are required")
	}

	serverName := options.Server
	pin := ""
	if options.TLS != nil {
		if options.TLS.ServerName != "" {
			serverName = options.TLS.ServerName
		}
		pin = pinHex(options.TLS)
	}

	servers := make([]string, 0, len(options.ServerPorts))
	for _, port := range options.ServerPorts {
		if port != "" {
			servers = append(servers, net.JoinHostPort(options.Server, port))
		}
	}

	cfg := core.Config{
		Server:                  options.Server,
		ServerPort:              int(options.ServerPort),
		Servers:                 servers,
		ServerName:              serverName,
		Pin:                     pin,
		UUID:                    options.UUID,
		Password:                options.Password,
		ObfsPassword:            options.ObfsPassword,
		ObfsKeys:                options.ObfsKeys,
		NodeID:                  options.NodeID,
		Insecure:                options.TLS != nil && options.TLS.Insecure,
		ObfsRotationSeconds:     options.ObfsRotationSeconds,
		ObfsToleranceSeconds:    options.ObfsToleranceSeconds,
		UpMbps:                  options.UpMbps,
		DownMbps:                options.DownMbps,
		BBRProfile:              options.BBRProfile,
		DisableLossCompensation: options.DisableLossCompensation,
		Connections:             options.Connections,
		MaxStreamsPerConnection: options.MaxStreamsPerConnection,
		PortRotateSeconds:       options.PortRotateSeconds,
	}
	addrs := servers
	if len(addrs) == 0 {
		addrs = []string{net.JoinHostPort(options.Server, fmt.Sprint(options.ServerPort))}
	}
	hopEvery := time.Duration(0)
	if options.PortHopEnabled != nil && *options.PortHopEnabled {
		hopEvery = time.Duration(options.PortRotateSeconds) * time.Second
	}
	if len(addrs) <= 1 {
		hopEvery = 0
	}
	client, err := core.NewH3(cfg)
	if err != nil {
		return nil, E.Cause(err, "skyhorse: client")
	}
	streamLimit := options.MaxStreamsPerConnection
	if streamLimit <= 0 {
		streamLimit = 256
	}
	node := &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(Type, tag,
			[]string{"tcp", "udp"}, options.DialerOptions),
		h3Cfg:    cfg,
		h3Addrs:  addrs,
		h3Every:  hopEvery,
		client:   client,
		hopEvery: hopEvery,
		sem:      make(chan struct{}, streamLimit),
	}
	return node, nil
}

// Start runs the port-hop timer. Any new mux transport after a tick uses the
// next port; existing streams are left alone.
func (o *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart || o.hopEvery <= 0 {
		return nil
	}
	o.hopStop = make(chan struct{})
	stop := o.hopStop
	o.hopWG.Add(1)
	go func() {
		defer o.hopWG.Done()
		ticker := time.NewTicker(o.hopEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				o.rotateClient()
			}
		}
	}()
	scope.Add(func() error {
		o.hopOnce.Do(func() { close(stop) })
		o.hopWG.Wait()
		return nil
	})
	return nil
}

func (o *Outbound) rotateClient() {
	o.h3mu.Lock()
	index := o.h3Next
	o.h3Next++
	address := o.h3Addrs[index%len(o.h3Addrs)]
	cfg := o.h3Cfg
	cfg.Servers = []string{address}
	o.h3mu.Unlock()
	client, err := core.NewH3(cfg)
	if err != nil {
		return
	}
	o.h3mu.Lock()
	old := o.client
	o.client = client
	o.h3mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.h3mu.Lock()
	client := o.client
	o.h3mu.Unlock()
	if client == nil {
		return nil, E.New("skyhorse: client not initialized")
	}
	if err := o.acquire(ctx); err != nil {
		return nil, err
	}
	conn, err := client.Dial(ctx, destination.String())
	if err != nil {
		o.release()
		return nil, err
	}
	return &limitedConn{Conn: conn, release: o.release}, nil
}

func (o *Outbound) acquire(ctx context.Context) error {
	select {
	case o.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Outbound) release() {
	select {
	case <-o.sem:
	default:
	}
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.h3mu.Lock()
	index := o.h3Next
	o.h3Next++
	address := o.h3Addrs[index%len(o.h3Addrs)]
	cfg := o.h3Cfg
	cfg.Servers = []string{address}
	o.h3mu.Unlock()
	if err := o.acquire(ctx); err != nil {
		return nil, err
	}
	client, err := core.NewH3(cfg)
	if err != nil {
		o.release()
		return nil, err
	}
	conn, err := client.DialUDP(ctx, destination.String())
	if err != nil {
		_ = client.Close()
		o.release()
		return nil, err
	}
	return &nativePacketConn{PacketConn: conn, client: client, release: o.release}, nil
}

type nativePacketConn struct {
	net.PacketConn
	client  *core.H3Client
	once    sync.Once
	release func()
}

func (c *nativePacketConn) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(func() {
		_ = c.client.Close()
		c.release()
	})
	return err
}

type h3Dialer struct {
	cfg   core.Config
	addrs []string
	next  atomic.Uint64
}

func (d *h3Dialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	index := d.next.Add(1) - 1
	cfg := d.cfg
	cfg.Servers = []string{d.addrs[int(index)%len(d.addrs)]}
	client, err := core.NewH3(cfg)
	if err != nil {
		return nil, err
	}
	conn, err := client.DialRaw(ctx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &rotatingConn{Conn: conn, client: client}, nil
}

type rotatingConn struct {
	net.Conn
	client *core.H3Client
	once   sync.Once
}

func (c *rotatingConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { _ = c.client.Close() })
	return err
}

func (d *h3Dialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("skyhorse: mux packet transport is not supported")
}

func (o *Outbound) Close() error {
	if o.hopStop != nil {
		o.hopOnce.Do(func() { close(o.hopStop) })
		o.hopWG.Wait()
	}
	o.h3mu.Lock()
	client := o.client
	o.client = nil
	o.h3mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	return nil
}
