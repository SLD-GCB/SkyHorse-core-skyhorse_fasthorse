// Package socks 提供最小 SOCKS5 CONNECT 入口，供快马客户端本地使用。
package socks

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
)

// Dialer 建立到目标的连接。
type Dialer func(ctx context.Context, target string) (net.Conn, error)

// Serve 在 listener 上提供 SOCKS5。
func Serve(ctx context.Context, ln net.Listener, dial Dialer) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(ctx, conn, dial)
	}
}

func handle(ctx context.Context, conn net.Conn, dial Dialer) {
	defer conn.Close()
	if err := handshake(conn); err != nil {
		return
	}
	target, err := readRequest(conn)
	if err != nil {
		return
	}
	upstream, err := dial(ctx, target)
	if err != nil {
		writeReply(conn, 0x05)
		return
	}
	defer upstream.Close()
	if err := writeReply(conn, 0x00); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, conn)
		if cw, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, upstream)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	wg.Wait()
}

func handshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("socks: bad version")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

func readRequest(conn net.Conn) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 || header[1] != 0x01 {
		return "", fmt.Errorf("socks: only CONNECT is supported")
	}
	var host string
	switch header[3] {
	case 0x01:
		address := make([]byte, 4)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", err
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = string(address)
	case 0x04:
		address := make([]byte, 16)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	default:
		return "", fmt.Errorf("socks: bad address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func writeReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}
