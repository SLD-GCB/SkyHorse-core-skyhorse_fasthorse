package wire

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

type userCtxKey struct{}

// WithUser 把动态门校验出的用户名放进 context。
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userCtxKey{}, user)
}

// UserFromContext 取出用户名。
func UserFromContext(ctx context.Context) string {
	user, _ := ctx.Value(userCtxKey{}).(string)
	return user
}

// Server 是 TCP + TLS + H2 的 CONNECT 服务端。
type Server struct {
	tlsConfig *tls.Config
	h2        *http2.Server
	// Authorize 在拨号前调用；返回非 nil 表示拒绝。
	Authorize func(ctx context.Context, r *http.Request) error
	// OnStream 在每条 CONNECT 流建立前申请配额，返回释放函数。
	OnStream func(ctx context.Context) (func(), error)
	// MaxStreams 限制单连接并发流，0 表示默认。
	MaxStreams uint32
	// Route 在 CONNECT 流上接管转发；为 nil 时直接拨号。
	Route func(ctx context.Context, conn net.Conn, authority string) error
	// RouteUDP 处理带 UDP 标记的 CONNECT 流。
	RouteUDP func(ctx context.Context, conn net.Conn, authority string) error
}

// NewServer 创建 H2 服务端。
func NewServer(tlsConfig *tls.Config) *Server {
	return &Server{tlsConfig: tlsConfig, h2: &http2.Server{}}
}

func h2Config(maxStreams uint32) *http.HTTP2Config {
	return &http.HTTP2Config{
		MaxConcurrentStreams:          int(maxStreams),
		MaxReceiveBufferPerConnection: 3 << 20,
		MaxReceiveBufferPerStream:     3 << 20,
		MaxReadFrameSize:              1 << 20,
	}
}

// ServeConn 在一条已经完成 TLS 握手的连接上提供 H2 服务。
func (s *Server) ServeConn(conn net.Conn) {
	s.ServeConnContext(context.Background(), conn)
}

// ServeConnContext 带用户上下文运行 H2。
func (s *Server) ServeConnContext(ctx context.Context, conn net.Conn) {
	s.h2.ServeConn(conn, &http2.ServeConnOpts{
		Context:    ctx,
		Handler:    http.HandlerFunc(s.handle),
		BaseConfig: &http.Server{HTTP2: h2Config(s.MaxStreams)},
	})
}

// Serve 在 listener 上提供服务。
func (s *Server) Serve(ln net.Listener) error {
	httpServer := &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 10 * time.Second,
		HTTP2:             h2Config(s.MaxStreams),
	}
	if err := http2.ConfigureServer(httpServer, s.h2); err != nil {
		return err
	}
	return httpServer.Serve(tls.NewListener(ln, s.tlsConfig))
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		panic(http.ErrAbortHandler)
	}
	authority := r.Header.Get("-connect-authority")
	if authority == "" {
		authority = r.Host
	}
	if authority == "" {
		authority = r.URL.Host
	}
	if s.Authorize != nil {
		if err := s.Authorize(r.Context(), r); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if s.OnStream != nil {
		release, err := s.OnStream(r.Context())
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		defer release()
	}
	if s.RouteUDP != nil && r.Header.Get("X-Kuaima-UDP") == "1" {
		w.WriteHeader(http.StatusOK)
		flusher, canFlush := w.(http.Flusher)
		if canFlush {
			flusher.Flush()
		}
		stream := &streamConn{
			reader:  r.Body,
			writer:  &flushWriter{w: w, f: flusher},
			closeFn: func() { _ = r.Body.Close() },
		}
		_ = s.RouteUDP(r.Context(), stream, authority)
		return
	}
	if s.Route != nil {
		w.WriteHeader(http.StatusOK)
		flusher, canFlush := w.(http.Flusher)
		if canFlush {
			flusher.Flush()
		}
		stream := &streamConn{
			reader:  r.Body,
			writer:  &flushWriter{w: w, f: flusher},
			closeFn: func() { _ = r.Body.Close() },
		}
		_ = s.Route(r.Context(), stream, authority)
		return
	}
	target, err := net.DialTimeout("tcp", authority, 10*time.Second)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer target.Close()

	go func() {
		buffer := make([]byte, 64<<10)
		_, _ = io.CopyBuffer(target, r.Body, buffer)
		if tcp, ok := target.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()

	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}
	// HTTP/1 需要显式开启全双工；HTTP/2 下该调用可能返回不支持，忽略即可。
	_ = http.NewResponseController(w).EnableFullDuplex()

	writer := &flushWriter{w: w, f: flusher}
	buffer := make([]byte, 64<<10)
	_, _ = io.CopyBuffer(writer, target, buffer)
	// 不等上行协程：CONNECT 的请求体可能一直开着，等它会和客户端
	// 等响应结束互相死锁。关闭目标后直接返回，让流结束。
	_ = target.Close()
}

// flushWriter 每次写入后立即 Flush，CONNECT 的响应体才会及时变成 DATA 帧。
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.f != nil {
		f.f.Flush()
	}
	return n, err
}

type Client struct {
	transport *http2.Transport
}

// NewClient 创建 H2 客户端。
func NewClient(tlsConfig *tls.Config) *Client {
	return &Client{
		transport: &http2.Transport{
			TLSClientConfig: tlsConfig,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: 10 * time.Second}
				raw, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				conn := tls.Client(raw, cfg)
				if err := conn.HandshakeContext(ctx); err != nil {
					_ = raw.Close()
					return nil, err
				}
				return conn, nil
			},
		},
	}
}

// Stream 是一条 CONNECT 双向流。
type Stream struct {
	body   io.ReadCloser
	writer *io.PipeWriter
	resp   *http.Response
	once   sync.Once
}

// Connect 打开到 authority 的 CONNECT 流。
func (c *Client) Connect(ctx context.Context, urlBase, authority string, header http.Header) (*Stream, error) {
	pr, pw := io.Pipe()
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Scheme: "https", Host: urlBase, Path: "/"},
		Host:   authority,
		Header: header,
		Body:   pr,
	}
	resp, err := c.transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		_ = resp.Body.Close()
		return nil, fmt.Errorf("kuaima: connect failed: HTTP %d", resp.StatusCode)
	}
	return &Stream{body: resp.Body, writer: pw, resp: resp}, nil
}

func (s *Stream) Read(p []byte) (int, error)  { return s.body.Read(p) }
func (s *Stream) Write(p []byte) (int, error) { return s.writer.Write(p) }

func (s *Stream) Close() error {
	var err error
	s.once.Do(func() {
		_ = s.writer.Close()
		if s.resp != nil {
			err = s.resp.Body.Close()
		}
	})
	return err
}

// SetDeadline 兼容 net.Conn 的接口；HTTP/2 流不支持单独 deadline。
func (s *Stream) SetDeadline(time.Time) error      { return nil }
func (s *Stream) SetReadDeadline(time.Time) error  { return nil }
func (s *Stream) SetWriteDeadline(time.Time) error { return nil }
