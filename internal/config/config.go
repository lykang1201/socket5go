package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ForwardRule struct {
	Name        string `yaml:"name"`        // 转发规则名称
	Path        string `yaml:"path"`        // 路径前缀 (如 /vue_frontend)
	LocalAddr   string `yaml:"local_addr"`  // 本地服务地址 (localhost:3000)
	RemotePort  int    `yaml:"remote_port"` // 远程服务端口 (8080)，已废弃，保留兼容
	Description string `yaml:"description"` // 规则描述
}

type ClientConfig struct {
	Server struct {
		ControlAddr string `yaml:"control_addr"` // 服务端控制地址
		TunnelAddr  string `yaml:"tunnel_addr"`  // 服务端隧道地址
	} `yaml:"server"`

	Client struct {
		ID string `yaml:"id"` // 客户端ID
	} `yaml:"client"`

	Forwards []ForwardRule `yaml:"forwards"` // 转发规则列表
}

func LoadConfig(configPath string) (*ClientConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config ClientConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

func (c *ClientConfig) Validate() error {
	if c.Server.ControlAddr == "" {
		return fmt.Errorf("server.control_addr is required")
	}
	if c.Server.TunnelAddr == "" {
		return fmt.Errorf("server.tunnel_addr is required")
	}
	if len(c.Forwards) == 0 {
		return fmt.Errorf("at least one forward rule is required")
	}

	for i, rule := range c.Forwards {
		if rule.Name == "" {
			return fmt.Errorf("forward rule %d: name is required", i+1)
		}
		if rule.LocalAddr == "" {
			return fmt.Errorf("forward rule %d: local_addr is required", i+1)
		}
		if rule.Path == "" && rule.RemotePort <= 0 {
			return fmt.Errorf("forward rule %d: either path or remote_port is required", i+1)
		}
		if rule.RemotePort < 0 || rule.RemotePort > 65535 {
			return fmt.Errorf("forward rule %d: remote_port must be between 0 and 65535", i+1)
		}
	}

	return nil
}
