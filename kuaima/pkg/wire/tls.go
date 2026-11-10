// Package wire 提供快马的 TCP/TLS/H2 传输。
package wire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateSelfSigned 生成一张自签证书，hosts 写入 SAN。
func GenerateSelfSigned(hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "kuaima"},
		NotBefore:    time.Now().Add(-time.Hour),
		// Chromium 拒绝有效期超过 398 天的证书；这里取 397 天。
		NotAfter:              time.Now().AddDate(0, 0, 90),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}, nil
}

// PinSPKI 计算证书公钥的 SHA-256，base64 编码。
func PinSPKI(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// NormalizePin 接受 base64 或 hex，返回 base64。
func NormalizePin(pin string) string {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return ""
	}
	if raw, err := hex.DecodeString(strings.ReplaceAll(pin, ":", "")); err == nil && len(raw) == 32 {
		return base64.StdEncoding.EncodeToString(raw)
	}
	return pin
}

// ClientTLS 构造客户端 TLS 配置：有 pin 时校验 SPKI，否则 insecure 才允许。
func ClientTLS(serverName, pin string, insecure bool) (*tls.Config, error) {
	pin = NormalizePin(pin)
	if pin == "" && !insecure {
		return nil, fmt.Errorf("kuaima: pin or insecure is required")
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h2"},
		ServerName:         serverName,
		InsecureSkipVerify: true, // 手动校验
	}
	if pin == "" {
		return cfg, nil
	}
	expected, err := base64.StdEncoding.DecodeString(pin)
	if err != nil || len(expected) != 32 {
		return nil, fmt.Errorf("kuaima: bad pin")
	}
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("kuaima: no certificate")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return err
		}
		got := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		if !equalBytes(got[:], expected) {
			return fmt.Errorf("kuaima: certificate pin mismatch")
		}
		return nil
	}
	return cfg, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ServerTLS 构造服务端 TLS 配置。
func ServerTLS(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	}
}
