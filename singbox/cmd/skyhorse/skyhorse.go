// Command SkyHorse is a sing-box kernel with the SkyHorse protocol built in.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"

	skyhorsebox "skyhorse.local/skyhorse/singbox"
)

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "version" {
			fmt.Println("SkyHorse " + C.Version + "-skyhorse")
			return
		}
	}

	configPath, checkOnly := parseArgs(args)

	content, err := os.ReadFile(configPath)
	if err != nil {
		fatal(err)
	}

	inboundRegistry := include.InboundRegistry()
	outboundRegistry := include.OutboundRegistry()
	skyhorsebox.RegisterInbound(inboundRegistry)
	skyhorsebox.RegisterOutbound(outboundRegistry)
	skyhorsebox.RegisterKuaimaInbound(inboundRegistry)
	skyhorsebox.RegisterKuaimaOutbound(outboundRegistry)

	ctx := box.Context(
		context.Background(),
		inboundRegistry,
		outboundRegistry,
		include.EndpointRegistry(),
		include.DNSTransportRegistry(),
		include.ServiceRegistry(),
		include.CertificateProviderRegistry(),
	)

	var options option.Options
	if err := sjson.UnmarshalContext(ctx, content, &options); err != nil {
		fatal(err)
	}

	instance, err := box.New(box.Options{Options: options, Context: ctx})
	if err != nil {
		fatal(err)
	}

	if checkOnly {
		fmt.Println("SkyHorse: config OK")
		_ = instance.Close()
		return
	}

	if err := instance.Start(); err != nil {
		fatal(err)
	}
	fmt.Println("SkyHorse: core started")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals

	if err := instance.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "SkyHorse:", err)
	}
}

// parseArgs accepts the stock sing-box command line.
func parseArgs(args []string) (configPath string, checkOnly bool) {
	configPath = "config.json"
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "run":
		case "check", "-check", "--check":
			checkOnly = true
		case "-c", "--config":
			if index+1 < len(args) {
				configPath = args[index+1]
				index++
			}
		default:
			_ = flag.CommandLine
		}
	}
	return configPath, checkOnly
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "SkyHorse:", err)
	os.Exit(1)
}
