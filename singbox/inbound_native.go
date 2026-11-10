package skyhorsebox

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-mux"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	core "tianma/boxapi"
)

// RegisterInbound registers the native QUIC node.
func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[InboundOptions](registry, Type, NewInbound)
}

// Inbound is a native QUIC server node. TCP uses QUIC streams with SOCKS5
// CONNECT, UDP uses QUIC DATAGRAM with a SOCKS5 UDP header.
type Inbound struct {
	inbound.Adapter
	logger      log.ContextLogger
	router      adapter.Router
	users       map[string]User
	tlsConfig   *tls.Config
	obfs        []byte
	obfsKeys    []string
	nodeID      string
	portRange   string
	failureMode string
	listenAddr  string
	listenPorts []int

	maxTxBps                uint64
	maxRxBps                uint64
	ignoreClientBandwidth   bool
	disableLossCompensation bool
	bbrProfile              string
	maxConns                int64
	maxStreams              int64
	liveConns               atomic.Int64
	guard                   *core.Guard
	policy                  *core.Policy

	transports []*quic.Transport
	sockets    []*net.UDPConn
	listeners  []*quic.Listener
	connMu     sync.Mutex
	conns      map[*quic.Conn]struct{}
	muxService *mux.Service
	cancel     context.CancelFunc
}

// NewInbound builds the node without binding anything.
func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options InboundOptions) (adapter.Inbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, E.New("skyhorse: tls is required")
	}
	if options.TLS.CertificatePath == "" || options.TLS.KeyPath == "" {
		return nil, E.New("skyhorse: tls certificate_path and key_path are required")
	}
	tlsConfig, err := core.ServerTLS(options.TLS.CertificatePath, options.TLS.KeyPath)
	if err != nil {
		return nil, E.Cause(err, "skyhorse: load tls")
	}
	users := make(map[string]User, len(options.Users))
	for _, user := range options.Users {
		if user.UUID == "" || user.Password == "" {
			continue
		}
		users[user.UUID] = user
	}
	host := "::"
	if options.Listen != nil {
		host = options.Listen.Build(netip.IPv6Unspecified()).String()
	}
	basePort := int(options.ListenPort)
	ports := parsePortRange(options.PortRange, basePort)
	compiledPolicy, err := core.NewPolicy(options.Policy)
	if err != nil {
		return nil, E.Cause(err, "skyhorse: policy")
	}
	muxService, muxErr := newMuxService(logger, router, adapter.InboundContext{Inbound: tag, Network: N.NetworkTCP})
	if muxErr != nil {
		return nil, E.Cause(muxErr, "skyhorse: mux")
	}
	return &Inbound{
		Adapter:                 inbound.NewAdapter(Type, tag),
		logger:                  logger,
		router:                  router,
		users:                   users,
		tlsConfig:               tlsConfig,
		obfs:                    []byte(options.ObfsPassword),
		obfsKeys:                append([]string{options.ObfsPassword}, options.ObfsKeys...),
		nodeID:                  options.NodeID,
		portRange:               options.PortRange,
		failureMode:             options.FailureMode,
		listenAddr:              net.JoinHostPort(host, strconv.Itoa(basePort)),
		listenPorts:             ports,
		maxConns:                int64(options.MaxConnections),
		maxStreams:              int64(options.MaxStreamsPerConnection),
		maxTxBps:                uint64(options.DownMbps) * 125000,
		maxRxBps:                uint64(options.UpMbps) * 125000,
		ignoreClientBandwidth:   options.IgnoreClientBandwidth,
		disableLossCompensation: options.DisableLossCompensation,
		bbrProfile:              options.BBRProfile,
		guard:                   core.NewGuard(options.Guard),
		policy:                  compiledPolicy,
		conns:                   make(map[*quic.Conn]struct{}),
		muxService:              muxService,
	}, nil
}

