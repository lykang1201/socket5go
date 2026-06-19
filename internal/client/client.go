package client

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"rfrp/internal/config"
	"rfrp/internal/protocol"
)

type Client struct {
	serverControlAddr string
	serverTunnelAddr  string
	localAddr         string
	clientID          string

	forwardRules     []config.ForwardRule
	pathToLocalAddr  map[string]string
	portToLocalAddr  map[int]string

	logger *logrus.Logger
}

func NewClient(serverControlAddr, serverTunnelAddr, localAddr, clientID string) *Client {
	return &Client{
		serverControlAddr: serverControlAddr,
		serverTunnelAddr:  serverTunnelAddr,
		localAddr:         localAddr,
		clientID:          clientID,
		portToLocalAddr:   make(map[int]string),
		logger:            logrus.New(),
	}
}

func NewClientWithConfig(serverControlAddr, serverTunnelAddr, clientID string, forwardRules []config.ForwardRule) *Client {
	pathToLocalAddr := make(map[string]string)
	portToLocalAddr := make(map[int]string)

	for _, rule := range forwardRules {
		if rule.Path != "" {
			pathToLocalAddr[rule.Path] = rule.LocalAddr
		}
		if rule.RemotePort > 0 {
			portToLocalAddr[rule.RemotePort] = rule.LocalAddr
		}
	}

	return &Client{
		serverControlAddr: serverControlAddr,
		serverTunnelAddr:  serverTunnelAddr,
		clientID:          clientID,
		forwardRules:      forwardRules,
		pathToLocalAddr:   pathToLocalAddr,
		portToLocalAddr:   portToLocalAddr,
		logger:            logrus.New(),
	}
}

func (c *Client) Start() error {
	c.logger.Infof("Starting RFRP client...")
	c.logger.Infof("Server control: %s", c.serverControlAddr)
	c.logger.Infof("Server tunnel: %s", c.serverTunnelAddr)
	c.logger.Infof("Client ID: %s", c.clientID)

	if c.localAddr != "" {
		c.logger.Infof("Local target: %s", c.localAddr)
	}

	if len(c.forwardRules) > 0 {
		c.logger.Infof("Forward rules: %d", len(c.forwardRules))
		for i, rule := range c.forwardRules {
			if rule.Path != "" {
				c.logger.Infof("  [%d] %s: %s -> path %s", i+1, rule.Name, rule.LocalAddr, rule.Path)
			} else {
				c.logger.Infof("  [%d] %s: %s -> :%d", i+1, rule.Name, rule.LocalAddr, rule.RemotePort)
			}
		}
	}

	if err := c.register(); err != nil {
		return err
	}

	c.listenControlMessages()

	return nil
}

