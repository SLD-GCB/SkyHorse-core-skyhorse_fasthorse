package e2e

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"tianma/internal/client"
	"tianma/internal/policy"
	"tianma/internal/server"
)

// TestObfsRotation runs the tunnel while both sides rotate the Salamander key
// every second.
func TestObfsRotation(t *testing.T) {
	dir := t.TempDir()
	certificatePath, keyPath, pin := writeCertificate(t, dir)
	port := freeUDPPort(t)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	instance, err := server.New(server.Config{
		Listen:               "127.0.0.1",
		Port:                 port,
		CertificatePath:      certificatePath,
		KeyPath:              keyPath,
		ObfsPassword:         "rotation-master",
		ObfsRotationSeconds:  1,
		ObfsToleranceSeconds: 1,
		Users: []server.User{
			{Name: "alice", UUID: "alice-1", Password: "secret"},
		},
		Policy: policy.Config{AllowPrivate: true, AllowLoopback: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = instance.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)

	host, portText, _ := net.SplitHostPort(address)
	serverPort, _ := strconv.Atoi(portText)
	proxy, err := client.New(client.Config{
		Server:               host,
		ServerPort:           serverPort,
		ServerName:           "localhost",
		Pin:                  pin,
		UUID:                 "alice-1",
		Password:             "secret",
		ObfsPassword:         "rotation-master",
		ObfsRotationSeconds:  1,
		ObfsToleranceSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	callCtx, callCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer callCancel()

	for round := 0; round < 3; round++ {
		conn, err := proxy.Dial(callCtx, target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte("rotate")); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		response := make([]byte, 6)
		if _, err := io.ReadFull(conn, response); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		_ = conn.Close()
		time.Sleep(1100 * time.Millisecond)
	}
}