// Start binds all UDP ports in the configured range.
func (h *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	host, _, err := net.SplitHostPort(h.listenAddr)
	if err != nil {
		host = "::"
	}
	quicConfig := core.QUICConfig()
	if h.maxStreams > 0 {
		quicConfig.MaxIncomingStreams = h.maxStreams
	}
	secret := append(append([]byte(nil), h.obfs...), []byte(h.nodeID)...)

	for _, port := range h.listenPorts {
		address, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			h.Close()
			return E.Cause(err, "skyhorse: listen quic")
		}
		socket, err := net.ListenUDP("udp", address)
		if err != nil {
			h.Close()
			return E.Cause(err, "skyhorse: listen quic")
		}
		_ = socket.SetReadBuffer(4 << 20)
		_ = socket.SetWriteBuffer(4 << 20)
		var packetConn net.PacketConn = socket
		if len(h.obfsKeys) > 0 {
			packetConn, err = core.WrapObfsKeys(packetConn, h.obfsKeys, 0, true)
			if err != nil {
				_ = socket.Close()
				h.Close()
				return E.Cause(err, "skyhorse: obfs")
			}
		}
		if len(secret) > 0 {
			packetConn = core.NewGateServer(packetConn, secret)
		}
		transport := &quic.Transport{Conn: packetConn}
		listener, err := transport.Listen(h.tlsConfig, quicConfig)
		if err != nil {
			_ = transport.Close()
			_ = socket.Close()
			h.Close()
			return E.Cause(err, "skyhorse: listen quic")
		}
		h.transports = append(h.transports, transport)
		h.sockets = append(h.sockets, socket)
		h.listeners = append(h.listeners, listener)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	scope.Add(func() error { return h.Close() })
	for _, listener := range h.listeners {
		go h.accept(ctx, listener)
	}
	return nil
}

// Close releases all listeners, sockets and accepted connections.
func (h *Inbound) Close() error {
	if h.cancel != nil {
		h.cancel()
	}
	h.connMu.Lock()
	conns := make([]*quic.Conn, 0, len(h.conns))
	for conn := range h.conns {
		conns = append(conns, conn)
	}
	h.conns = make(map[*quic.Conn]struct{})
	h.connMu.Unlock()
	for _, conn := range conns {
		_ = conn.CloseWithError(0, "server closed")
	}
	for _, listener := range h.listeners {
		_ = listener.Close()
	}
	h.listeners = nil
	for _, transport := range h.transports {
		_ = transport.Close()
	}
	h.transports = nil
	for _, socket := range h.sockets {
		_ = socket.Close()
	}
	h.sockets = nil
	return nil
}

func (h *Inbound) accept(ctx context.Context, listener *quic.Listener) {
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return
		}
		if h.maxConns > 0 && h.liveConns.Add(1) > h.maxConns {
			h.liveConns.Add(-1)
			_ = conn.CloseWithError(0x1, "too many connections")
			continue
		}
		ip := remoteIP(conn.RemoteAddr())
		release, allowed := h.guard.AllowConnection(ip)
		if !allowed {
			if h.maxConns > 0 {
				h.liveConns.Add(-1)
			}
			_ = conn.CloseWithError(0x1, "too many connections")
			continue
		}
		h.connMu.Lock()
		h.conns[conn] = struct{}{}
		h.connMu.Unlock()
		go func() {
			defer func() {
				h.connMu.Lock()
				delete(h.conns, conn)
				h.connMu.Unlock()
			}()
			defer func() {
				if h.maxConns > 0 {
					h.liveConns.Add(-1)
				}
			}()
			defer release()
			h.handleConn(ctx, conn)
		}()
	}
}

type connState struct {
	associated atomic.Bool
}

func (h *Inbound) handleConn(ctx context.Context, conn *quic.Conn) {
	state := &connState{}
	udpDone := make(chan struct{})
	go h.handleDatagrams(ctx, conn, state, udpDone)
	defer close(udpDone)

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go h.handleStream(stream, conn, state)
	}
}

type streamReaderWriter struct {
	io.Reader
	io.Writer
}

func (h *Inbound) handleStream(stream *quic.Stream, conn *quic.Conn, state *connState) {
	defer stream.Close()
	reader := bufio.NewReader(stream)
	first, err := reader.Peek(1)
	if err != nil {
		return
	}
	if first[0] != 5 {
		source := M.ParseSocksaddr(conn.RemoteAddr().String())
		h.muxService.NewConnectionEx(context.Background(), &streamNetConn{
			Reader: reader,
			Writer: stream,
			closer: stream,
		}, source, M.Socksaddr{}, func(error) {})
		return
	}
	rw := &streamReaderWriter{Reader: reader, Writer: stream}
	user, err := h.socksAuth(rw, conn)
	if err != nil {
		return
	}
	command, target, err := readSocksRequest(rw)
	if err != nil || command == 0 {
		return
	}
	h.configureCongestion(conn)
	switch command {
	case 1:
		h.handleTCP(stream, target, user)
	case 3:
		state.associated.Store(true)
		if err := writeSocksReply(stream); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, stream)
		state.associated.Store(false)
	}
}

