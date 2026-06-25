package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	SOCKS5Version    = 0x05
	SOCKS5AuthNone   = 0x00
	CmdRegister      = 0x80
	CmdProxyRequest  = 0x81
	CmdProxyResponse = 0x82
)

var ServerHost string
var ServerPort string
var ClientURL string
var ClientID string
var RoutePrefix string
var AuthKey string

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

func execLocalTask(task ProxyTask) ProxyResp {
	fullUrl := ClientURL + task.Path
	if task.Query != "" {
		fullUrl += "?" + task.Query
	}

	var bodyReader io.Reader
	if task.Body != "" {
		bodyReader = strings.NewReader(task.Body)
	}
	req, err := http.NewRequest(task.Method, fullUrl, bodyReader)
	if err != nil {
		return ProxyResp{
			ReqId:  task.ReqId,
			Status: 500,
			Body:   fmt.Sprintf("构造本地请求失败: %v", err),
		}
	}
	for k, vs := range task.Header {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	client := http.Client{Timeout: 110 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ProxyResp{
			ReqId:  task.ReqId,
			Status: 500,
			Body:   fmt.Sprintf("本地接口调用失败: %v", err),
		}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	headerMap := make(map[string][]string)
	for k, v := range resp.Header {
		if strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
			continue
		}
		headerMap[k] = v
	}

	return ProxyResp{
		ReqId:  task.ReqId,
		Status: resp.StatusCode,
		Header: headerMap,
		Body:   string(bodyBytes),
	}
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

func connectAndRegister() (net.Conn, error) {
	addr := fmt.Sprintf("%s:%s", ServerHost, ServerPort)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}

	conn.Write([]byte{SOCKS5Version, 0x01, SOCKS5AuthNone})

	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return nil, err
	}
	if buf[0] != SOCKS5Version || buf[1] != SOCKS5AuthNone {
		conn.Close()
		return nil, fmt.Errorf("认证失败")
	}

	idLen := len(ClientID)
	prefixLen := len(RoutePrefix)
	authKeyLen := len(AuthKey)
	req := make([]byte, 3+idLen+1+prefixLen+1+authKeyLen)
	req[0] = SOCKS5Version
	req[1] = CmdRegister
	req[2] = byte(idLen)
	copy(req[3:3+idLen], ClientID)
	req[3+idLen] = byte(prefixLen)
	copy(req[3+idLen+1:3+idLen+1+prefixLen], RoutePrefix)
	req[3+idLen+1+prefixLen] = byte(authKeyLen)
	copy(req[3+idLen+1+prefixLen+1:], AuthKey)

	conn.Write(req)

	buf = make([]byte, 10)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return nil, err
	}
	if buf[1] != 0x00 {
		conn.Close()
		if buf[1] == 0x01 {
			return nil, fmt.Errorf("认证失败：密钥不匹配")
		}
		return nil, fmt.Errorf("注册失败")
	}

	fmt.Printf("注册成功，客户端ID: %s，路由前缀: %s\n", ClientID, RoutePrefix)
	return conn, nil
}

func handleTasks(conn net.Conn) {
	for {
		cmd, data, err := readMessage(conn)
		if err != nil {
			fmt.Printf("读取消息失败: %v\n", err)
			return
		}

		switch cmd {
		case CmdProxyRequest:
			var task ProxyTask
			if err := json.Unmarshal(data, &task); err != nil {
				continue
			}
			fmt.Printf("收到代理任务 method=%s path=%s\n", task.Method, task.Path)
			go func(t ProxyTask) {
				respData := execLocalTask(t)
				jsonData, _ := json.Marshal(respData)
				writeMessage(conn, CmdProxyResponse, jsonData)
			}(task)
		case 0xFF:
			conn.Write([]byte{SOCKS5Version, 0xFF})
		}
	}
}

type Config struct {
	ServerURL   string `yaml:"server_url"`
	ClientURL   string `yaml:"client_url"`
	ClientID    string `yaml:"client_id"`
	RoutePrefix string `yaml:"route_prefix"`
	AuthKey     string `yaml:"auth_key"`
}

func loadConfig() bool {
	var config Config
	data, err := os.ReadFile("clientConfig.yaml")
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		return false
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		fmt.Printf("解析配置失败: %v\n", err)
		return false
	}

	u, err := url.Parse(config.ServerURL)
	if err != nil {
		fmt.Printf("解析服务端URL失败: %v\n", err)
		return false
	}
	ServerHost = u.Hostname()
	ServerPort = u.Port()
	if ServerPort == "" {
		if u.Scheme == "https" {
			ServerPort = "443"
		} else {
			ServerPort = "80"
		}
	}

	ClientURL = config.ClientURL
	ClientID = config.ClientID
	if ClientID == "" {
		ClientID = fmt.Sprintf("client-%d", time.Now().Unix())
	}
	RoutePrefix = config.RoutePrefix
	if RoutePrefix == "" {
		RoutePrefix = "/llm"
	}
	if !strings.HasPrefix(RoutePrefix, "/") {
		RoutePrefix = "/" + RoutePrefix
	}
	AuthKey = config.AuthKey
	if AuthKey == "" {
		fmt.Println("警告: 未配置认证密钥，建议设置 auth_key 以提高安全性")
	}
	return true
}

func main() {
	if !loadConfig() {
		fmt.Println("配置加载失败")
		return
	}
	fmt.Printf("客户端启动，目标服务端: %s:%s，本地目标: %s\n", ServerHost, ServerPort, ClientURL)

	for {
		conn, err := connectAndRegister()
		if err != nil {
			fmt.Printf("连接失败: %v，5s后重试\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		handleTasks(conn)
		conn.Close()
		fmt.Println("连接断开，5s后重连")
		time.Sleep(5 * time.Second)
	}
}
