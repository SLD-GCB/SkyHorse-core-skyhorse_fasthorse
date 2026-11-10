// Package obfs 实现快马的 TCP 流混淆：主密码派生时间窗口密钥，
// 每个块自带 epoch，长连接可以跨窗口继续。
package obfs

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/chacha20"
)

const (
	// MaxChunk 单个明文块上限。
	MaxChunk = 16 << 10
	// headerSize keyID(1) + epoch(4) + salt(8) + dataLen(2) + padLen(2)。
	headerSize = 17
	// windowSeed 轮换密钥的域分离字符串。
	windowSeed = "kuaima-obfs-rotate"
)

var (
	// ErrBadEpoch 表示块的时间窗口不在容忍范围内。
	ErrBadEpoch = errors.New("obfs: epoch out of tolerance")
	// ErrCorrupt 表示块头或长度异常。
	ErrCorrupt = errors.New("obfs: corrupt chunk")
	// ErrBadKey 表示未知密钥版本。
	ErrBadKey = errors.New("obfs: unknown key version")
)

// Key 一个混淆密钥版本。
type Key struct {
	ID       byte
	Password string
}

// Config 混淆参数。
type Config struct {
	// Password 当前主密码。
	Password string
	// KeyID 当前密钥版本，写入每个块头。
	KeyID byte
	// Keys 服务端同时接受的版本，用于密码轮换过渡。
	Keys []Key
	// PadTo 小于该长度的块补齐到该长度；0 表示不填充。
	PadTo int
	// Rotation 密钥轮换周期；0 表示固定密钥。
	Rotation time.Duration
	// Tolerance 允许的窗口偏差；0 时自动取一个窗口。
	Tolerance time.Duration
	// Now 仅用于测试；nil 使用 time.Now。
	Now func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Config) period() int64 {
	if c.Rotation <= 0 {
		return 0
	}
	seconds := int64(c.Rotation / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	return seconds
}

func (c Config) epoch(t time.Time) uint32 {
	period := c.period()
	if period <= 0 {
		return 0
	}
	return uint32(t.Unix() / period)
}

func (c Config) tolerance() uint32 {
	period := c.period()
	if period <= 0 {
		return 0
	}
	tolerance := int64(c.Tolerance / time.Second)
	if tolerance <= 0 {
		tolerance = period
	}
	return uint32(tolerance / period)
}

var padBuckets = [...]int{1024, 4096, 16384}

// paddedSize 把任意块长归到固定桶，大流量块也统一到 16KiB。
func paddedSize(n int) int {
	for _, bucket := range padBuckets {
		if n <= bucket {
			return bucket
		}
	}
	return n
}

func masterKey(password string) [32]byte {
	sum := blake2b.Sum256([]byte(password))
	return sum
}

func deriveKey(master [32]byte, epoch uint32, salt []byte) [32]byte {
	h, _ := blake2b.New256(nil)
	_, _ = h.Write(master[:])
	_, _ = h.Write([]byte(windowSeed))
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], epoch)
	_, _ = h.Write(buf[:])
	_, _ = h.Write(salt)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Writer 把明文流分块加密后写入底层连接。
type Writer struct {
	mu     sync.Mutex
	w      io.Writer
	master [32]byte
	cfg    Config
	buf    []byte
}