func (h *Inbound) socksAuth(stream io.ReadWriter, conn *quic.Conn) (User, error) {
	ip := remoteIP(conn.RemoteAddr())
	if h.guard.AuthBlocked(ip) {
		return User{}, errors.New("skyhorse: auth blocked")
	}
	var greeting [2]byte
	if _, err := io.ReadFull(stream, greeting[:]); err != nil {
		return User{}, err
	}
	if greeting[0] != 5 {
		return User{}, errors.New("skyhorse: bad socks version")
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(stream, methods); err != nil {
		return User{}, err
	}
	if _, err := stream.Write([]byte{5, 2}); err != nil {
		return User{}, err
	}
	var authHead [2]byte
	if _, err := io.ReadFull(stream, authHead[:]); err != nil {
		return User{}, err
	}
	if authHead[0] != 1 {
		return User{}, errors.New("skyhorse: bad socks auth version")
	}
	username := make([]byte, int(authHead[1]))
	if _, err := io.ReadFull(stream, username); err != nil {
		return User{}, err
	}
	var passLen [1]byte
	if _, err := io.ReadFull(stream, passLen[:]); err != nil {
		return User{}, err
	}
	passwordHex := make([]byte, int(passLen[0]))
	if _, err := io.ReadFull(stream, passwordHex); err != nil {
		return User{}, err
	}
	user, known := h.users[string(username)]
	if !known {
		h.guard.RecordAuthFailure(ip)
		_, _ = stream.Write([]byte{1, 1})
		return User{}, errors.New("skyhorse: unknown user")
	}
	state := conn.ConnectionState()
	expected, err := core.Token(&state.TLS, user.UUID, user.Password)
	if err != nil || subtle.ConstantTimeCompare([]byte(hex.EncodeToString(expected)), passwordHex) != 1 {
		h.guard.RecordAuthFailure(ip)
		_, _ = stream.Write([]byte{1, 1})
		return User{}, errors.New("skyhorse: bad token")
	}
	h.guard.ResetAuthFailures(ip)
	if _, err := stream.Write([]byte{1, 0}); err != nil {
		return User{}, err
	}
	return user, nil
}

func (h *Inbound) configureCongestion(conn *quic.Conn) {
	if h.ignoreClientBandwidth || h.maxTxBps == 0 {
		core.UseBBR(conn, h.bbrProfile)
		return
	}
	core.UseBrutal(conn, h.maxTxBps, h.disableLossCompensation)
}

func (h *Inbound) handleTCP(stream *quic.Stream, target string, _ User) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		_ = writeSocksFailure(stream)
		return
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = writeSocksFailure(stream)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ip, err := h.policy.Check(ctx, host, uint16(port))
	if err != nil {
		_ = writeSocksFailure(stream)
		return
	}
	dialTarget := net.JoinHostPort(ip.String(), portText)
	conn, err := net.DialTimeout("tcp", dialTarget, 10*time.Second)
	if err != nil {
		_ = writeSocksFailure(stream)
		return
	}
	defer conn.Close()
	if err := writeSocksReply(stream); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(conn, stream)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, _ = io.Copy(stream, conn)
}

func (h *Inbound) handleDatagrams(ctx context.Context, conn *quic.Conn, state *connState, done <-chan struct{}) {
	var socket *net.UDPConn
	defer func() {
		if socket != nil {
			_ = socket.Close()
		}
	}()
	for {
		data, err := conn.ReceiveDatagram(ctx)
		if err != nil {
			return
		}
		if !state.associated.Load() {
			continue
		}
		addr, payload, err := parseSocksUDP(data)
		if err != nil {
			continue
		}
		if h.policy != nil {
			if _, err := h.policy.Check(ctx, addr.IP.String(), uint16(addr.Port)); err != nil {
				continue
			}
		}
		if socket == nil {
			socket, err = net.ListenUDP("udp", nil)
			if err != nil {
				return
			}
			go h.udpReplies(ctx, conn, socket, done)
		}
		_, _ = socket.WriteToUDP(payload, addr)
	}
}

