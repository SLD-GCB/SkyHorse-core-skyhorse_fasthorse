package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"tianma/internal/client"
	"tianma/internal/policy"
	"tianma/internal/server"
)

type fixture struct {
	address string
	pin     string
	obfs    string
	upMbps  int
	downMbps int
	obfsRotation  int
	obfsTolerance int
	stop    func()
}

func newFixture(t *testing.T, obfs string) *fixture {
	return newFixtureWithBandwidth(t, obfs, 0, 0)
}



func newFixtureWithBandwidth(t *testing.T, obfs string, upMbps, downMbps int) *fixture {
	t.Helper()

	dir := t.TempDir()
	certificatePath, keyPath, pin := writeCertificate(t, dir)
	port := freeUDPPort(t)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	instance, err := server.New(server.Config{
		Listen:          "127.0.0.1",
		Port:            port,
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
		ObfsPassword:    obfs,
		UpMbps:          upMbps,
		DownMbps:        downMbps,
		Users: []server.User{
			{Name: "alice", UUID: "alice-1", Password: "secret"},
		},
		Policy: policy.Config{AllowPrivate: true, AllowLoopback: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = instance.Run(ctx)
	}()
	time.Sleep(150 * time.Millisecond)

	return &fixture{address: address, pin: pin, obfs: obfs, upMbps: upMbps, downMbps: downMbps, stop: cancel}
}

func (f *fixture) clientConfig() client.Config {
	host, portText, _ := net.SplitHostPort(f.address)
	port, _ := strconv.Atoi(portText)
	return client.Config{
		Server:       host,
		ServerPort:   port,
		ServerName:   "localhost",
		Pin:          f.pin,
		UUID:         "alice-1",
		Password:     "secret",
		ObfsPassword: f.obfs,
		UpMbps:       f.upMbps,
		DownMbps:     f.downMbps,
	}
}

func writeCertificate(t *testing.T, dir string) (certificatePath, keyPath, pin string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tianma-test"},
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
	sum := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)

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
	return certificatePath, keyPath, hex.EncodeToString(sum[:])
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

// startTCPDownload accepts connections and writes size bytes to each.
func startTCPDownload(t *testing.T, size int64) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buffer := make([]byte, 1<<20)
				remaining := size
				for remaining > 0 {
					chunk := int64(len(buffer))
					if chunk > remaining {
						chunk = remaining
					}
					if _, err := conn.Write(buffer[:chunk]); err != nil {
						return
					}
					remaining -= chunk
				}
			}(conn)
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}

// startTCPEcho echoes every byte back.
func startTCPEcho(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(conn)
		}
	}()
	return listener.Addr().String(), func() { _ = listener.Close() }
}

// startUDPEcho echoes datagrams.
func startUDPEcho(t *testing.T) (string, func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, peer, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buffer[:n], peer)
		}
	}()
	return conn.LocalAddr().String(), func() { _ = conn.Close() }
}
