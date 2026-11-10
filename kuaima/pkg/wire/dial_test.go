package wire

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"skyhorse.local/kuaima/pkg/gate"
	"skyhorse.local/kuaima/pkg/obfs"
)

func TestGateObfsTLS(t *testing.T) {
	cert, err := GenerateSelfSigned([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := ServerTLS(cert)
	clientTLS, err := ClientTLS("127.0.0.1", PinSPKI(cert.Leaf), false)
	if err != nil {
		t.Fatal(err)
	}

	obfsConfig := obfs.Config{Password: "obfs-secret", Rotation: time.Hour, Tolerance: time.Hour}
	gateConfig := gate.Config{Password: "obfs-secret"}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type result struct {
		user string
		err  error
	}
	serverDone := make(chan result, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			serverDone <- result{err: err}
			return
		}
		conn, user, err := Accept(raw, obfsConfig, gate.NewVerifier(gateConfig), serverTLS)
		if err != nil {
			serverDone <- result{err: err}
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
		serverDone <- result{user: user}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, ln.Addr().String(), obfsConfig, gateConfig, "alice", clientTLS)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
	_ = conn.Close()

	select {
	case res := <-serverDone:
		if res.err != nil {
			t.Fatalf("server: %v", res.err)
		}
		if res.user != "alice" {
			t.Fatalf("user = %q", res.user)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not finish")
	}
}

func TestGateWrongObfsPassword(t *testing.T) {
	cert, err := GenerateSelfSigned([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := ServerTLS(cert)
	clientTLS, err := ClientTLS("127.0.0.1", PinSPKI(cert.Leaf), false)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		_, _, err = Accept(raw, obfs.Config{Password: "right"}, gate.NewVerifier(gate.Config{Password: "right"}), serverTLS)
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = Dial(ctx, ln.Addr().String(), obfs.Config{Password: "wrong"}, gate.Config{Password: "wrong"}, "alice", clientTLS)
	if err == nil {
		t.Fatal("wrong obfs password accepted")
	}
	select {
	case err := <-serverErr:
		if err == nil {
			t.Fatal("server accepted wrong password")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not reject")
	}
}
