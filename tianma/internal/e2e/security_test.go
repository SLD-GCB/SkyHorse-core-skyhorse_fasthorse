package e2e

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	quic "github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"

	"tianma/internal/client"
	"tianma/internal/proto"
	"tianma/internal/wire"
)

func TestWrongPassword(t *testing.T) {
	fixture := newFixture(t, "obfs-security-key")
	defer fixture.stop()

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	config := fixture.clientConfig()
	config.Password = "wrong"
	instance, err := client.New(config)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := instance.Dial(ctx, target); err == nil {
		t.Fatal("a wrong password must not authenticate")
	}
}

func TestWrongPin(t *testing.T) {
	fixture := newFixture(t, "obfs-security-key")
	defer fixture.stop()

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	config := fixture.clientConfig()
	config.Pin = "0000000000000000000000000000000000000000000000000000000000000000"
	instance, err := client.New(config)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := instance.Dial(ctx, target); err == nil {
		t.Fatal("a wrong pin must not complete the TLS handshake")
	}
}

func TestWrongObfsPassword(t *testing.T) {
	fixture := newFixture(t, "obfs-right-key")
	defer fixture.stop()

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	config := fixture.clientConfig()
	config.ObfsPassword = "obfs-wrong-key"
	instance, err := client.New(config)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := instance.Dial(ctx, target); err == nil {
		t.Fatal("a wrong obfs key must not establish a QUIC connection")
	}
}

func TestUnauthenticatedProbe(t *testing.T) {
	fixture := newFixture(t, "")
	defer fixture.stop()

	pin, err := hex.DecodeString(fixture.pin)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := wire.ClientTLS("localhost", pin, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, fixture.address, tlsConfig, wire.QUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http3.Transport{
		TLSClientConfig: tlsConfig,
		QUICConfig:      wire.QUICConfig(),
		Dial: func(_ context.Context, _ string, _ *tls.Config, _ *quic.Config) (*quic.Conn, error) {
			return conn, nil
		},
	}
	defer transport.Close()

	for _, path := range []string{"/auth", "/", "/random"} {
		request := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Scheme: "https", Host: proto.URLHost, Path: path},
			Header: make(http.Header),
		}
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatalf("probe %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("probe %s returned %d, want 404", path, response.StatusCode)
		}
	}
}

func TestUnauthenticatedStream(t *testing.T) {
	fixture := newFixture(t, "")
	defer fixture.stop()

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	pin, err := hex.DecodeString(fixture.pin)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := wire.ClientTLS("localhost", pin, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, fixture.address, tlsConfig, wire.QUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	address, err := proto.ParseAddr(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.WriteTCPRequest(stream, address, 0); err != nil {
		t.Fatal(err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(2 * time.Second))

	var status [1]byte
	if _, err := io.ReadFull(stream, status[:]); err == nil {
		t.Fatalf("an unauthenticated stream was served, status=%d", status[0])
	}
}
