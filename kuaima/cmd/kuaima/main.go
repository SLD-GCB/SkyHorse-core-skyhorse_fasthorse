package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"skyhorse.local/kuaima/internal/config"
	"skyhorse.local/kuaima/internal/server"
	"skyhorse.local/kuaima/internal/socks"
	"skyhorse.local/kuaima/pkg/pool"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "client":
		runClient(os.Args[2:])
	case "version":
		fmt.Println("kuaima 0.1.0")
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  kuaima serve  -c server.json")
	fmt.Fprintln(os.Stderr, "  kuaima client -c client.json")
	fmt.Fprintln(os.Stderr, "  kuaima version")
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runServe(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	path := flags.String("c", "server.json", "config path")
	_ = flags.Parse(args)

	cfg, err := config.LoadServer(*path)
	if err != nil {
		log.Fatal(err)
	}
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signalContext()
	defer cancel()
	go watchConfig(ctx, *path, srv)
	log.Printf("kuaima server listening on %s ports %s", cfg.Listen, cfg.PortRange)
	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func watchConfig(ctx context.Context, path string, srv *server.Server) {
	var last time.Time
	if info, err := os.Stat(path); err == nil {
		last = info.ModTime()
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil || !info.ModTime().After(last) {
				continue
			}
			cfg, err := config.LoadServer(path)
			if err != nil {
				log.Printf("reload config: %v", err)
				continue
			}
			srv.Reload(cfg)
			last = info.ModTime()
			log.Printf("config reloaded")
		}
	}
}

func runClient(args []string) {
	flags := flag.NewFlagSet("client", flag.ExitOnError)
	path := flags.String("c", "client.json", "config path")
	_ = flags.Parse(args)

	cfg, err := config.LoadClient(*path)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signalContext()
	defer cancel()

	p, err := pool.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("kuaima client socks5 on %s, %d connections", cfg.Listen, len(p.Stats()))
	if err := socks.Serve(ctx, ln, p.DialContext); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
