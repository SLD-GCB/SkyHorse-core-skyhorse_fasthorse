package client

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/posener/h2conn"
)

// DialUDP 打开一条带 UDP 标记的 H2 CONNECT 流。
func (c *Client) DialUDP(ctx context.Context, target string) (net.PacketConn, error) {
	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(c.cfg.Username+":"+c.cfg.Password))
	h2 := &h2conn.Client{
		Method: "CONNECT",
		Header: http.Header{
			"-connect-authority":  {target},
			"Proxy-Authorization": {authorization},
			"X-Kuaima-UDP":        {"1"},
		},
		Client: &http.Client{Transport: c.transport},
	}
	conn, resp, err := h2.Connect(ctx, "https://"+net.JoinHostPort(c.cfg.Server, fmt.Sprint(c.cfg.Port)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("kuaima: udp connect failed: HTTP %d", resp.StatusCode)
	}
	return &udpConn{
		conn:   &streamConn{Conn: conn, local: dummyAddr("kuaima-udp-local"), remote: dummyAddr(target)},
		target: dummyAddr(target),
	}, nil
}

// udpConn 把 H2 字节流封装成数据报连接。
type udpConn struct {
	conn   net.Conn
	target net.Addr
}

func (c *udpConn) Read(p []byte) (int, error)  { return readDatagram(c.conn, p) }
func (c *udpConn) Write(p []byte) (int, error) { return writeDatagram(c.conn, p) }

func (c *udpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := readDatagram(c.conn, p)
	return n, c.target, err
}

func (c *udpConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return writeDatagram(c.conn, p)
}

func (c *udpConn) Close() error                       { return c.conn.Close() }
func (c *udpConn) LocalAddr() net.Addr                { return dummyAddr("kuaima-udp-local") }
func (c *udpConn) RemoteAddr() net.Addr               { return c.target }
func (c *udpConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *udpConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *udpConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

func writeDatagram(w io.Writer, p []byte) (int, error) {
	if len(p) > 65535 {
		return 0, fmt.Errorf("kuaima: udp datagram too large")
	}
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(p)))
	if _, err := w.Write(header[:]); err != nil {
		return 0, err
	}
	if _, err := w.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func readDatagram(r io.Reader, p []byte) (int, error) {
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
