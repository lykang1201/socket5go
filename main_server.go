package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

const (
	SOCKS5Version    = 0x05
	SOCKS5AuthNone   = 0x00
	SOCKS5CmdConnect = 0x01
	SOCKS5AddrIPv4   = 0x01
	SOCKS5AddrDomain = 0x03

	CmdRegister      = 0x80
	CmdProxyRequest  = 0x81
	CmdProxyResponse = 0x82
)

type ClientInfo struct {
	ClientID    string
	RoutePrefix string
	Conn        net.Conn
	LastActive  time.Time
	Mutex       sync.Mutex
	ctx         chan struct{}
}

var (
	clientMap      sync.Map
	routeClientMap sync.Map
	reqPool        sync.Map
	hbTimeout      = 120 * time.Second
)

type ProxyTask struct {
	ReqId  string              `json:"reqId"`
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

type ProxyResp struct {
	ReqId  string              `json:"reqId"`
	Status int                 `json:"status"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

func heartbeatChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		clientMap.Range(func(k, v any) bool {
			cli := v.(*ClientInfo)
			if now.Sub(cli.LastActive) > hbTimeout {
				close(cli.ctx)
				cli.Conn.Close()
				if cli.RoutePrefix != "" {
					routeClientMap.Delete(cli.RoutePrefix)
				}
				clientMap.Delete(k)
				fmt.Printf("[超时清理] 客户端 %s 超时，断开\n", cli.ClientID)
			}
			return true
		})
	}
}

func readBody(c *gin.Context) string {
	data, _ := io.ReadAll(c.Request.Body)
	_ = c.Request.Body.Close()
	c.Request.Body = io.NopCloser(strings.NewReader(string(data)))
	return string(data)
}

func writeMessage(conn net.Conn, cmd byte, data []byte) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	length := uint32(len(data))
	buf := make([]byte, 1+4+length)
	buf[0] = cmd
	binary.BigEndian.PutUint32(buf[1:5], length)
	copy(buf[5:], data)
	_, err := conn.Write(buf)
	return err
}

func readMessage(conn net.Conn) (byte, []byte, error) {
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, nil, err
	}
	cmd := header[0]
	length := binary.BigEndian.Uint32(header[1:5])
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return 0, nil, err
	}
	return cmd, data, nil
}

func handleClient(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	if buf[0] != SOCKS5Version {
		return
	}

	conn.Write([]byte{SOCKS5Version, SOCKS5AuthNone})

	n, err = conn.Read(buf)
	if err != nil {
		return
	}

	if buf[0] != SOCKS5Version || buf[1] != CmdRegister {
		fmt.Println("不支持的命令类型")
		return
	}

	clientIdLen := int(buf[2])
	if clientIdLen == 0 || clientIdLen+3 > n {
		return
	}
	clientId := string(buf[3 : 3+clientIdLen])

	routePrefixLen := int(buf[3+clientIdLen])
	routePrefix := "/llm"
	if routePrefixLen > 0 && 3+clientIdLen+routePrefixLen <= n {
		routePrefix = string(buf[3+clientIdLen+1 : 3+clientIdLen+1+routePrefixLen])
	}
	if !strings.HasPrefix(routePrefix, "/") {
		routePrefix = "/" + routePrefix
	}

	ctx := make(chan struct{})
	cli := &ClientInfo{
		ClientID:    clientId,
		RoutePrefix: routePrefix,
		Conn:        conn,
		LastActive:  time.Now(),
		ctx:         ctx,
	}

	routeClientMap.Store(routePrefix, cli)
	clientMap.Store(clientId, cli)
	fmt.Printf("[注册] 客户端 %s 路由前缀 %s\n", clientId, routePrefix)

	conn.Write([]byte{SOCKS5Version, 0x00, 0x00, SOCKS5AddrIPv4, 0, 0, 0, 0, 0, 0})

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx:
				return
			case <-ticker.C:
				cli.Mutex.Lock()
				cli.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				cli.Conn.Write([]byte{SOCKS5Version, 0xFF})
				cli.Mutex.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx:
			return
		default:
			cmd, data, err := readMessage(conn)
			if err != nil {
				if cli.RoutePrefix != "" {
					routeClientMap.Delete(cli.RoutePrefix)
				}
				clientMap.Delete(clientId)
				fmt.Printf("[断开] 客户端 %s 连接关闭\n", clientId)
				return
			}

			cli.LastActive = time.Now()

			switch cmd {
			case CmdProxyResponse:
				var resp ProxyResp
				if json.Unmarshal(data, &resp) == nil {
					if val, ok := reqPool.Load(resp.ReqId); ok {
						ch := val.(chan ProxyResp)
						ch <- resp
					}
				}
			case 0xFF:
				fmt.Printf("[心跳] 客户端 %s\n", clientId)
			}
		}
	}
}

func QueryClientStatus(c *gin.Context) {
	cid := c.Query("clientId")
	val, ok := clientMap.Load(cid)
	if !ok {
		c.JSON(200, gin.H{
			"clientId": cid,
			"online":   false,
		})
		return
	}
	cli := val.(*ClientInfo)
	c.JSON(200, gin.H{
		"clientId":    cid,
		"online":      true,
		"routePrefix": cli.RoutePrefix,
		"lastActive":  cli.LastActive.Format(time.RFC3339),
		"delaySecond": time.Since(cli.LastActive).Seconds(),
	})
}

func ProxyHandler(c *gin.Context) {
	path := c.Request.URL.Path
	var targetClient *ClientInfo
	var matchedPrefix string

	routeClientMap.Range(func(k, v any) bool {
		prefix := k.(string)
		if strings.HasPrefix(path, prefix) {
			targetClient = v.(*ClientInfo)
			matchedPrefix = prefix
			return false
		}
		return true
	})

	if targetClient == nil {
		c.JSON(404, gin.H{"code": 1, "msg": "未找到匹配的路由代理"})
		return
	}

	innerPath := strings.TrimPrefix(path, matchedPrefix)
	if innerPath == "" {
		innerPath = "/"
	}

	query := c.Request.URL.RawQuery
	body := readBody(c)

	reqId := fmt.Sprintf("%d", time.Now().UnixNano())
	task := ProxyTask{
		ReqId:  reqId,
		Method: c.Request.Method,
		Path:   innerPath,
		Query:  query,
		Header: c.Request.Header,
		Body:   body,
	}

	taskJson, err := json.Marshal(task)
	if err != nil {
		c.JSON(500, gin.H{"code": 1, "msg": "打包任务失败"})
		return
	}

	targetClient.Mutex.Lock()
	err = writeMessage(targetClient.Conn, CmdProxyRequest, taskJson)
	targetClient.Mutex.Unlock()
	if err != nil {
		if targetClient.RoutePrefix != "" {
			routeClientMap.Delete(targetClient.RoutePrefix)
		}
		clientMap.Delete(targetClient.ClientID)
		c.JSON(503, gin.H{"code": 1, "msg": "推送任务失败，客户端连接已断开"})
		return
	}

	resChan := make(chan ProxyResp, 1)
	reqPool.Store(reqId, resChan)
	defer reqPool.Delete(reqId)

	select {
	case resp := <-resChan:
		for k, vs := range resp.Header {
			for _, v := range vs {
				c.Header(k, v)
			}
		}
		c.Data(resp.Status, "application/json", []byte(resp.Body))
	case <-time.After(120 * time.Second):
		c.JSON(504, gin.H{"code": 1, "msg": "内网接口请求超时"})
	}
}

var ServerPort string
var SocksPort string

type serverConfig struct {
	ServerPort string `yaml:"server_port"`
	SocksPort  string `yaml:"socks_port"`
}

func loadServerConfig() {
	var config serverConfig
	data, err := os.ReadFile("serverConfig.yaml")
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		ServerPort = "9082"
		SocksPort = "9083"
		return
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		fmt.Printf("解析配置失败: %v\n", err)
		ServerPort = "9082"
		SocksPort = "9083"
		return
	}
	ServerPort = config.ServerPort
	SocksPort = config.SocksPort
}

func main() {
	loadServerConfig()
	go heartbeatChecker()

	go func() {
		listener, err := net.Listen("tcp", ":"+SocksPort)
		if err != nil {
			fmt.Printf("SOCKS5监听失败: %v\n", err)
			return
		}
		fmt.Printf("SOCKS5代理监听端口: %s\n", SocksPort)
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			go handleClient(conn)
		}
	}()

	r := gin.Default()
	r.GET("/api/client/status", QueryClientStatus)
	r.NoRoute(func(c *gin.Context) {
		ProxyHandler(c)
	})

	fmt.Println("HTTP服务端启动 :" + ServerPort)
	_ = r.Run(":" + ServerPort)
}
