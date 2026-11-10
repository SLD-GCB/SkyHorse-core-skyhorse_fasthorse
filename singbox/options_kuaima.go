package skyhorsebox

import (
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// TypeFastHorse is the JSON "type" for the FastHorse protocol.
const TypeFastHorse = "fasthorse"

// TypeKuaima is kept for source compatibility.
// Deprecated: use TypeFastHorse.
const TypeKuaima = TypeFastHorse

// KuaimaUser is one account.
type KuaimaUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// KuaimaObfsKey is one密钥版本.
type KuaimaObfsKey struct {
	ID       uint8  `json:"id"`
	Password string `json:"password"`
}

// KuaimaObfsOptions 混淆参数。
type KuaimaObfsOptions struct {
	Password  string             `json:"password"`
	KeyID     uint8              `json:"key_id,omitempty"`
	Rotation  badoption.Duration `json:"rotation,omitempty"`
	Tolerance badoption.Duration `json:"tolerance,omitempty"`
	PadTo     int                `json:"pad_to,omitempty"`
	Keys      []KuaimaObfsKey    `json:"keys,omitempty"`
}

// KuaimaGateOptions 动态门参数。
type KuaimaGateOptions struct {
	NodeID       string             `json:"node_id,omitempty"`
	FailureMode  string             `json:"failure_mode,omitempty"`
	FailureDelay badoption.Duration `json:"failure_delay,omitempty"`
}

// KuaimaInboundOptions 服务端节点。
type KuaimaInboundOptions struct {
	option.ListenOptions
	Users           []KuaimaUser              `json:"users,omitempty"`
	Obfs            KuaimaObfsOptions         `json:"obfs"`
	Gate            KuaimaGateOptions         `json:"gate,omitempty"`
	TLS             *option.InboundTLSOptions `json:"tls,omitempty"`
	ListenPortRange string                    `json:"listen_port_range,omitempty"`
	ServerPorts     []string                  `json:"server_ports,omitempty"`
	MaxStreams      int                       `json:"max_streams_per_connection,omitempty"`
	PortHopEnabled  *bool                     `json:"port_hop_enabled,omitempty"`
	PortRotate      int                       `json:"port_rotate_seconds,omitempty"`
	UpMbps          int                       `json:"up_mbps,omitempty"`
	DownMbps        int                       `json:"down_mbps,omitempty"`
	// Deprecated: accepted only so old saved configs still validate.
	Connections int     `json:"connections,omitempty"`
	TargetMbps  float64 `json:"target_mbps,omitempty"`
}

// KuaimaOutboundOptions 客户端节点。
type KuaimaOutboundOptions struct {
	option.DialerOptions
	option.ServerOptions
	Username                string                     `json:"username"`
	Password                string                     `json:"password"`
	Obfs                    KuaimaObfsOptions          `json:"obfs"`
	Gate                    KuaimaGateOptions          `json:"gate,omitempty"`
	TLS                     *option.OutboundTLSOptions `json:"tls,omitempty"`
	ServerPorts             []string                   `json:"server_ports,omitempty"`
	PortHopEnabled          *bool                      `json:"port_hop_enabled,omitempty"`
	PortRotateSeconds       int                        `json:"port_rotate_seconds,omitempty"`
	MaxStreamsPerConnection int                        `json:"max_streams_per_connection,omitempty"`
	UpMbps                  int                        `json:"up_mbps,omitempty"`
	DownMbps                int                        `json:"down_mbps,omitempty"`
	// Deprecated: accepted only so old subscription configs still validate.
	Connections int     `json:"connections,omitempty"`
	TargetMbps  float64 `json:"target_mbps,omitempty"`
}
