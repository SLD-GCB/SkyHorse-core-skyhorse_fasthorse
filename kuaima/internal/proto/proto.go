// Package proto 定义快马的认证头和目标地址编码。
package proto

import (
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	// AuthScheme 出现在 Proxy-Authorization 里的认证方案。
	AuthScheme = "Kuaima"
	// HeaderAuth 标准代理认证头。
	HeaderAuth = "Proxy-Authorization"
	// ExporterLabel 派生连接绑定令牌的 TLS exporter 标签。
	ExporterLabel = "kuaima"
	// TokenSize 令牌长度。
	TokenSize = 32
)

var (
	// ErrMalformed 表示认证头格式错误。
	ErrMalformed = errors.New("kuaima: malformed authorization")
	// ErrBadTarget 表示目标地址不合法。
	ErrBadTarget = errors.New("kuaima: bad target")
)

// EncodeToken 生成 `Kuaima name:hex(token)`。
func EncodeToken(name string, token []byte) string {
	return AuthScheme + " " + name + ":" + hex.EncodeToString(token)
}

// DecodeToken 解析 `Kuaima name:hex(token)`；也接受不带 scheme 的 `name:hex`。
func DecodeToken(value string) (string, []byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, ErrMalformed
	}
	if len(value) > len(AuthScheme) && strings.EqualFold(value[:len(AuthScheme)], AuthScheme) {
		value = strings.TrimSpace(value[len(AuthScheme):])
	}
	index := strings.IndexByte(value, ':')
	if index <= 0 {
		return "", nil, ErrMalformed
	}
	token, err := hex.DecodeString(value[index+1:])
	if err != nil || len(token) != TokenSize {
		return "", nil, ErrMalformed
	}
	return value[:index], token, nil
}

// DeriveToken 从当前 TLS 连接派生绑定令牌。
func DeriveToken(state *tls.ConnectionState, name, password string) ([]byte, error) {
	if state == nil {
		return nil, errors.New("kuaima: no tls state")
	}
	context := []byte(name + ":" + password)
	token, err := state.ExportKeyingMaterial(ExporterLabel, context, TokenSize)
	if err != nil {
		return nil, fmt.Errorf("kuaima: exporter: %w", err)
	}
	return token, nil
}

// ParseTarget 解析 CONNECT 的 authority。
func ParseTarget(authority string) (string, uint16, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(authority))
	if err != nil {
		return "", 0, ErrBadTarget
	}
	if host == "" {
		return "", 0, ErrBadTarget
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", 0, ErrBadTarget
	}
	return host, uint16(port), nil
}

// Target 把 host:port 重新拼成 authority。
func Target(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}
