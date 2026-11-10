// Command kuaima-bench 通过快马连接池测单流/多流吞吐。
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"skyhorse.local/kuaima/internal/config"
	"skyhorse.local/kuaima/pkg/pool"
)

func main() {
	path := flag.String("c", "client.json", "client config")
	rawURL := flag.String("url", "", "http URL to download")
	streams := flag.Int("streams", 1, "concurrent streams")
	seconds := flag.Int("seconds", 20, "test duration")
	flag.Parse()

	if *rawURL == "" {
		log.Fatal("url is required")
	}
	target, err := url.Parse(*rawURL)
	if err != nil {
		log.Fatal(err)
	}
	port := target.Port()
	if port == "" {
		port = "80"
	}
	address := target.Hostname() + ":" + port
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target.RequestURI(), target.Host)

	cfg, err := config.LoadClient(*path)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*seconds)*time.Second)
	defer cancel()
	p, err := pool.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var total atomic.Int64
	var failed atomic.Int64
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := p.DialContext(ctx, address)
			if err != nil {
				failed.Add(1)
				return
			}
			defer conn.Close()
			if _, err := conn.Write([]byte(request)); err != nil {
				failed.Add(1)
				return
			}
			reader := bufio.NewReaderSize(conn, 64<<10)
			// 跳过响应头。
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == "\r\n" {
					break
				}
			}
			buffer := make([]byte, 64<<10)
			for {
				n, err := reader.Read(buffer)
				if n > 0 {
					total.Add(int64(n))
				}
				if err != nil {
					if err != io.EOF && ctx.Err() == nil {
						failed.Add(1)
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	bytes := total.Load()
	mbps := float64(bytes) * 8 / elapsed.Seconds() / 1_000_000
	fmt.Printf("streams=%d bytes=%d elapsed=%s throughput=%.2f Mbps failed=%d\n",
		*streams, bytes, elapsed.Round(time.Millisecond), mbps, failed.Load())
	for _, line := range p.Stats() {
		fmt.Fprintln(os.Stdout, "  "+strings.TrimSpace(line))
	}
}
