package e2e

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net"
	"testing"
	"time"

	core "tianma/boxapi"
)

// TestKernelUDPFragmented proves the kernel inbound reassembles a datagram that
// was split across several QUIC datagram frames.
func TestKernelUDPFragmented(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pinBase64 := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)

	startKernel(t, serverConfig(serverPort, certPath, keyPath))

	pinBytes, err := base64.StdEncoding.DecodeString(pinBase64)
	if err != nil {
		t.Fatal(err)
	}

	instance, err := core.NewClient(core.Config{
		Server:       "127.0.0.1",
		ServerPort:   serverPort,
		ServerName:   "localhost",
		Pin:          hex.EncodeToString(pinBytes),
		UUID:         "alice-1",
		Password:     "secret",
		ObfsPassword: "kernel-test-obfs",
	})
	if err != nil {
		t.Fatal(err)
	}

	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, peer, err := echo.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(buffer[:n], peer)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	udp, err := instance.DialUDP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	destination := echo.LocalAddr().(*net.UDPAddr)
	payload := make([]byte, 4000)
	for index := range payload {
		payload[index] = 'x'
	}
	if _, err := udp.WriteTo(payload, destination); err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, 65535)
	done := make(chan struct{})
	var n int
	var readErr error
	go func() {
		n, _, readErr = udp.ReadFrom(buffer)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the fragmented echo")
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if n != len(payload) {
		t.Fatalf("echoed %d bytes, want %d", n, len(payload))
	}
}
