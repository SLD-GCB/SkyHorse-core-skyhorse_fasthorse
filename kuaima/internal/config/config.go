// Package config 定义快马服务端与客户端配置。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// User 一个快马用户。
type User struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// Policy 目的地策略。
type Policy struct {
	AllowPrivate  bool     `json:"allow_private"`
	AllowLoopback bool     `json:"allow_loopback"`
	AllowedPorts  []uint16 `json:"allowed_ports"`
	DeniedCIDRs   []string `json:"denied_cidrs"`
}

// Guard 连接滥用限制。
type Guard struct {
	MaxConnectionsPerIP   int `json:"max_connections_per_ip"`
	MaxConnectionsPerUser int `json:"max_connections_per_user"`
	MaxStreamsPerUser     int `json:"max_streams_per_user"`
	MaxStreamsPerConn     int `json:"max_streams_per_connection"`
	MaxHandshakes         int `json:"max_handshakes"`
}

// ObfsKey 混淆密钥版本。
type ObfsKey struct {
	ID       int    `json:"id"`
	Password string `json:"password"`
}

// ServerConfig 服务端配置。
type ServerConfig struct {
	Listen               string    `json:"listen"`
	NodeID               string    `json:"node_id"`
	PortRange            string    `json:"port_range"`
	CertificatePath      string    `json:"certificate_path"`
	KeyPath              string    `json:"key_path"`
	ServerName           string    `json:"server_name"`
	ObfsPassword         string    `json:"obfs_password"`
	ObfsKeys             []ObfsKey `json:"obfs_keys"`
	ObfsRotationSeconds  int       `json:"obfs_rotation_seconds"`
	ObfsToleranceSeconds int       `json:"obfs_tolerance_seconds"`
	ObfsPadTo            int       `json:"obfs_pad_to"`
	Users                []User    `json:"users"`
	Policy               Policy    `json:"policy"`
	Guard                Guard     `json:"guard"`
	FailureMode          string    `json:"failure_mode"`
	FailureDelayMS       int       `json:"failure_delay_ms"`
}

// Entry 客户端入口。
type Entry struct {
	Server    string `json:"server"`
	NodeID    string `json:"node_id"`
	PortRange string `json:"port_range"`
}

// ClientConfig 客户端配置。
type ClientConfig struct {
	Listen               string  `json:"listen"`
	Server               string  `json:"server"`
	NodeID               string  `json:"node_id"`
	PortRange            string  `json:"port_range"`
	ServerName           string  `json:"server_name"`
	Username             string  `json:"username"`
	Password             string  `json:"password"`
	CertificatePath      string  `json:"certificate_path"`
	ObfsPassword         string  `json:"obfs_password"`
	ObfsKeyID            int     `json:"obfs_key_id"`
	ObfsRotationSeconds  int     `json:"obfs_rotation_seconds"`
	ObfsToleranceSeconds int     `json:"obfs_tolerance_seconds"`
	ObfsPadTo            int     `json:"obfs_pad_to"`
	Connections          int     `json:"connections_per_entry"`
	MaxConnections       int     `json:"max_connections"`
	TargetMbps           float64 `json:"target_mbps"`
	ReceiveWindowMB      int     `json:"receive_window_mb"`
	PortRotateSeconds    int     `json:"port_rotate_seconds"`
	DisableRotateFailure bool    `json:"disable_rotate_on_failure"`
	DisableRotateDegrade bool    `json:"disable_rotate_on_degrade"`
	Entries              []Entry `json:"entries"`
}

// LoadServer 读取服务端配置。
func LoadServer(path string) (ServerConfig, error) {
	var cfg ServerConfig
	if err := loadJSON(path, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0"
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "default"
	}
	if cfg.PortRange == "" {
		return cfg, fmt.Errorf("config: port_range is required")
	}
	if cfg.ObfsPassword == "" {
		return cfg, fmt.Errorf("config: obfs_password is required")
	}
	if cfg.CertificatePath == "" || cfg.KeyPath == "" {
		return cfg, fmt.Errorf("config: certificate_path and key_path are required")
	}
	if len(cfg.Users) == 0 {
		return cfg, fmt.Errorf("config: users is required")
	}
	if cfg.Guard.MaxConnectionsPerIP <= 0 {
		cfg.Guard.MaxConnectionsPerIP = 64
	}
	if cfg.Guard.MaxConnectionsPerUser <= 0 {
		cfg.Guard.MaxConnectionsPerUser = 16
	}
	if cfg.Guard.MaxStreamsPerUser <= 0 {
		cfg.Guard.MaxStreamsPerUser = 256
	}
	if cfg.Guard.MaxStreamsPerConn <= 0 {
		cfg.Guard.MaxStreamsPerConn = 128
	}
	if cfg.Guard.MaxHandshakes <= 0 {
		cfg.Guard.MaxHandshakes = 128
	}
	if cfg.FailureMode == "" {
		cfg.FailureMode = "drop"
	}
	if cfg.FailureDelayMS <= 0 {
		cfg.FailureDelayMS = 1500
	}
	if cfg.ObfsPadTo == 0 {
		cfg.ObfsPadTo = 1024
	}
	if cfg.ObfsPadTo < 0 {
		cfg.ObfsPadTo = 0
	}
	return cfg, nil
}

// LoadClient 读取客户端配置。
func LoadClient(path string) (ClientConfig, error) {
	var cfg ClientConfig
	if err := loadJSON(path, &cfg); err != nil {
		return cfg, err
	}
	if len(cfg.Entries) == 0 {
		if cfg.Server == "" || cfg.PortRange == "" {
			return cfg, fmt.Errorf("config: entries or server/port_range is required")
		}
		cfg.Entries = []Entry{{Server: cfg.Server, PortRange: cfg.PortRange}}
	}
	if cfg.ObfsPassword == "" {
		return cfg, fmt.Errorf("config: obfs_password is required")
	}
	if cfg.Username == "" {
		return cfg, fmt.Errorf("config: username is required")
	}
	if cfg.CertificatePath == "" {
		return cfg, fmt.Errorf("config: certificate_path is required")
	}
	if cfg.Connections <= 0 {
		cfg.Connections = 2
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = cfg.Connections * 4
	}
	if cfg.MaxConnections < cfg.Connections {
		cfg.MaxConnections = cfg.Connections
	}
	if cfg.ReceiveWindowMB <= 0 {
		cfg.ReceiveWindowMB = 16
	}
	if cfg.ObfsPadTo == 0 {
		cfg.ObfsPadTo = 1024
	}
	if cfg.ObfsPadTo < 0 {
		cfg.ObfsPadTo = 0
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:1080"
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "default"
	}
	return cfg, nil
}

func loadJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Ports 解析 "20000-20010" 或 "20000,20002"。
func Ports(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("config: empty port range")
	}
	var ports []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("config: bad port range %q", part)
			}
			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil || end < start {
				return nil, fmt.Errorf("config: bad port range %q", part)
			}
			for port := start; port <= end; port++ {
				if port <= 0 || port > 65535 {
					return nil, fmt.Errorf("config: bad port %d", port)
				}
				ports = append(ports, port)
			}
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("config: bad port %q", part)
		}
		ports = append(ports, port)
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("config: no ports")
	}
	return ports, nil
}

// ObfsRotation 返回轮换周期。
func ObfsRotation(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// ObfsTolerance 返回容忍窗口。
func ObfsTolerance(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
