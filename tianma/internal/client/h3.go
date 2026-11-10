package client

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	quic "github.com/apernet/quic-go"

	"tianma/internal/congestion"
	"tianma/internal/obfs"
	"tianma/internal/proto"
	"tianma/internal/wire"
)

// H3Client is kept as the kernel-facing name. It no longer uses HTTP/3:
// TCP runs on QUIC streams with SOCKS5 CONNECT, UDP runs on QUIC DATAGRAM
// with a standard SOCKS5 UDP header.
type H3Client struct {
	config    Config
	tlsConfig *tls.Config
	quicConn  *quic.Transport
	socket    *net.UDPConn
	mu        sync.Mutex
	conn      *quic.Conn
	udpAssoc  net.Conn
}

// NewH3 builds a native QUIC client.
func NewH3(config Config) (*H3Client, error) {
	pin, err := decodePin(config.Pin)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := wire.ClientTLS(config.ServerName, pin, config.Insecure)
	if err != nil {
		return nil, err
	}
	return &H3Client{config: config, tlsConfig: tlsConfig}, nil
}

func (c *H3Client) address() string {
	host := c.config.Server
	port := c.config.ServerPort
	if len(c.config.Servers) > 0 {
		return c.config.Servers[0]
	}
	return net.JoinHostPort(host, fmt.Sprint(port))
}

func (c *H3Client) dial(ctx context.Context, address string, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Conn, error) {
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	var packetConn net.PacketConn = socket
	if c.config.ObfsPassword != "" {
		passwords := append([]string{c.config.ObfsPassword}, c.config.ObfsKeys...)
		keys := make([][]byte, 0, len(passwords))
		for _, password := range passwords {
			if password != "" {
				keys = append(keys, []byte(password))
			}
		}
		packetConn, err = obfs.WrapClientKeys(packetConn, keys, time.Duration(c.config.ObfsRotationSeconds)*time.Second)
		if err != nil {
			_ = socket.Close()
			return nil, err
		}
	}
	secret := append(append([]byte(nil), []byte(c.config.ObfsPassword)...), []byte(c.config.NodeID)...)
	if len(secret) > 0 {
		packetConn = wire.NewGateClient(packetConn, secret)
	}
	transport := &quic.Transport{Conn: packetConn}
	conn, err := transport.Dial(ctx, remote, tlsConfig, quicConfig)
	if err != nil {
		_ = transport.Close()
		_ = socket.Close()
		return nil, err
	}
	// The sender side also needs an explicit congestion controller; otherwise
	// uploads fall back to the library default while downloads use BBR/Brutal.
	if c.config.BBRProfile != "" {
		congestion.UseBBR(conn, c.config.BBRProfile)
	} else if c.config.UpMbps > 0 {
		congestion.UseBrutal(conn, uint64(c.config.UpMbps)*125000, c.config.DisableLossCompensation)
	}
	c.mu.Lock()
	c.quicConn = transport
	c.socket = socket
	c.mu.Unlock()
	return conn, nil
}

func (c *H3Client) ensureConn(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	if conn != nil && conn.Context().Err() == nil {
		c.mu.Unlock()
		return nil
	}
	if conn != nil {
		c.conn = nil
		c.udpAssoc = nil
		c.mu.Unlock()
		_ = conn.CloseWithError(0, "")
	} else {
		c.mu.Unlock()
	}
	conn, err := c.dial(ctx, c.address(), c.tlsConfig, wire.QUICConfig())
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// Alive reports whether the cached QUIC connection is still usable. A client
// that has not dialed yet is considered alive and will connect on first use.
func (c *H3Client) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn == nil || c.conn.Context().Err() == nil
}

// invalidate drops a connection after a stream-level failure so the next
// request reconnects instead of reusing a dead transport.
func (c *H3Client) invalidate(conn *quic.Conn) {
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	c.udpAssoc = nil
	c.mu.Unlock()
	_ = conn.CloseWithError(0, "")
}

func (c *H3Client) token() (string, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return "", errors.New("tianma: quic connection not established")
	}
	state := conn.ConnectionState()
	token, err := wire.Token(&state.TLS, c.config.UUID, c.config.Password)
	if err != nil {
		return "", err
	}
	return proto.EncodeToken(c.config.UUID, token), nil
}

func (c *H3Client) openStream(ctx context.Context) (*quic.Stream, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureConn(ctx); err != nil {
			return nil, err
		}
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		stream, err := conn.OpenStreamSync(ctx)
		if err == nil {
			return stream, nil
		}
		c.invalidate(conn)
	}
	return nil, errors.New("tianma: open stream failed")
}

