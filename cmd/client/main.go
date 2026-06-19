package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"rfrp/internal/client"
	"rfrp/internal/config"
)

func main() {
	var configPath string
	var serverControlAddr string
	var serverTunnelAddr string
	var localAddr string
	var clientID string

	flag.StringVar(&configPath, "config", "", "Path to config file (YAML)")
	flag.StringVar(&serverControlAddr, "control", "localhost:8000", "Server control address")
	flag.StringVar(&serverTunnelAddr, "tunnel", "localhost:8001", "Server tunnel address")
	flag.StringVar(&localAddr, "local", "localhost:3000", "Local service address")
	flag.StringVar(&clientID, "id", "", "Client ID (auto-generated if not provided)")

	flag.Parse()

	if configPath != "" {
		// 使用配置文件
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			logrus.Fatalf("Failed to load config: %v", err)
		}

		if err := cfg.Validate(); err != nil {
			logrus.Fatalf("Invalid config: %v", err)
		}

		if cfg.Client.ID != "" {
			clientID = cfg.Client.ID
		}
		if clientID == "" {
			clientID = fmt.Sprintf("client_%d", time.Now().Unix())
		}

		logrus.Infof("Starting RFRP Client (config mode)")
		logrus.Infof("Control: %s", cfg.Server.ControlAddr)
		logrus.Infof("Tunnel: %s", cfg.Server.TunnelAddr)
		logrus.Infof("ID: %s", clientID)
		logrus.Infof("Forward rules: %d", len(cfg.Forwards))

		for i, rule := range cfg.Forwards {
			logrus.Infof("  [%d] %s: %s -> :%d", i+1, rule.Name, rule.LocalAddr, rule.RemotePort)
		}

		cli := client.NewClientWithConfig(cfg.Server.ControlAddr, cfg.Server.TunnelAddr, clientID, cfg.Forwards)
		if err := cli.Start(); err != nil {
			logrus.Fatalf("Client failed to start: %v", err)
		}
	} else {
		// 使用命令行参数（向后兼容）
		if clientID == "" {
			clientID = fmt.Sprintf("client_%d", time.Now().Unix())
		}

		logrus.Infof("Starting RFRP Client (command line mode)")
		logrus.Infof("Control: %s", serverControlAddr)
		logrus.Infof("Tunnel: %s", serverTunnelAddr)
		logrus.Infof("Local: %s", localAddr)
		logrus.Infof("ID: %s", clientID)

		cli := client.NewClient(serverControlAddr, serverTunnelAddr, localAddr, clientID)
		if err := cli.Start(); err != nil {
			logrus.Fatalf("Client failed to start: %v", err)
		}
	}
}
