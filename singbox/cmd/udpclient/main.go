// Command udpclient performs a standard SOCKS5 UDP ASSOCIATE against the local
// sing-box socks inbound and sends one datagram to the target.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	socksAddress := "127.0.0.1:21080"
	target := "127.0.0.1:28081"
	size := 8
	if len(os.Args) > 2 {
		socksAddress = os.Args[1]
		target = os.Args[2]
	}
	if len(os.Args) > 3 {
		parsed, err := strconv.Atoi(os.Args[3])
		if err != nil || parsed < 0 || parsed > 60000 {
			fail(fmt.Errorf("bad size %q", os.Args[3]))
		}
		size = parsed
	}

	payload := bytes.Repeat([]byte("x"), size)

	control, err := net.Dial("tcp", socksAddress)
	if err != nil {
		fail(err)
	}
	defer control.Close()

	// No-auth method negotiation.
	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		fail(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(control, method[:]); err != nil {
		fail(err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		fail(fmt.Errorf("server picked method %v", method))
	}

	// UDP ASSOCIATE, client address 0.0.0.0:0.
	if _, err := control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		fail(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(control, reply[:]); err != nil {
		fail(err)
	}
	if reply[1] != 0x00 {
		fail(fmt.Errorf("associate failed, rep=%d", reply[1]))
	}

	relayIP := net.IP(reply[4:8])
	if relayIP.IsUnspecified() {
		host, _, _ := net.SplitHostPort(socksAddress)
		relayIP = net.ParseIP(host)
	}
	relayPort := binary.BigEndian.Uint16(reply[8:10])
	relay := &net.UDPAddr{IP: relayIP, Port: int(relayPort)}

	socket, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		fail(err)
	}
	defer socket.Close()
	_ = socket.SetDeadline(time.Now().Add(8 * time.Second))

	destination, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		fail(err)
	}
	packet := encodeUDPDatagram(destination, payload)
	if _, err := socket.Write(packet); err != nil {
		fail(err)
	}

	buffer := make([]byte, 65535)
	n, err := socket.Read(buffer)
	if err != nil {
		fail(err)
	}
	received, err := decodeUDPDatagram(buffer[:n])
	if err != nil {
		fail(err)
	}
	if !bytes.Equal(received, payload) {
		fail(fmt.Errorf("echo mismatch: got %d bytes, want %d", len(received), len(payload)))
	}
	fmt.Printf("ok len=%d\n", len(received))
}

func encodeUDPDatagram(destination *net.UDPAddr, payload []byte) []byte {
	packet := []byte{0x00, 0x00, 0x00}
	ip := destination.IP.To4()
	if ip == nil {
		packet = append(packet, 0x04)
		packet = append(packet, destination.IP.To16()...)
	} else {
		packet = append(packet, 0x01)
		packet = append(packet, ip...)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(destination.Port))
	packet = append(packet, port[:]...)
	return append(packet, payload...)
}

func decodeUDPDatagram(packet []byte) ([]byte, error) {
	if len(packet) < 4 {
		return nil, fmt.Errorf("short reply")
	}
	offset := 3
	switch packet[offset] {
	case 0x01:
		offset += 1 + 4
	case 0x04:
		offset += 1 + 16
	case 0x03:
		length := int(packet[offset+1])
		offset += 2 + length
	default:
		return nil, fmt.Errorf("bad address type %d", packet[offset])
	}
	offset += 2 // port
	if offset > len(packet) {
		return nil, fmt.Errorf("truncated reply")
	}
	return packet[offset:], nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "udpclient:", err)
	os.Exit(1)
}