// DialRaw opens a QUIC stream without the SOCKS5 handshake. It is used as the
// transport for sing-mux, which carries its own stream framing.
func (c *H3Client) DialRaw(ctx context.Context) (net.Conn, error) {
	stream, err := c.openStream(ctx)
	if err != nil {
		return nil, err
	}
	return &streamConn{Stream: stream}, nil
}

// Dial opens one SOCKS5 TCP CONNECT stream.
func (c *H3Client) Dial(ctx context.Context, target string) (net.Conn, error) {
	stream, err := c.openStream(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.socksHandshake(stream); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := socksConnect(stream, target); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &streamConn{Stream: stream}, nil
}

// DialUDP creates the SOCKS5 UDP association and returns a DATAGRAM-backed
// packet connection.
func (c *H3Client) DialUDP(ctx context.Context, target string) (net.PacketConn, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	assoc := c.udpAssoc
	if assoc != nil {
		if sc, ok := assoc.(*streamConn); ok && sc.Stream.Context().Err() != nil {
			assoc = nil
			c.udpAssoc = nil
		}
	}
	c.mu.Unlock()
	if assoc == nil {
		stream, err := c.openStream(ctx)
		if err != nil {
			return nil, err
		}
		if err := c.socksHandshake(stream); err != nil {
			_ = stream.Close()
			return nil, err
		}
		if err := socksUDPAssociate(stream); err != nil {
			_ = stream.Close()
			return nil, err
		}
		assoc = &streamConn{Stream: stream}
		c.mu.Lock()
		c.udpAssoc = assoc
		c.mu.Unlock()
	}
	return &udpConn{client: c, target: target, assoc: assoc}, nil
}

func (c *H3Client) socksHandshake(stream *quic.Stream) error {
	auth, err := c.token()
	if err != nil {
		return err
	}
	uuid, token, err := proto.DecodeToken(auth)
	if err != nil {
		return err
	}
	return socksAuthenticate(stream, uuid, hex.EncodeToString(token))
}

// Close shuts the native QUIC transport.
func (c *H3Client) Close() error {
	c.mu.Lock()
	conn := c.conn
	assoc := c.udpAssoc
	quicTransport := c.quicConn
	socket := c.socket
	c.conn = nil
	c.udpAssoc = nil
	c.quicConn = nil
	c.socket = nil
	c.mu.Unlock()
	if assoc != nil {
		_ = assoc.Close()
	}
	if conn != nil {
		_ = conn.CloseWithError(0, "")
	}
	if quicTransport != nil {
		_ = quicTransport.Close()
	}
	if socket != nil {
		_ = socket.Close()
	}
	return nil
}

type streamConn struct {
	*quic.Stream
}

func (c *streamConn) LocalAddr() net.Addr                { return socksAddr("tianma-quic-local") }
func (c *streamConn) RemoteAddr() net.Addr               { return socksAddr("tianma-quic-remote") }
func (c *streamConn) SetDeadline(t time.Time) error      { return c.Stream.SetDeadline(t) }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.Stream.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.Stream.SetWriteDeadline(t) }

type udpConn struct {
	client *H3Client
	target string
	assoc  net.Conn
	mu     sync.Mutex
}

func (c *udpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.client.mu.Lock()
		conn := c.client.conn
		c.client.mu.Unlock()
		if conn == nil {
			return 0, nil, errors.New("tianma: quic connection closed")
		}
		data, err := conn.ReceiveDatagram(context.Background())
		if err != nil {
			return 0, nil, err
		}
		addr, payload, err := parseSocksUDP(data)
		if err != nil {
			continue
		}
		n := copy(p, payload)
		return n, addr, nil
	}
}

func (c *udpConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	target := addr.String()
	if target == "" || target == "<nil>" {
		target = c.target
	}
	data, err := buildSocksUDP(target, p)
	if err != nil {
		return 0, err
	}
	c.client.mu.Lock()
	conn := c.client.conn
	c.client.mu.Unlock()
	if conn == nil {
		return 0, errors.New("tianma: quic connection closed")
	}
	if err := conn.SendDatagram(data); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *udpConn) Close() error                     { return nil }
func (c *udpConn) LocalAddr() net.Addr              { return socksAddr("tianma-quic-local") }
func (c *udpConn) SetDeadline(time.Time) error      { return nil }
func (c *udpConn) SetReadDeadline(time.Time) error  { return nil }
func (c *udpConn) SetWriteDeadline(time.Time) error { return nil }
func (c *udpConn) RemoteAddr() net.Addr             { return socksAddr(c.target) }

func socksAddr(value string) net.Addr { return socksAddrValue(value) }

