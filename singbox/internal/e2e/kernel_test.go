package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"

	skyhorsebox "skyhorse.local/skyhorse/singbox"
)

func startKernel(t *testing.T, config string) *box.Box {
	t.Helper()

	inboundRegistry := include.InboundRegistry()
	outboundRegistry := include.OutboundRegistry()
	skyhorsebox.RegisterInbound(inboundRegistry)
	skyhorsebox.RegisterOutbound(outboundRegistry)

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
	if err := sjson.UnmarshalContext(ctx, []byte(config), &options); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	instance, err := box.New(box.Options{Options: options, Context: ctx})
	if err != nil {
		t.Fatalf("build box: %v", err)
	}
	if err := instance.Start(); err != nil {
		t.Fatalf("start box: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func kernelCertificate(t *testing.T, dir string) (certificatePath, keyPath, pinBase64 string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "skyhorse-kernel"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)

	certificatePath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificatePath, keyPath, base64.StdEncoding.EncodeToString(pin[:])
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port
}

func serverConfig(port int, certPath, keyPath string) string {
	return fmt.Sprintf(`{
		"log": { "level": "error" },
		"inbounds": [{
			"type": "skyhorse",
			"tag": "sky-in",
			"listen": "127.0.0.1",
			"listen_port": %d,
			"users": [{ "name": "alice", "uuid": "alice-1", "password": "secret" }],
			"obfs_password": "kernel-test-obfs",
			"policy": { "allow_private": true, "allow_loopback": true },
			"tls": { "enabled": true, "certificate_path": %q, "key_path": %q }
		}],
		"outbounds": [{ "type": "direct", "tag": "direct" }],
		"route": { "final": "direct" }
	}`, port, certPath, keyPath)
}

func clientConfig(socksPort, serverPort int, pin string, password string) string {
	return fmt.Sprintf(`{
		"log": { "level": "error" },
		"inbounds": [{ "type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": %d }],
		"outbounds": [{
			"type": "skyhorse",
			"tag": "proxy",
			"server": "127.0.0.1",
			"server_port": %d,
			"uuid": "alice-1",
			"password": %q,
			"obfs_password": "kernel-test-obfs",
			"tls": {
				"enabled": true,
				"server_name": "localhost",
				"certificate_public_key_sha256": [%q]
			}
		}, { "type": "direct", "tag": "direct" }],
		"route": { "final": "proxy" }
	}`, socksPort, serverPort, password, pin)
}

func socksConnect(t *testing.T, socksAddress, target string) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", socksAddress, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if method[1] != 0x00 {
		conn.Close()
		t.Fatalf("socks method %v", method)
	}

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	request := []byte{0x05, 0x01, 0x00, 0x01}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		conn.Close()
		t.Fatalf("test target must be IPv4: %s", target)
	}
	request = append(request, ip...)
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	request = append(request, portBytes[:]...)
	if _, err := conn.Write(request); err != nil {
		conn.Close()
		t.Fatal(err)
	}

	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		conn.Close()
		t.Fatalf("socks connect rep=%d", reply[1])
	}
	return conn
}

func TestKernelTCP(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pin := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)
	socksPort := freeTCPPort(t)

	startKernel(t, serverConfig(serverPort, certPath, keyPath))
	startKernel(t, clientConfig(socksPort, serverPort, pin, "secret"))

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("kernel-ok"))
	}))
	defer target.Close()

	host, portText, _ := net.SplitHostPort(target.Listener.Addr().String())
	conn := socksConnect(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort)), net.JoinHostPort(host, portText))
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(body, "kernel-ok") {
		t.Fatalf("response = %q", body)
	}
}

func TestKernelUDP(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pin := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)
	socksPort := freeTCPPort(t)

	startKernel(t, serverConfig(serverPort, certPath, keyPath))
	startKernel(t, clientConfig(socksPort, serverPort, pin, "secret"))

	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, peer, err := echo.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(buffer[:n], peer)
		}
	}()

	control, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(control, method[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(control, reply[:]); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("udp associate rep=%d", reply[1])
	}

	relay := &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: int(binary.BigEndian.Uint16(reply[8:10])),
	}
	socket, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	_ = socket.SetDeadline(time.Now().Add(10 * time.Second))

	payload := []byte("kernel-udp")
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(echo.LocalAddr().(*net.UDPAddr).Port))
	packet = append(packet, portBytes[:]...)
	packet = append(packet, payload...)
	if _, err := socket.Write(packet); err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, 65535)
	n, err := socket.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 || string(buffer[10:n]) != string(payload) {
		t.Fatalf("udp reply = %v", buffer[:n])
	}
}

func TestKernelWrongPassword(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pin := kernelCertificate(t, dir)
	serverPort := freeUDPPort(t)
	socksPort := freeTCPPort(t)

	startKernel(t, serverConfig(serverPort, certPath, keyPath))
	startKernel(t, clientConfig(socksPort, serverPort, pin, "wrong"))

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("should-not-reach"))
	}))
	defer target.Close()

	host, portText, _ := net.SplitHostPort(target.Listener.Addr().String())
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		t.Fatal(err)
	}
	request := []byte{0x05, 0x01, 0x00, 0x01}
	request = append(request, net.ParseIP(host).To4()...)
	port, _ := strconv.Atoi(portText)
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	request = append(request, portBytes[:]...)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return
	}
	if reply[1] == 0x00 {
		t.Fatal("wrong password must not open a relay")
	}
}

func contains(haystack []byte, needle string) bool { return bytes.Contains(haystack, []byte(needle)) }