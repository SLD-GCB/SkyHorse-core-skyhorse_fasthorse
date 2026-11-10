package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// MasqueradeConfig decides what an unauthenticated visitor sees.
// 字段名与 sing-box 原生 masquerade 保持一致：
//
//	file:   directory
//	proxy:  url, rewrite_host
//	string: status_code, headers, content
type MasqueradeConfig struct {
	Type        string     `json:"type"` // "", "404", "string", "file", "proxy"
	Directory   string     `json:"directory,omitempty"`
	URL         string     `json:"url,omitempty"`
	RewriteHost bool       `json:"rewrite_host,omitempty"`
	StatusCode  int        `json:"status_code,omitempty"`
	Headers     HTTPHeader `json:"headers,omitempty"`
	Content     string     `json:"content,omitempty"`
}

// HTTPHeader 兼容 sing-box 的写法：值可以是字符串，也可以是字符串数组。
type HTTPHeader map[string][]string

func (h *HTTPHeader) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(HTTPHeader, len(raw))
	for key, value := range raw {
		var single string
		if err := json.Unmarshal(value, &single); err == nil {
			out[key] = []string{single}
			continue
		}
		var list []string
		if err := json.Unmarshal(value, &list); err != nil {
			return fmt.Errorf("masquerade: bad header %q: %w", key, err)
		}
		out[key] = list
	}
	*h = out
	return nil
}

// NewMasquerade builds the handler for unauthenticated traffic.
func NewMasquerade(config MasqueradeConfig) (http.Handler, error) {
	switch strings.ToLower(config.Type) {
	case "", "404":
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte("<!doctype html><title>404</title><h1>Not Found</h1>"))
		}), nil
	case "string":
		status := config.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		headers := config.Headers
		content := config.Content
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			for key, values := range headers {
				for _, value := range values {
					writer.Header().Add(key, value)
				}
			}
			if writer.Header().Get("Content-Type") == "" {
				writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			}
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(content))
		}), nil
	case "file":
		if config.Directory == "" {
			return nil, fmt.Errorf("masquerade: directory is required for type file")
		}
		return http.FileServer(http.Dir(config.Directory)), nil
	case "proxy":
		target, err := url.Parse(config.URL)
		if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
			return nil, fmt.Errorf("masquerade: bad url %q", config.URL)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		proxy.Director = func(request *http.Request) {
			originalDirector(request)
			if config.RewriteHost {
				request.Host = target.Host
			}
		}
		return proxy, nil
	default:
		return nil, fmt.Errorf("masquerade: unknown type %q", config.Type)
	}
}
