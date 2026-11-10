package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"

	core "tianma/boxapi"
)

// TestKernelMasquerade proves an unauthenticated visitor sees the configured
// site instead of the tunnel.
func TestKernelMasquerade(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, _ := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)

	config := fmt.Sprintf(`{
		"log": { "level": "error" },
		"inbounds": [{
			"type": "skyhorse",
			"tag": "sky-in",
			"listen": "127.0.0.1",
			"listen_port": %d,
			"users": [{ "name": "alice", "uuid": "alice-1", "password": "secret" }],
			"policy": { "allow_private": true, "allow_loopback": true },
			"masquerade": { "type": "string", "content": "site-ok" },
			"tls": { "enabled": true, "certificate_path": %q, "key_path": %q }
		}],
		"outbounds": [{ "type": "direct", "tag": "direct" }],
		"route": { "final": "direct" }
	}`, serverPort, certPath, keyPath)
	startKernel(t, config)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tlsConfig := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}
	conn, err := quic.DialAddr(ctx, fmt.Sprintf("127.0.0.1:%d", serverPort), tlsConfig, core.QUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")

	transport := &http3.Transport{
		TLSClientConfig: tlsConfig,
		QUICConfig:      core.QUICConfig(),
		Dial: func(_ context.Context, _ string, _ *tls.Config, _ *quic.Config) (*quic.Conn, error) {
			return conn, nil
		},
	}
	defer transport.Close()

	request, err := http.NewRequest(http.MethodGet, "https://tianma/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "site-ok" {
		t.Fatalf("got %d %q", response.StatusCode, body)
	}
}
