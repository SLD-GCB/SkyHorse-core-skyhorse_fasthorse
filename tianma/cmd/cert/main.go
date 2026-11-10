// Command cert writes a self signed certificate and prints the SPKI pin.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	certificatePath := flag.String("cert", "cert.pem", "certificate output")
	keyPath := flag.String("key", "key.pem", "key output")
	flag.Parse()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tianma-local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		fatal(err)
	}
	pin := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)

	if err := os.WriteFile(*certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		fatal(err)
	}
	fmt.Println(hex.EncodeToString(pin[:]))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cert:", err)
	os.Exit(1)
}