// NewWriter 创建混淆写入器。
func NewWriter(w io.Writer, cfg Config) *Writer {
	return &Writer{
		w:      w,
		master: masterKey(cfg.Password),
		cfg:    cfg,
		buf:    make([]byte, 0, MaxChunk),
	}
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := 0
	for len(p) > 0 {
		space := MaxChunk - len(w.buf)
		if space <= 0 {
			if err := w.flushLocked(); err != nil {
				return written, err
			}
			continue
		}
		n := len(p)
		if n > space {
			n = space
		}
		w.buf = append(w.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(w.buf) == MaxChunk {
			if err := w.flushLocked(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Flush 把当前缓冲作为一个块写出。
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

func (w *Writer) flushLocked() error {
	if len(w.buf) == 0 {
		return nil
	}
	epoch := w.cfg.epoch(w.cfg.now())
	var salt [8]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return err
	}
	key := deriveKey(w.master, epoch, salt[:])
	var nonce [chacha20.NonceSize]byte
	copy(nonce[:8], salt[:])
	cipher, err := chacha20.NewUnauthenticatedCipher(key[:], nonce[:])
	if err != nil {
		return err
	}
	dataLen := len(w.buf)
	padLen := 0
	if w.cfg.PadTo > 0 {
		if target := paddedSize(dataLen); target > dataLen {
			padLen = target - dataLen
		}
	}
	plain := make([]byte, dataLen+padLen)
	copy(plain, w.buf)
	ciphertext := make([]byte, len(plain))
	cipher.XORKeyStream(ciphertext, plain)

	header := make([]byte, headerSize)
	header[0] = w.cfg.KeyID
	binary.BigEndian.PutUint32(header[1:5], epoch)
	copy(header[5:13], salt[:])
	binary.BigEndian.PutUint16(header[13:15], uint16(dataLen))
	binary.BigEndian.PutUint16(header[15:17], uint16(padLen))
	if _, err := w.w.Write(header); err != nil {
		return err
	}
	if _, err := w.w.Write(ciphertext); err != nil {
		return err
	}
	w.buf = w.buf[:0]
	return nil
}

// Reader 从底层连接读取并解密分块。
type Reader struct {
	r      io.Reader
	master [32]byte
	cfg    Config
	keys   map[byte][32]byte
	plain  []byte
}

// NewReader 创建混淆读取器。
func NewReader(r io.Reader, cfg Config) *Reader {
	keys := make(map[byte][32]byte)
	if len(cfg.Keys) == 0 {
		keys[cfg.KeyID] = masterKey(cfg.Password)
	} else {
		for _, key := range cfg.Keys {
			keys[key.ID] = masterKey(key.Password)
		}
	}
	return &Reader{
		r:    r,
		cfg:  cfg,
		keys: keys,
	}
}

func (r *Reader) Read(p []byte) (int, error) {
	for len(r.plain) == 0 {
		if err := r.readChunk(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.plain)
	r.plain = r.plain[n:]
	return n, nil
}

func (r *Reader) readChunk() error {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r.r, header); err != nil {
		return err
	}
	keyID := header[0]
	epoch := binary.BigEndian.Uint32(header[1:5])
	dataLen := int(binary.BigEndian.Uint16(header[13:15]))
	padLen := int(binary.BigEndian.Uint16(header[15:17]))
	total := dataLen + padLen
	if dataLen == 0 || dataLen > MaxChunk || total > 65535 {
		return ErrCorrupt
	}
	master, ok := r.keys[keyID]
	if !ok {
		return ErrBadKey
	}
	nowEpoch := r.cfg.epoch(r.cfg.now())
	tolerance := r.cfg.tolerance()
	if epoch > nowEpoch+tolerance || nowEpoch > epoch+tolerance {
		return ErrBadEpoch
	}

	var salt [8]byte
	copy(salt[:], header[5:13])
	var nonce [chacha20.NonceSize]byte
	copy(nonce[:8], salt[:])
	key := deriveKey(master, epoch, salt[:])
	cipher, err := chacha20.NewUnauthenticatedCipher(key[:], nonce[:])
	if err != nil {
		return err
	}
	ciphertext := make([]byte, total)
	if _, err := io.ReadFull(r.r, ciphertext); err != nil {
		return err
	}
	plain := make([]byte, total)
	cipher.XORKeyStream(plain, ciphertext)
	r.plain = plain[:dataLen]
	return nil
}

// String 方便日志排查，不输出密钥。
func (c Config) String() string {
	return fmt.Sprintf("obfs(rotation=%s tolerance=%s)", c.Rotation, c.Tolerance)
}
