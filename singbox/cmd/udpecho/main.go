// Command udpecho echoes UDP datagrams; it is the target for UDP tests.
package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	address := "127.0.0.1:28081"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}
	endpoint, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "udpecho:", err)
		os.Exit(1)
	}
	conn, err := net.ListenUDP("udp", endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "udpecho:", err)
		os.Exit(1)
	}
	defer conn.Close()

	buffer := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		_, _ = conn.WriteToUDP(buffer[:n], peer)
	}
}
