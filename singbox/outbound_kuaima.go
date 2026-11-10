package skyhorsebox

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"skyhorse.local/kuaima/pkg/client"
	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

// RegisterKuaimaOutbound adds the Kuaima client to a registry.
func RegisterKuaimaOutbound(registry *outbound.Registry) {
	outbound.Register[KuaimaOutboundOptions](registry, TypeFastHorse, NewKuaimaOutbound)
}

// KuaimaOutbound is a Kuaima client node.
type KuaimaOutbound struct {
	outbound.Adapter
	mu       sync.Mutex
	cfg      client.Config
	ports    []uint16
	next     int
	client   *client.Client
	hopEvery time.Duration
	hopStop  chan struct{}
	hopOnce  sync.Once
	hopWG    sync.WaitGroup
	sem      chan struct{}
}

// NewKuaimaOutbound builds the client without dialing.
func NewKuaimaOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options KuaimaOutboundOptions) (adapter.Outbound, error) {
	if options.Server == "" || options.ServerPort == 0 {
		return nil, E.New("kuaima: server and server_port are required")
	}
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, E.New("kuaima: tls is required")
	}
	serverName := options.Server
	if options.TLS.ServerName != "" {
		serverName = options.TLS.ServerName
	}
	certPEM := ""
	if options.TLS.CertificatePath != "" {
		data, err := os.ReadFile(options.TLS.CertificatePath)
		if err != nil {
			return nil, E.Cause(err, "kuaima: read certificate")
		}
		certPEM = string(data)
	}
	if certPEM == "" && !options.TLS.Insecure {
		return nil, E.New("kuaima: tls.certificate_path or tls.insecure is required")
	}
	obfsConfig := obfs.Config{
		Password:  options.Obfs.Password,
		KeyID:     options.Obfs.KeyID,
		Rotation:  options.Obfs.Rotation.Build(),
		Tolerance: options.Obfs.Tolerance.Build(),
		PadTo:     options.Obfs.PadTo,
	}
	gateConfig := gate.Config{Password: options.Obfs.Password, NodeID: options.Gate.NodeID}
	ports := []uint16{options.ServerPort}
	if len(options.ServerPorts) > 0 {
		parsed, err := kuaimaPorts(strings.Join(options.ServerPorts, ","), options.ServerPort)
		if err != nil {
			return nil, err
		}
		ports = parsed
	}
	if options.PortHopEnabled != nil && !*options.PortHopEnabled {
		ports = []uint16{options.ServerPort}
	}
	outboundNode := &KuaimaOutbound{
		Adapter: outbound.NewAdapterWithDialerOptions(TypeFastHorse, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
	}
	baseCfg := client.Config{
		Server:               options.Server,
		ServerName:           serverName,
		Username:             options.Username,
		Password:             options.Password,
		CertificatePEM:       certPEM,
		Insecure:             options.TLS.Insecure,
		Obfs:                 obfsConfig,
		Gate:                 gateConfig,
		UploadBytesPerSecond: options.UpMbps * 125000,
	}
	baseCfg.Port = ports[0]
	instance, err := client.New(ctx, baseCfg)
	if err != nil {
		return nil, E.Cause(err, "kuaima: client")
	}
	outboundNode.cfg = baseCfg
	outboundNode.ports = ports
	outboundNode.client = instance
	outboundNode.next = 1
	streams := options.MaxStreamsPerConnection
	if streams <= 0 {
		streams = 64
	}
	outboundNode.sem = make(chan struct{}, streams)
	outboundNode.hopEvery = 0
	if options.PortHopEnabled != nil && *options.PortHopEnabled {
		outboundNode.hopEvery = time.Duration(options.PortRotateSeconds) * time.Second
	}
	if len(ports) <= 1 {
		outboundNode.hopEvery = 0
	}
	return outboundNode, nil
}

func (o *KuaimaOutbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
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

func (o *KuaimaOutbound) rotateClient() {
	o.mu.Lock()
	if len(o.ports) == 0 {
		o.mu.Unlock()
		return
	}
	cfg := o.cfg
	cfg.Port = o.ports[o.next%len(o.ports)]
	o.next++
	o.mu.Unlock()
	instance, err := client.New(context.Background(), cfg)
	if err != nil {
		return
	}
	o.mu.Lock()
	old := o.client
	o.client = instance
	o.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (o *KuaimaOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.mu.Lock()
	instance := o.client
	o.mu.Unlock()
	if instance == nil {
		return nil, E.New("kuaima: client not initialized")
	}
	if N.NetworkName(network) == N.NetworkUDP {
		if err := o.acquire(ctx); err != nil {
			return nil, err
		}
		pc, err := instance.DialUDP(ctx, destination.String())
		if err != nil {
			o.release()
			return nil, err
		}
		return &packetConnAdapter{PacketConn: pc, remote: destination.UDPAddr(), release: o.release}, nil
	}
	if err := o.acquire(ctx); err != nil {
		return nil, err
	}
	conn, err := instance.DialContext(ctx, destination.String())
	if err != nil {
		o.release()
		return nil, err
	}
	return &limitedConn{Conn: conn, release: o.release}, nil
}

func (o *KuaimaOutbound) acquire(ctx context.Context) error {
	select {
	case o.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *KuaimaOutbound) release() {
	select {
	case <-o.sem:
	default:
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (o *KuaimaOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.mu.Lock()
	instance := o.client
	o.mu.Unlock()
	if instance == nil {
		return nil, E.New("kuaima: client not initialized")
	}
	if err := o.acquire(ctx); err != nil {
		return nil, err
	}
	pc, err := instance.DialUDP(ctx, destination.String())
	if err != nil {
		o.release()
		return nil, err
	}
	return &packetConnAdapter{PacketConn: pc, release: o.release}, nil
}

type packetConnAdapter struct {
	net.PacketConn
	remote  net.Addr
	once    sync.Once
	release func()
}

func (c *packetConnAdapter) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(c.release)
	return err
}

func (c *packetConnAdapter) Read(p []byte) (int, error) {
	n, _, err := c.PacketConn.ReadFrom(p)
	return n, err
}

func (c *packetConnAdapter) Write(p []byte) (int, error) {
	return c.PacketConn.WriteTo(p, c.remote)
}

func (c *packetConnAdapter) RemoteAddr() net.Addr { return c.remote }

func (o *KuaimaOutbound) Close() error {
	if o.hopStop != nil {
		o.hopOnce.Do(func() { close(o.hopStop) })
		o.hopWG.Wait()
	}
	o.mu.Lock()
	instance := o.client
	o.client = nil
	o.mu.Unlock()
	if instance != nil {
		_ = instance.Close()
	}
	return nil
}

type kuaimaDialer struct {
	cfg   client.Config
	ports []uint16
	next  atomic.Uint64
}

func (d *kuaimaDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	index := d.next.Add(1) - 1
	cfg := d.cfg
	cfg.Port = d.ports[int(index)%len(d.ports)]
	instance, err := client.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	conn, err := instance.DialRaw(ctx)
	if err != nil {
		_ = instance.Close()
		return nil, err
	}
	return &fhRotatingConn{Conn: conn, client: instance}, nil
}

type fhRotatingConn struct {
	net.Conn
	client *client.Client
	once   sync.Once
}

func (c *fhRotatingConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { _ = c.client.Close() })
	return err
}

func (d *kuaimaDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("kuaima: mux packet transport is not supported")
}
