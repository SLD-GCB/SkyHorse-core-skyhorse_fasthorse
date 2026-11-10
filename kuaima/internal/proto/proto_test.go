package proto

import (
	"bytes"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	token := bytes.Repeat([]byte{0xab}, TokenSize)
	value := EncodeToken("alice", token)
	name, got, err := DecodeToken(value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != "alice" || !bytes.Equal(got, token) {
		t.Fatalf("name=%q token=%x", name, got)
	}
}

func TestTokenWithoutScheme(t *testing.T) {
	token := bytes.Repeat([]byte{0x01}, TokenSize)
	name, got, err := DecodeToken("bob:" + EncodeToken("x", token)[len("Kuaima x:"):])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != "bob" || !bytes.Equal(got, token) {
		t.Fatalf("name=%q token=%x", name, got)
	}
}

func TestMalformedToken(t *testing.T) {
	cases := []string{"", "Kuaima", "Kuaima alice", "alice:zz", "alice:0011"}
	for _, value := range cases {
		if _, _, err := DecodeToken(value); err == nil {
			t.Fatalf("expected error for %q", value)
		}
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port uint16
	}{
		{"example.com:443", "example.com", 443},
		{"1.2.3.4:80", "1.2.3.4", 80},
		{"[::1]:8080", "::1", 8080},
	}
	for _, c := range cases {
		host, port, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("parse %q: %v", c.in, err)
		}
		if host != c.host || port != c.port {
			t.Fatalf("parse %q = %s:%d", c.in, host, port)
		}
	}
	for _, bad := range []string{"", "example.com", "example.com:0", "example.com:99999"} {
		if _, _, err := ParseTarget(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
