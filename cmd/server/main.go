package main

import (
	"flag"

	"github.com/sirupsen/logrus"

	"rfrp/internal/server"
)

func main() {
	var controlAddr string
	var tunnelAddr string
	var publicAddr string

	flag.StringVar(&controlAddr, "control", ":8000", "Control server address (default: :8000)")
	flag.StringVar(&tunnelAddr, "tunnel", ":8001", "Tunnel server address (default: :8001)")
	flag.StringVar(&publicAddr, "public", ":8080", "Public HTTP server address (default: :8080)")

	flag.Parse()

	logrus.Infof("Starting RFRP Server")
	logrus.Infof("Control: %s", controlAddr)
	logrus.Infof("Tunnel: %s", tunnelAddr)
	logrus.Infof("Public: %s", publicAddr)

	srv := server.NewServer(controlAddr, tunnelAddr, publicAddr)
	if err := srv.Start(); err != nil {
		logrus.Fatalf("Server failed to start: %v", err)
	}
}
