package e2e

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"tianma/internal/client"
)

func newClient(t *testing.T, config client.Config) *client.Client {
	t.Helper()
	instance, err := client.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestTCPDownloadThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput test skipped in -short mode")
	}
	fixture := newFixture(t, "obfs-perf-key")
	defer fixture.stop()

	const size = 64 << 20
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
	throughput := float64(written) / elapsed.Seconds() / (1 << 20)

	t.Logf("download: %d bytes in %v, %.1f MiB/s", written, elapsed, throughput)
	if written != size {
		t.Fatalf("downloaded %d bytes, want %d", written, size)
	}
	if throughput < 10 {
		t.Fatalf("throughput %.1f MiB/s is below the 10 MiB/s floor", throughput)
	}
}

func TestTCPConcurrentThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput test skipped in -short mode")
	}
	fixture := newFixture(t, "obfs-perf-key")
	defer fixture.stop()

	const streams = 8
	const size = 8 << 20
	target, stopTarget := startTCPDownload(t, size)
	defer stopTarget()

	instance := newClient(t, fixture.clientConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	var wait sync.WaitGroup
	errs := make(chan error, streams)
	for index := 0; index < streams; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			conn, err := instance.Dial(ctx, target)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			if _, err := io.Copy(io.Discard, conn); err != nil {
				errs <- err
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	aggregate := float64(streams*size) / elapsed.Seconds() / (1 << 20)
	t.Logf("concurrent: %d streams x %d MiB in %v, %.1f MiB/s aggregate", streams, size>>20, elapsed, aggregate)
}

func TestTCPLatency(t *testing.T) {
	fixture := newFixture(t, "obfs-latency-key")
	defer fixture.stop()

	target, stopTarget := startTCPEcho(t)
	defer stopTarget()

	instance := newClient(t, fixture.clientConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const rounds = 100
	start := time.Now()
	payload := []byte{0x42}
	response := make([]byte, 1)
	for index := 0; index < rounds; index++ {
		conn, err := instance.Dial(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(payload); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, response); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	elapsed := time.Since(start)
	t.Logf("latency: %d dial+echo round trips in %v, %.2f ms each", rounds, elapsed, float64(elapsed.Milliseconds())/rounds)
}

func TestUDPThroughput(t *testing.T) {
	fixture := newFixture(t, "obfs-udp-key")
	defer fixture.stop()

	target, stopTarget := startUDPEcho(t)
	defer stopTarget()

	instance := newClient(t, fixture.clientConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	payload := make([]byte, 1000)
	buffer := make([]byte, 65535)
	const rounds = 300

	start := time.Now()
	for index := 0; index < rounds; index++ {
		if _, err := udp.WriteTo(payload, destination); err != nil {
			t.Fatal(err)
		}
		if _, _, err := udp.ReadFrom(buffer); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	packetsPerSecond := float64(rounds) / elapsed.Seconds()
	throughput := float64(rounds*len(payload)) / elapsed.Seconds() / (1 << 20)
	t.Logf("udp: %d datagrams in %v, %.0f pps, %.1f MiB/s", rounds, elapsed, packetsPerSecond, throughput)
}
