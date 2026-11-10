// Package gate 实现 obfs 内的动态凭据：时间戳 + nonce + HMAC，
// 网络上看不到明文结构，只有解出 obfs 的客户端才能生成。
package gate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/blake2b"
)

const (
	// RecordSize 固定记录长度，避免长度特征。
	RecordSize = 128
	// Version 当前协议版本。
	Version = 1
	// MaxUser 用户名最大长度。
	MaxUser = 32
	// Window 允许的时间偏差。
	Window = 30 * time.Second
	// NonceTTL nonce 缓存时长。
	NonceTTL = 5 * time.Minute

	domain = "kuaima-gate"
)

var (
	// ErrReplay nonce 已使用。
	ErrReplay = errors.New("gate: replay")
	// ErrExpired 时间戳超出窗口。
	ErrExpired = errors.New("gate: expired")
	// ErrAuth HMAC 不匹配。
	ErrAuth = errors.New("gate: authentication failed")
	// ErrMalformed 记录格式错误。
	ErrMalformed = errors.New("gate: malformed record")
)

// Config 动态门参数。
type Config struct {
	// Password 与 obfs 共用的主密码。
	Password string
	// NodeID 绑定到当前节点，跨节点重放会失败。
	NodeID string
	// Now 仅测试用；nil 使用 time.Now。
	Now func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func deriveKey(password, nodeID string) [32]byte {
	h, _ := blake2b.New256(nil)
	_, _ = h.Write([]byte(password))
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte(nodeID))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sign(key [32]byte, version byte, timestamp uint64, nonce []byte, user []byte) []byte {
	msg := make([]byte, 0, 1+8+16+1+len(user))
	msg = append(msg, version)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], timestamp)
	msg = append(msg, ts[:]...)
	msg = append(msg, nonce...)
	msg = append(msg, byte(len(user)))
	msg = append(msg, user...)
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(msg)
	return mac.Sum(nil)
}

// Seal 生成并写入一条动态凭据记录。
func Seal(w io.Writer, cfg Config, username string) error {
	user := []byte(username)
	if len(user) == 0 || len(user) > MaxUser {
		return fmt.Errorf("gate: bad username length %d", len(user))
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	version := byte(Version)
	timestamp := uint64(cfg.now().Unix())
	mac := sign(deriveKey(cfg.Password, cfg.NodeID), version, timestamp, nonce, user)

	record := make([]byte, RecordSize)
	record[0] = version
	binary.BigEndian.PutUint64(record[1:9], timestamp)
	copy(record[9:25], nonce)
	record[25] = byte(len(user))
	copy(record[26:26+len(user)], user)
	copy(record[26+len(user):26+len(user)+len(mac)], mac)

	_, err := w.Write(record)
	return err
}

// Verifier 校验动态凭据，并维护 nonce 重放缓存。
type Verifier struct {
	cfg    Config
	mu     sync.Mutex
	nonces map[string]time.Time
}

// NewVerifier 创建校验器。
func NewVerifier(cfg Config) *Verifier {
	return &Verifier{cfg: cfg, nonces: make(map[string]time.Time)}
}

// Verify 读取并校验一条记录，返回用户名。
func (v *Verifier) Verify(r io.Reader) (string, error) {
	record := make([]byte, RecordSize)
	if _, err := io.ReadFull(r, record); err != nil {
		return "", err
	}
	if record[0] != Version {
		return "", ErrMalformed
	}
	timestamp := binary.BigEndian.Uint64(record[1:9])
	now := v.cfg.now()
	if now.Unix() < int64(timestamp)-int64(Window/time.Second) ||
		now.Unix() > int64(timestamp)+int64(Window/time.Second) {
		return "", ErrExpired
	}
	nonce := record[9:25]
	userLen := int(record[25])
	if userLen == 0 || userLen > MaxUser {
		return "", ErrMalformed
	}
	user := record[26 : 26+userLen]
	mac := record[26+userLen : 26+userLen+sha256.Size]
	expected := sign(deriveKey(v.cfg.Password, v.cfg.NodeID), record[0], timestamp, nonce, user)
	if subtle.ConstantTimeCompare(mac, expected) != 1 {
		return "", ErrAuth
	}

	key := string(nonce)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweepLocked(now)
	if _, used := v.nonces[key]; used {
		return "", ErrReplay
	}
	v.nonces[key] = now.Add(NonceTTL)
	return string(user), nil
}

func (v *Verifier) sweepLocked(now time.Time) {
	for nonce, expiry := range v.nonces {
		if now.After(expiry) {
			delete(v.nonces, nonce)
		}
	}
}