func (c *Client) register() error {
	conn, err := net.Dial("tcp", c.serverControlAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	reqMetadata, _ := json.Marshal(protocol.RegisterRequest{ClientID: c.clientID})
	msg := &protocol.Message{
		Type:     protocol.MsgTypeRegister,
		ClientID: c.clientID,
		Metadata: reqMetadata,
	}

	if err := protocol.WriteMessage(conn, msg); err != nil {
		return err
	}

	resp, err := protocol.ReadMessage(conn)
	if err != nil {
		return err
	}

	if resp.Type != protocol.MsgTypeRegisterAck {
		return err
	}

	var registerResp protocol.RegisterResponse
	if err := json.Unmarshal(resp.Metadata, &registerResp); err != nil {
		return err
	}

	if !registerResp.Success {
		return err
	}

	c.logger.Infof("Registered successfully with server")
	return nil
}

func (c *Client) listenControlMessages() {
	for {
		conn, err := net.Dial("tcp", c.serverControlAddr)
		if err != nil {
			c.logger.Errorf("Failed to connect to control server: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		reqMetadata, _ := json.Marshal(protocol.RegisterRequest{ClientID: c.clientID})
		msg := &protocol.Message{
			Type:     protocol.MsgTypeRegister,
			ClientID: c.clientID,
			Metadata: reqMetadata,
		}
		if err := protocol.WriteMessage(conn, msg); err != nil {
			c.logger.Errorf("Failed to re-register: %v", err)
			conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		c.logger.Infof("Reconnected to control server")

		go c.handleControlMessages(conn)

		select {}
	}
}

func (c *Client) handleControlMessages(conn net.Conn) {
	defer conn.Close()

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			c.logger.Warnf("Failed to read control message: %v", err)
			return
		}

		switch msg.Type {
		case protocol.MsgTypeHeartbeat:
			resp := &protocol.Message{
				Type: protocol.MsgTypeHeartbeatAck,
			}
			protocol.WriteMessage(conn, resp)

		case protocol.MsgTypeNewConn:
			var req protocol.NewConnRequest
			if err := json.Unmarshal(msg.Metadata, &req); err != nil {
				c.logger.Errorf("Failed to unmarshal new conn request: %v", err)
				continue
			}
			go c.handleNewTunnel(req.ConnID, req.Port, req.Path)
		}
	}
}

func (c *Client) handleNewTunnel(connID string, port int, path string) {
	tunnelConn, err := net.Dial("tcp", c.serverTunnelAddr)
	if err != nil {
		c.logger.Errorf("Failed to connect to tunnel server: %v", err)
		return
	}

	reqMetadata, _ := json.Marshal(protocol.NewConnRequest{ConnID: connID, Port: port, Path: path})
	msg := &protocol.Message{
		Type:     protocol.MsgTypeNewConn,
		ConnID:   connID,
		Metadata: reqMetadata,
	}
	if err := protocol.WriteMessage(tunnelConn, msg); err != nil {
		c.logger.Errorf("Failed to send tunnel message: %v", err)
		tunnelConn.Close()
		return
	}

	resp, err := protocol.ReadMessage(tunnelConn)
	if err != nil {
		c.logger.Errorf("Failed to read tunnel response: %v", err)
		tunnelConn.Close()
		return
	}

	if resp.Type != protocol.MsgTypeNewConnAck {
		c.logger.Errorf("Unexpected tunnel response type: %s", resp.Type)
		tunnelConn.Close()
		return
	}

	localAddr := c.localAddr

	if path != "" && len(c.pathToLocalAddr) > 0 {
		if addr, ok := c.matchPath(path); ok {
			localAddr = addr
			c.logger.Infof("Using local address %s for path %s", localAddr, path)
		} else {
			c.logger.Warnf("No local address configured for path %s, using default %s", path, localAddr)
		}
	} else if port > 0 && len(c.portToLocalAddr) > 0 {
		if addr, ok := c.portToLocalAddr[port]; ok {
			localAddr = addr
			c.logger.Infof("Using local address %s for port %d", localAddr, port)
		} else {
			c.logger.Warnf("No local address configured for port %d, using default %s", port, localAddr)
		}
	}

	if localAddr == "" {
		c.logger.Error("No local address configured")
		tunnelConn.Close()
		return
	}

	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		c.logger.Errorf("Failed to connect to local service %s: %v", localAddr, err)
		tunnelConn.Close()
		return
	}

	c.logger.Infof("Established tunnel connection: %s -> %s", connID, localAddr)

	c.pipeRawConnections(localConn, tunnelConn)
}

func (c *Client) matchPath(path string) (string, bool) {
	var bestMatch string
	var bestLen int

	for p, addr := range c.pathToLocalAddr {
		if strings.HasPrefix(path, p) {
			if len(p) > bestLen {
				bestLen = len(p)
				bestMatch = addr
			}
		}
	}

	if bestMatch != "" {
		return bestMatch, true
	}
	return "", false
}

func (c *Client) pipeRawConnections(localConn, tunnelConn net.Conn) {
	defer localConn.Close()
	defer tunnelConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		c.copyRaw(localConn, tunnelConn)
	}()

	go func() {
		defer wg.Done()
		c.copyRaw(tunnelConn, localConn)
	}()

	wg.Wait()
}

func (c *Client) copyRaw(src, dst net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if err != nil {
			break
		}
		if n == 0 {
			continue
		}
		_, err = dst.Write(buf[:n])
		if err != nil {
			break
		}
	}
}