func (h *Inbound) udpReplies(ctx context.Context, conn *quic.Conn, socket *net.UDPConn, done <-chan struct{}) {
	buffer := make([]byte, 65535)
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		default:
		}
		_ = socket.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := socket.ReadFromUDP(buffer)
		if err != nil {
			continue
		}
		payload, err := buildSocksUDP(addr.String(), buffer[:n])
		if err != nil {
			continue
		}
		_ = conn.SendDatagram(payload)
	}
}

func parsePortRange(value string, fallback int) []int {
	value = strings.TrimSpace(value)
	if value == "" {
		return []int{fallback}
	}
	seen := make(map[int]bool)
	var ports []int
	add := func(port int) {
		if port < 1 || port > 65535 || seen[port] || len(ports) >= 256 {
			return
		}
		seen[port] = true
		ports = append(ports, port)
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if start, end, ok := strings.Cut(part, ":"); ok {
			first, err1 := strconv.Atoi(start)
			last, err2 := strconv.Atoi(end)
			if err1 != nil || err2 != nil {
				continue
			}
			if first > last {
				first, last = last, first
			}
			for port := first; port <= last; port++ {
				add(port)
			}
			continue
		}
		if port, err := strconv.Atoi(part); err == nil {
			add(port)
		}
	}
	if len(ports) == 0 {
		return []int{fallback}
	}
	return ports
}

func readSocksRequest(r io.Reader) (byte, string, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, "", err
	}
	if head[0] != 5 {
		return 0, "", errors.New("skyhorse: bad socks request")
	}
	var host string
	switch head[3] {
	case 1:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, "", err
		}
		host = net.IP(buf).String()
	case 4:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, "", err
		}
		host = net.IP(buf).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return 0, "", err
		}
		buf := make([]byte, int(length[0]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, "", err
		}
		host = string(buf)
	default:
		return 0, "", errors.New("skyhorse: bad socks atyp")
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(r, portBuf[:]); err != nil {
		return 0, "", err
	}
	port := binary.BigEndian.Uint16(portBuf[:])
	return head[1], net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func writeSocksReply(w io.Writer) error {
	_, err := w.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func writeSocksFailure(w io.Writer) error {
	_, err := w.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func buildSocksUDP(target string, payload []byte) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}
	out := []byte{0, 0, 0}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		out = append(out, 1)
		out = append(out, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		out = append(out, 4)
		out = append(out, ip6...)
	} else {
		if len(host) > 255 {
			return nil, errors.New("skyhorse: host too long")
		}
		out = append(out, 3, byte(len(host)))
		out = append(out, host...)
	}
	out = append(out, byte(port>>8), byte(port))
	out = append(out, payload...)
	return out, nil
}

func parseSocksUDP(data []byte) (*net.UDPAddr, []byte, error) {
	if len(data) < 4 {
		return nil, nil, errors.New("skyhorse: short socks udp")
	}
	offset := 4
	var host string
	switch data[3] {
	case 1:
		if len(data) < offset+4+2 {
			return nil, nil, errors.New("skyhorse: short udp ipv4")
		}
		host = net.IP(data[offset : offset+4]).String()
		offset += 4
	case 4:
		if len(data) < offset+16+2 {
			return nil, nil, errors.New("skyhorse: short udp ipv6")
		}
		host = net.IP(data[offset : offset+16]).String()
		offset += 16
	case 3:
		if len(data) < offset+1 {
			return nil, nil, errors.New("skyhorse: short udp domain")
		}
		size := int(data[offset])
		offset++
		if len(data) < offset+size+2 {
			return nil, nil, errors.New("skyhorse: short udp domain")
		}
		host = string(data[offset : offset+size])
		offset += size
	default:
		return nil, nil, errors.New("skyhorse: bad udp atyp")
	}
	port := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, nil, err
	}
	return addr, data[offset:], nil
}

func remoteIP(address net.Addr) string {
	if address == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return address.String()
	}
	return host
}

var _ = fmt.Sprintf
var _ = sync.Once{}
