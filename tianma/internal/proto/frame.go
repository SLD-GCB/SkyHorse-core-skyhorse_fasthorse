// Package proto is the Tianma wire format: Hysteria-style HTTP/3 auth plus a
// TCP request frame carried on a raw QUIC stream.
package proto

import (
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// URLHost and URLPath identify the auth request among normal web traffic.
	URLHost = "localhost"
	URLPath = "/"

	// HeaderAuth carries "<uuid>:<hex token>".
	HeaderAuth = "Authorization"
	// HeaderPadding carries random bytes to blur the request size.
	HeaderPadding = "Tianma-Padding"

	// StatusAuthOK is the non-standard status Hysteria uses for auth success.
	StatusAuthOK = 204
)

// EncodeToken formats the auth header value.
func EncodeToken(uuid string, token []byte) string {
	return "Bearer " + uuid + ":" + hex.EncodeToString(token)
}

// DecodeToken parses the auth header value.
func DecodeToken(value string) (string, []byte, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.EqualFold(value[:7], "Bearer ") {
		value = value[7:]
	}
	for index := 0; index < len(value); index++ {
		if value[index] != ':' {
			continue
		}
		token, err := hex.DecodeString(value[index+1:])
		if err != nil {
			return "", nil, err
		}
		return value[:index], token, nil
	}
	return "", nil, errors.New("tianma: malformed auth header")
}
