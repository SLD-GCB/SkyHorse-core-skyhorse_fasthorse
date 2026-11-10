package client

import (
	"encoding/hex"
	"errors"
)

// Config is the client configuration.
type Config struct {
	Listen                  string   `json:"listen"`
	Server                  string   `json:"server"`
	ServerPort              int      `json:"server_port"`
	Servers                 []string `json:"servers"`
	ServerName              string   `json:"server_name"`
	Pin                     string   `json:"pin"`
	Insecure                bool     `json:"insecure"`
	UUID                    string   `json:"uuid"`
	Password                string   `json:"password"`
	ObfsPassword            string   `json:"obfs_password"`
	ObfsKeys                []string `json:"obfs_keys"`
	ObfsRotationSeconds     int      `json:"obfs_rotation_seconds"`
	ObfsToleranceSeconds    int      `json:"obfs_tolerance_seconds"`
	UpMbps                  int      `json:"up_mbps"`
	DownMbps                int      `json:"down_mbps"`
	BBRProfile              string   `json:"bbr_profile"`
	DisableLossCompensation bool     `json:"disable_loss_compensation"`
	Connections             int      `json:"connections"`
	MaxStreamsPerConnection int      `json:"max_streams_per_connection"`
	PortRotateSeconds       int      `json:"port_rotate_seconds"`
	Prewarm                 bool     `json:"prewarm"`
	NodeID                  string   `json:"node_id"`
}

func decodePin(text string) ([]byte, error) {
	if text == "" {
		return nil, nil
	}
	pin, err := hex.DecodeString(text)
	if err != nil || len(pin) != 32 {
		return nil, errors.New("tianma: pin must be 64 hex characters")
	}
	return pin, nil
}
