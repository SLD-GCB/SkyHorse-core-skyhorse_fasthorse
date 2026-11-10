package e2e

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"

	core "tianma/boxapi"
)

// TestKernelPolicyBlocksLoopback proves the SSRF boundary: with the default
// policy a valid user cannot make the node dial the machine itself.
func TestKernelPolicyBlocksLoopback(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pinBase64 := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)

	config := fmt.Sprintf(`{
		"log": { "level": "error" },
		"inbounds": [{
			"type": "skyhorse",
			"tag": "sky-in",
			"listen": "127.0.0.1",
			"listen_port": %d,
			"users": [{ "name": "alice", "uuid": "alice-1", "password": "secret" }],
			"obfs_password": "kernel-test-obfs",
			"tls": { "enabled": true, "certificate_path": %q, "key_path": %q }
		}],
		"outbounds": [{ "type": "direct", "tag": "direct" }],
		"route": { "final": "direct" }
	}`, serverPort, certPath, keyPath)
	startKernel(t, config)

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

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if conn, err := instance.Dial(ctx, echo.Addr().String()); err == nil {
		_ = conn.Close()
		t.Fatal("the node dialed loopback despite the default policy")
	}
}
