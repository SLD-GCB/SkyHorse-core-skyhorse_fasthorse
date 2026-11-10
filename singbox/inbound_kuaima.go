package skyhorsebox

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-mux"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
	"skyhorse.local/kuaima/pkg/wire"
)

// RegisterKuaimaInbound adds the Kuaima server to a registry.
func RegisterKuaimaInbound(registry *inbound.Registry) {
	inbound.Register[KuaimaInboundOptions](registry, TypeFastHorse, NewKuaimaInbound)
}

// KuaimaInbound is a Kuaima server node.
type KuaimaInbound struct {
	inbound.Adapter
	ctx        context.Context
	logger     log.ContextLogger
	router     adapter.Router
	tlsConfig  *tls.Config
	obfs       obfs.Config
	verifier   *gate.Verifier
	users      map[string]string
	failure    string
	listeners  []*listener.Listener
	muxService *mux.Service
	downBps    int
	maxStreams int
}

// NewKuaimaInbound builds the node.
func NewKuaimaInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options KuaimaInboundOptions) (adapter.Inbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, E.New("kuaima: tls is required")
	}
	if options.Obfs.Password == "" {
		return nil, E.New("kuaima: obfs.password is required")
	}
	cert, err := tls.LoadX509KeyPair(options.TLS.CertificatePath, options.TLS.KeyPath)
	if err != nil {
		return nil, E.Cause(err, "kuaima: load certificate")
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	}
	users := make(map[string]string, len(options.Users))
	for _, user := range options.Users {
		if user.Username == "" || user.Password == "" {
			continue
		}
		users[user.Username] = user.Password
	}
	obfsConfig := obfs.Config{
		Password:  options.Obfs.Password,
		KeyID:     options.Obfs.KeyID,
		Rotation:  options.Obfs.Rotation.Build(),
		Tolerance: options.Obfs.Tolerance.Build(),
		PadTo:     options.Obfs.PadTo,
		Keys:      []obfs.Key{{ID: options.Obfs.KeyID, Password: options.Obfs.Password}},
	}
	for _, key := range options.Obfs.Keys {
		if key.Password == "" {
			continue
		}
		obfsConfig.Keys = append(obfsConfig.Keys, obfs.Key{ID: key.ID, Password: key.Password})
	}
	node := &KuaimaInbound{
		Adapter:    inbound.NewAdapter(TypeFastHorse, tag),
		ctx:        ctx,
		logger:     logger,
		router:     router,
		tlsConfig:  tlsConfig,
		obfs:       obfsConfig,
		verifier:   gate.NewVerifier(gate.Config{Password: options.Obfs.Password, NodeID: options.Gate.NodeID}),
		users:      users,
		failure:    options.Gate.FailureMode,
		downBps:    options.DownMbps * 125000,
		maxStreams: options.MaxStreams,
	}
	if node.maxStreams <= 0 {
		node.maxStreams = 128
	}
	node.muxService, err = newMuxService(logger, router, adapter.InboundContext{
		Inbound:     tag,
		InboundType: TypeFastHorse,
		Network:     N.NetworkTCP,
	})
	if err != nil {
		return nil, E.Cause(err, "kuaima: mux")
	}

	ports, err := kuaimaPorts(options.ListenPortRange, options.ListenPort)
	if err != nil {
		return nil, err
	}
	for _, port := range ports {
		listen := options.ListenOptions
		listen.ListenPort = port
		node.listeners = append(node.listeners, listener.New(listener.Options{
			Context:           ctx,
			Logger:            logger,
			Network:           []string{N.NetworkTCP},
			Listen:            listen,
			ConnectionHandler: node,
		}))
	}
	return node, nil
}

func (h *KuaimaInbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	for _, listener := range h.listeners {
		if err := listener.Start(); err != nil {
			return err
		}
		scope.Add(listener.Close)
	}
	return nil
}

