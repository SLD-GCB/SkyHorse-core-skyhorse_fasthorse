// Command gencert writes a self-signed certificate for local Horse v2 tests
// and prints the SPKI SHA-256 pin in the form the client config expects.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	certPath := flag.String("cert", "cert.pem", "certificate output path")
	keyPath := flag.String("key", "key.pem", "key output path")
	flag.Parse()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "horse2-local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
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

	if err := os.WriteFile(*certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		fatal(err)
	}

	fmt.Println(base64.StdEncoding.EncodeToString(pin[:]))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gencert:", err)
	os.Exit(1)
}