type socksAddrValue string

func (a socksAddrValue) Network() string { return "tianma" }
func (a socksAddrValue) String() string  { return string(a) }

func socksAuthenticate(w io.Writer, username, password string) error {
	if len(username) > 255 || len(password) > 255 {
		return errors.New("tianma: socks credential too long")
	}
	if _, err := w.Write([]byte{5, 1, 2}); err != nil {
		return err
	}
	var reply [2]byte
	if _, err := io.ReadFull(w.(io.Reader), reply[:]); err != nil {
		return err
	}
	if reply[0] != 5 || reply[1] != 2 {
		return errors.New("tianma: socks auth method rejected")
	}
	buf := make([]byte, 0, 3+len(username)+len(password))
	buf = append(buf, 1, byte(len(username)))
	buf = append(buf, username...)
	buf = append(buf, byte(len(password)))
	buf = append(buf, password...)
	if _, err := w.Write(buf); err != nil {
		return err
	}
	if _, err := io.ReadFull(w.(io.Reader), reply[:]); err != nil {
		return err
	}
	if reply[0] != 1 || reply[1] != 0 {
		return errors.New("tianma: socks authentication failed")
	}
	return nil
}

func socksConnect(w io.Writer, target string) error {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}
	req, err := socksRequest(1, host, port)
	if err != nil {
		return err
	}
	if _, err := w.Write(req); err != nil {
		return err
	}
	return readSocksReply(w.(io.Reader))
}

func socksUDPAssociate(w io.Writer) error {
	req, err := socksRequest(3, "0.0.0.0", 0)
	if err != nil {
		return err
	}
	if _, err := w.Write(req); err != nil {
		return err
	}
	return readSocksReply(w.(io.Reader))
}

func socksRequest(command byte, host string, port int) ([]byte, error) {
	ip := net.ParseIP(host)
	buf := []byte{5, command, 0}
	if ip4 := ip.To4(); ip4 != nil {
		buf = append(buf, 1)
		buf = append(buf, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		buf = append(buf, 4)
		buf = append(buf, ip6...)
	} else {
		if len(host) > 255 {
			return nil, errors.New("tianma: host too long")
		}
		buf = append(buf, 3, byte(len(host)))
		buf = append(buf, host...)
	}
	buf = append(buf, byte(port>>8), byte(port))
	return buf, nil
}

func readSocksReply(r io.Reader) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	if head[0] != 5 || head[1] != 0 {
		return fmt.Errorf("tianma: socks request failed: %d", head[1])
	}
	switch head[3] {
	case 1:
		_, err := io.ReadFull(r, make([]byte, 4+2))
		return err
	case 4:
		_, err := io.ReadFull(r, make([]byte, 16+2))
		return err
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return err
		}
		_, err := io.ReadFull(r, make([]byte, int(length[0])+2))
		return err
	default:
		return errors.New("tianma: invalid socks reply")
	}
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
	req, err := socksRequest(0, host, port)
	if err != nil {
		return nil, err
	}
	// SOCKS5 UDP header starts with RSV(2) + FRAG, then the request body
	// beginning at ATYP.
	out := make([]byte, 0, 3+len(req)-3+len(payload))
	out = append(out, 0, 0, 0)
	out = append(out, req[3:]...)
	out = append(out, payload...)
	return out, nil
}

func parseSocksUDP(data []byte) (net.Addr, []byte, error) {
	if len(data) < 4 {
		return nil, nil, errors.New("tianma: short socks udp")
	}
	atyp := data[3]
	offset := 4
	var host string
	switch atyp {
	case 1:
		if len(data) < offset+4+2 {
			return nil, nil, errors.New("tianma: short socks udp ipv4")
		}
		host = net.IP(data[offset : offset+4]).String()
		offset += 4
	case 4:
		if len(data) < offset+16+2 {
			return nil, nil, errors.New("tianma: short socks udp ipv6")
		}
		host = net.IP(data[offset : offset+16]).String()
		offset += 16
	case 3:
		if len(data) < offset+1 {
			return nil, nil, errors.New("tianma: short socks udp domain")
		}
		size := int(data[offset])
		offset++
		if len(data) < offset+size+2 {
			return nil, nil, errors.New("tianma: short socks udp domain")
		}
		host = string(data[offset : offset+size])
		offset += size
	default:
		return nil, nil, errors.New("tianma: invalid socks udp atyp")
	}
	port := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2
	return socksAddr(net.JoinHostPort(host, strconv.Itoa(int(port)))), data[offset:], nil
}

func hexToken(uuid string, token []byte) string {
	_ = uuid
	return hex.EncodeToString(token)
}
