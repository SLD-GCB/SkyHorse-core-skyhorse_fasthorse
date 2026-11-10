package gate

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func seal(t *testing.T, cfg Config, user string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Seal(&buf, cfg, user); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if buf.Len() != RecordSize {
		t.Fatalf("record size = %d", buf.Len())
	}
	return buf.Bytes()
}

func TestSealVerify(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")

	verifier := NewVerifier(cfg)
	user, err := verifier.Verify(bytes.NewReader(record))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if user != "alice" {
		t.Fatalf("user = %q", user)
	}
}

func TestReplayRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")
	verifier := NewVerifier(cfg)

	if _, err := verifier.Verify(bytes.NewReader(record)); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := verifier.Verify(bytes.NewReader(record)); !errors.Is(err, ErrReplay) {
		t.Fatalf("second verify = %v, want ErrReplay", err)
	}
}

func TestExpiredRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")

	later := now.Add(2 * time.Minute)
	verifier := NewVerifier(Config{Password: "secret", Now: func() time.Time { return later }})
	if _, err := verifier.Verify(bytes.NewReader(record)); !errors.Is(err, ErrExpired) {
		t.Fatalf("verify = %v, want ErrExpired", err)
	}
}

func TestTamperedRecordRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")
	record[26] = 'b' // 改用户名

	verifier := NewVerifier(cfg)
	if _, err := verifier.Verify(bytes.NewReader(record)); !errors.Is(err, ErrAuth) {
		t.Fatalf("verify = %v, want ErrAuth", err)
	}
}

func TestWrongPasswordRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")

	other := Config{Password: "other", Now: func() time.Time { return now }}
	verifier := NewVerifier(other)
	if _, err := verifier.Verify(bytes.NewReader(record)); !errors.Is(err, ErrAuth) {
		t.Fatalf("verify = %v, want ErrAuth", err)
	}
}

func TestWrongNodeRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{Password: "secret", NodeID: "node-a", Now: func() time.Time { return now }}
	record := seal(t, cfg, "alice")

	other := Config{Password: "secret", NodeID: "node-b", Now: func() time.Time { return now }}
	verifier := NewVerifier(other)
	if _, err := verifier.Verify(bytes.NewReader(record)); !errors.Is(err, ErrAuth) {
		t.Fatalf("verify = %v, want ErrAuth", err)
	}
}

func TestBadUsername(t *testing.T) {
	cfg := Config{Password: "secret"}
	if err := Seal(&bytes.Buffer{}, cfg, ""); err == nil {
		t.Fatal("empty username accepted")
	}
	long := make([]byte, MaxUser+1)
	if err := Seal(&bytes.Buffer{}, cfg, string(long)); err == nil {
		t.Fatal("long username accepted")
	}
}
