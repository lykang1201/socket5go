package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"rfrp/internal/protocol"
)

type Server struct {
	controlAddr string
	tunnelAddr  string
	publicAddr  string

	clients      map[string]net.Conn
	clientLocker sync.RWMutex

	tunnelConnChan chan *tunnelConnInfo

	logger *logrus.Logger
}

type tunnelConnInfo struct {
	conn   net.Conn
	connID string
	port   int
	path   string
}

func NewServer(controlAddr, tunnelAddr, publicAddr string) *Server {
	return &Server{
		controlAddr:    controlAddr,
		tunnelAddr:     tunnelAddr,
		publicAddr:     publicAddr,
		clients:        make(map[string]net.Conn),
		tunnelConnChan: make(chan *tunnelConnInfo, 100),
		logger:         logrus.New(),
	}
}

func (s *Server) Start() error {
	s.logger.Infof("Starting RFRP server...")
	s.logger.Infof("Control listening on: %s", s.controlAddr)
	s.logger.Infof("Tunnel listening on: %s", s.tunnelAddr)
	s.logger.Infof("Public HTTP listening on: %s", s.publicAddr)

	go s.handlePublicConnections()
	go s.listenControl()
	go s.listenTunnel()

	select {}
}

func (s *Server) listenControl() {
	listener, err := net.Listen("tcp", s.controlAddr)
	if err != nil {
		s.logger.Fatalf("Failed to listen on control port: %v", err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.logger.Errorf("Failed to accept control connection: %v", err)
			continue
		}
		go s.handleControlConnection(conn)
	}
}

func (s *Server) handleControlConnection(conn net.Conn) {
	defer conn.Close()

	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		s.logger.Errorf("Failed to read control message: %v", err)
		return
	}

	if msg.Type != protocol.MsgTypeRegister {
		s.logger.Errorf("Unexpected message type: %s", msg.Type)
		return
	}

	var req protocol.RegisterRequest
	if err := json.Unmarshal(msg.Metadata, &req); err != nil {
		s.logger.Errorf("Failed to unmarshal register request: %v", err)
		return
	}

	s.clientLocker.Lock()
	s.clients[req.ClientID] = conn
	s.clientLocker.Unlock()

	s.logger.Infof("Client registered: %s", req.ClientID)

	resp := &protocol.Message{
		Type: protocol.MsgTypeRegisterAck,
	}
	respMetadata, _ := json.Marshal(protocol.RegisterResponse{Success: true})
	resp.Metadata = respMetadata

	if err := protocol.WriteMessage(conn, resp); err != nil {
		s.logger.Errorf("Failed to send register ack: %v", err)
		return
	}

	s.heartbeatLoop(conn, req.ClientID)
}

func (s *Server) heartbeatLoop(conn net.Conn, clientID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		msg := &protocol.Message{
			Type: protocol.MsgTypeHeartbeat,
		}
		if err := protocol.WriteMessage(conn, msg); err != nil {
			s.logger.Warnf("Heartbeat failed for client %s: %v", clientID, err)
			s.clientLocker.Lock()
			delete(s.clients, clientID)
			s.clientLocker.Unlock()
			return
		}
	}
}

func (s *Server) listenTunnel() {
	listener, err := net.Listen("tcp", s.tunnelAddr)
	if err != nil {
		s.logger.Fatalf("Failed to listen on tunnel port: %v", err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			s.logger.Errorf("Failed to accept tunnel connection: %v", err)
			continue
		}
		go s.handleTunnelConnection(conn)
	}
}

func (s *Server) handleTunnelConnection(conn net.Conn) {
	defer conn.Close()

	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		s.logger.Errorf("Failed to read tunnel message: %v", err)
		return
	}

	if msg.Type != protocol.MsgTypeNewConn {
		s.logger.Errorf("Unexpected tunnel message type: %s", msg.Type)
		return
	}

	var req protocol.NewConnRequest
	if err := json.Unmarshal(msg.Metadata, &req); err != nil {
		s.logger.Errorf("Failed to unmarshal new conn request: %v", err)
		return
	}

	s.tunnelConnChan <- &tunnelConnInfo{conn: conn, connID: req.ConnID, port: req.Port, path: req.Path}

	resp := &protocol.Message{
		Type: protocol.MsgTypeNewConnAck,
	}
	respMetadata, _ := json.Marshal(protocol.NewConnResponse{Success: true})
	resp.Metadata = respMetadata

	if err := protocol.WriteMessage(conn, resp); err != nil {
		s.logger.Errorf("Failed to send new conn ack: %v", err)
	}
}

