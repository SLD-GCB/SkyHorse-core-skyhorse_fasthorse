package e2e

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

)

// TestBrutalRateLimit checks that a 20 Mbit/s declaration actually caps the
// download instead of letting Cubic fill the loopback.
func TestBrutalRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput test skipped in -short mode")
	}
	fixture := newFixtureWithBandwidth(t, "obfs-brutal-key", 20, 20)
	defer fixture.stop()

	const size = 8 << 20
	target, stopTarget := startTCPDownload(t, size)
	defer stopTarget()

	instance := newClient(t, fixture.clientConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := instance.Dial(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	written, err := io.Copy(io.Discard, conn)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	throughput := float64(written) * 8 / elapsed.Seconds() / 1e6

	t.Logf("brutal 20 Mbit/s: %d bytes in %v, %.1f Mbit/s", written, elapsed, throughput)
	if written != size {
		t.Fatalf("downloaded %d bytes, want %d", written, size)
	}
	if throughput > 60 {
		t.Fatalf("throughput %.1f Mbit/s, the 20 Mbit/s cap did not apply", throughput)
	}
}

// TestBrutalDatagram makes sure the rate change does not break datagrams.
func TestBrutalDatagram(t *testing.T) {
	fixture := newFixtureWithBandwidth(t, "obfs-brutal-key", 20, 20)
	defer fixture.stop()

	target, stopTarget := startUDPEcho(t)
	defer stopTarget()

	instance := newClient(t, fixture.clientConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	udp, err := instance.DialUDP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	destination, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := udp.WriteTo([]byte("cc"), destination); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	done := make(chan struct{})
	var n int
	var readErr error
	go func() {
		n, _, readErr = udp.ReadFrom(buffer)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the datagram echo")
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if n != 2 {
		t.Fatalf("echoed %d bytes", n)
	}
}