func (h *KuaimaInbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	tlsConn, user, err := wire.Accept(conn, h.obfs, h.verifier, h.tlsConfig)
	if err != nil {
		h.fail(conn)
		return
	}
	reader := bufio.NewReader(tlsConn)
	if magic, peekErr := reader.Peek(4); peekErr == nil && string(magic) == "FHMX" {
		_, _ = reader.Discard(4)
		h.muxService.NewConnectionEx(ctx, &streamNetConn{
			Reader: reader,
			Writer: tlsConn,
			closer: tlsConn,
		}, metadata.Source, M.Socksaddr{}, func(error) {})
		return
	}
	tlsConn = &bufferedConn{Conn: tlsConn, reader: reader}
	server := wire.NewServer(h.tlsConfig)
	server.MaxStreams = uint32(h.maxStreams)
	server.Authorize = func(ctx context.Context, r *http.Request) error {
		gateUser := wire.UserFromContext(ctx)
		name, password, ok := kuaimaBasicUser(r.Header.Get("Proxy-Authorization"))
		if !ok || name != gateUser || h.users[name] != password {
			return E.New("kuaima: bad credentials")
		}
		return nil
	}
	server.Route = func(ctx context.Context, stream net.Conn, authority string) error {
		if authority == "mux" {
			h.muxService.NewConnectionEx(ctx, stream, metadata.Source, M.Socksaddr{}, func(error) {})
			return nil
		}
		streamMetadata := adapter.InboundContext{
			Inbound:     h.Tag(),
			InboundType: h.Type(),
			Source:      metadata.Source,
			Destination: M.ParseSocksaddr(authority).Unwrap(),
			User:        user,
		}
		done := make(chan struct{})
		var once sync.Once
		h.router.RouteConnectionEx(ctx, stream, streamMetadata, func(error) {
			once.Do(func() { close(done) })
		})
		select {
		case <-done:
		case <-ctx.Done():
		}
		return nil
	}
	server.RouteUDP = func(ctx context.Context, stream net.Conn, authority string) error {
		target, err := net.ResolveUDPAddr("udp", authority)
		if err != nil {
			return err
		}
		conn, err := net.DialUDP("udp", nil, target)
		if err != nil {
			return err
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			buffer := make([]byte, 65535)
			for {
				n, err := readKuaimaDatagram(stream, buffer)
				if err != nil {
					_ = conn.Close()
					return
				}
				if _, err := conn.Write(buffer[:n]); err != nil {
					_ = conn.Close()
					return
				}
			}
		}()
		buffer := make([]byte, 65535)
		for {
			n, err := conn.Read(buffer)
			if err != nil {
				break
			}
			if err := writeKuaimaDatagram(stream, buffer[:n]); err != nil {
				break
			}
		}
		_ = conn.Close()
		<-done
		return nil
	}
	var serveConn net.Conn = tlsConn
	if h.downBps > 0 {
		serveConn = newWriteLimitConn(serveConn, h.downBps)
	}
	server.ServeConnContext(wire.WithUser(ctx, user), serveConn)
}

func (h *KuaimaInbound) fail(conn net.Conn) {
	switch h.failure {
	case "reset":
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	case "close":
		_ = conn.Close()
	default:
		time.Sleep(1500 * time.Millisecond)
		_ = conn.Close()
	}
}

func kuaimaPorts(spec string, fallback uint16) ([]uint16, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		if fallback == 0 {
			return nil, E.New("kuaima: listen_port or listen_port_range is required")
		}
		return []uint16{fallback}, nil
	}
	var ports []uint16
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, ":") {
			bounds := strings.SplitN(part, ":", 2)
			start, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, E.New("kuaima: bad port range ", part)
			}
			end, err := strconv.Atoi(bounds[1])
			if err != nil || end < start {
				return nil, E.New("kuaima: bad port range ", part)
			}
			for port := start; port <= end; port++ {
				if port <= 0 || port > 65535 {
					return nil, E.New("kuaima: bad port ", port)
				}
				ports = append(ports, uint16(port))
			}
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port <= 0 || port > 65535 {
			return nil, E.New("kuaima: bad port ", part)
		}
		ports = append(ports, uint16(port))
	}
	if len(ports) == 0 {
		return nil, E.New("kuaima: no ports")
	}
	return ports, nil
}

func kuaimaBasicUser(header string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false
	}
	name, password, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", false
	}
	return name, password, true
}

func writeKuaimaDatagram(w io.Writer, p []byte) error {
	if len(p) > 65535 {
		return E.New("kuaima: udp datagram too large")
	}
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(p)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

func readKuaimaDatagram(r io.Reader, p []byte) (int, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, err
	}
	size := int(binary.BigEndian.Uint16(header[:]))
	if size > len(p) {
		return 0, io.ErrShortBuffer
	}
	if _, err := io.ReadFull(r, p[:size]); err != nil {
		return 0, err
	}
	return size, nil
}
