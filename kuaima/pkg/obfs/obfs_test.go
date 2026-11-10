package obfs

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func roundTrip(t *testing.T, cfg Config, payload []byte) {
	t.Helper()
	var wire bytes.Buffer
	writer := NewWriter(&wire, cfg)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	reader := NewReader(&wire, cfg)
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestRoundTripSingleChunk(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{
		Password:  "secret",
		Rotation:  time.Hour,
		Tolerance: time.Hour,
		Now:       func() time.Time { return now },
	}
	roundTrip(t, cfg, []byte("hello kuaima"))
}

func TestRoundTripMultiChunk(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{
		Password:  "secret",
		Rotation:  time.Hour,
		Tolerance: time.Hour,
		Now:       func() time.Time { return now },
	}
	payload := bytes.Repeat([]byte("abcdefghij"), 5000) // 50 KB
	roundTrip(t, cfg, payload)
}

func TestRotationAcrossWindows(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := Config{
		Password:  "secret",
		Rotation:  time.Hour,
		Tolerance: 2 * time.Hour,
		Now:       func() time.Time { return now },
	}
	var wire bytes.Buffer
	writer := NewWriter(&wire, cfg)
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := writer.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}

	reader := NewReader(&wire, cfg)
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "firstsecond" {
		t.Fatalf("got %q", got)
	}
}

func TestOldWindowRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	writeCfg := Config{
		Password:  "secret",
		Rotation:  time.Hour,
		Tolerance: time.Hour,
		Now:       func() time.Time { return now },
	}
	var wire bytes.Buffer
	writer := NewWriter(&wire, writeCfg)
	if _, err := writer.Write([]byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}

	readCfg := writeCfg
	readCfg.Now = func() time.Time { return now.Add(3 * time.Hour) }
	reader := NewReader(&wire, readCfg)
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrBadEpoch) {
		t.Fatalf("err = %v, want ErrBadEpoch", err)
	}
}

func TestCorruptLength(t *testing.T) {
	cfg := Config{Password: "secret"}
	var wire bytes.Buffer
	// keyID(1) + epoch(4) + salt(8) + dataLen(2) + padLen(2)，数据长度超过 MaxChunk。
	wire.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0})
	reader := NewReader(&wire, cfg)
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
}