func (s *Server) handlePublicConnections() {
	listener, err := net.Listen("tcp", s.publicAddr)
	if err != nil {
		s.logger.Fatalf("Failed to listen on public port: %v", err)
	}
	defer listener.Close()

	s.logger.Infof("Public HTTP server listening on %s", s.publicAddr)

	for {
		publicConn, err := listener.Accept()
		if err != nil {
			s.logger.Errorf("Failed to accept public connection: %v", err)
			continue
		}
		go s.handlePublicConnection(publicConn)
	}
}

func (s *Server) handlePublicConnection(publicConn net.Conn) {
	defer publicConn.Close()

	s.clientLocker.RLock()
	if len(s.clients) == 0 {
		s.clientLocker.RUnlock()
		s.logger.Warn("No clients available to handle connection")
		s.sendHTTPError(publicConn, 503, "No available clients")
		return
	}

	var clientConn net.Conn
	var clientID string
	for id, conn := range s.clients {
		clientConn = conn
		clientID = id
		break
	}
	s.clientLocker.RUnlock()

	if clientConn == nil {
		s.logger.Warn("No clients available")
		s.sendHTTPError(publicConn, 503, "No available clients")
		return
	}

	reader := bufio.NewReader(publicConn)
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		s.logger.Errorf("Failed to read request line: %v", err)
		return
	}

	requestLine = strings.TrimSpace(requestLine)
	parts := strings.Split(requestLine, " ")
	if len(parts) < 2 {
		s.logger.Errorf("Invalid request line: %s", requestLine)
		s.sendHTTPError(publicConn, 400, "Bad request")
		return
	}

	httpMethod := parts[0]
	path := parts[1]

	s.logger.Infof("Received request: %s %s", httpMethod, path)

	connID := fmt.Sprintf("%d", time.Now().UnixNano())

	msg := &protocol.Message{
		Type:     protocol.MsgTypeNewConn,
		ClientID: clientID,
		ConnID:   connID,
	}
	msgMetadata, _ := json.Marshal(protocol.NewConnRequest{ConnID: connID, Path: path})
	msg.Metadata = msgMetadata

	if err := protocol.WriteMessage(clientConn, msg); err != nil {
		s.logger.Errorf("Failed to send new conn to client: %v", err)
		s.sendHTTPError(publicConn, 503, "Failed to connect to client")
		return
	}

	tunnelInfo := <-s.tunnelConnChan
	if tunnelInfo.connID != connID {
		s.logger.Errorf("ConnID mismatch: expected %s, got %s", connID, tunnelInfo.connID)
		return
	}

	s.logger.Infof("Established tunnel for conn %s (path: %s)", connID, tunnelInfo.path)

	s.pipeHTTPRequest(publicConn, tunnelInfo.conn, requestLine, reader)
}

func (s *Server) sendHTTPError(conn net.Conn, statusCode int, message string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		statusCode, httpStatusText(statusCode), len(message), message)
	conn.Write([]byte(resp))
}

func httpStatusText(code int) string {
	switch code {
	case 400:
		return "Bad Request"
	case 404:
		return "Not Found"
	case 503:
		return "Service Unavailable"
	default:
		return "Internal Server Error"
	}
}

func (s *Server) pipeHTTPRequest(publicConn, tunnelConn net.Conn, requestLine string, reader *bufio.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		tunnelConn.Write([]byte(requestLine + "\r\n"))
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if err != nil {
				break
			}
			if n == 0 {
				continue
			}
			s.copyDataToRaw(tunnelConn, buf[:n])
		}
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := tunnelConn.Read(buf)
			if err != nil {
				break
			}
			if n == 0 {
				continue
			}
			publicConn.Write(buf[:n])
		}
	}()

	wg.Wait()
}

func (s *Server) copyDataToRaw(dst net.Conn, data []byte) {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if err != nil {
			break
		}
		data = data[n:]
	}
}

func (s *Server) pipeConnections(conn1, conn2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		s.copyData(conn1, conn2)
	}()

	go func() {
		defer wg.Done()
		s.copyData(conn2, conn1)
	}()

	wg.Wait()
}

func (s *Server) copyData(dst, src net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if err != nil {
			break
		}
		if n == 0 {
			continue
		}

		msg := &protocol.Message{
			Type: protocol.MsgTypeData,
			Data: buf[:n],
		}
		if err := protocol.WriteMessage(dst, msg); err != nil {
			break
		}
	}
}