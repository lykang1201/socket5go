# RFRP - 路由转发内网穿透

基于 SOCKS5 协议扩展的轻量级内网穿透工具，支持多客户端通过路由前缀共享一个服务端口。

## 功能特性

- **路由前缀映射**：一个 HTTP 端口支持多个客户端，通过路径前缀自动路由
- **密钥认证**：防止未授权客户端使用隧道
- **SOCKS5 协议**：基于标准协议，网络兼容性好
- **自动重连**：客户端断线后自动重连
- **心跳检测**：自动清理不活跃客户端
- **并发支持**：高并发请求处理

## 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                        服务端 (Server)                          │
│                                                                 │
│  ┌──────────────┐    ┌─────────────────────────────────────┐   │
│  │  HTTP 端口   │    │         SOCKS5 端口 (9083)          │   │
│  │   9082       │    │                                     │   │
│  └──────┬───────┘    └───────────────┬─────────────────────┘   │
│         │                            │                         │
│         │ 路由匹配                    │ 客户端注册              │
│         │                            │                         │
│         ▼                            ▼                         │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              routeClientMap (路由 → 客户端映射)           │   │
│  │  /llm → ClientA   /api → ClientB   /web → ClientC        │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│  客户端 A     │    │  客户端 B     │    │  客户端 C     │
│  route=/llm   │    │  route=/api   │    │  route=/web   │
│  目标:11434   │    │  目标:8080    │    │  目标:3000    │
└───────────────┘    └───────────────┘    └───────────────┘
```

## 快速开始

### 1. 编译项目

```bash
# 编译服务端和客户端
go build -o main_server main_server.go
go build -o main_client main_client.go

# 编译 Linux 版本 (Windows 环境)
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -ldflags "-s -w" -o main_server main_server.go
go build -ldflags "-s -w" -o main_client main_client.go
```

### 2. 服务端配置

```bash
# 复制配置文件示例
cp serverConfig.yaml.example serverConfig.yaml

# 编辑配置文件
vim serverConfig.yaml
```

配置内容：

```yaml
server_port: "9082"           # HTTP 请求入口端口
socks_port: "9083"            # SOCKS5 客户端连接端口
auth_key: "your-secret-key"   # 认证密钥（必填）
```

### 3. 客户端配置

```bash
# 复制配置文件示例
cp clientConfig.yaml.example clientConfig.yaml

# 编辑配置文件
vim clientConfig.yaml
```

配置内容：

```yaml
server_url: http://your-server-ip:9083  # 服务端 SOCKS5 地址
client_url: http://127.0.0.1:11434      # 本地服务地址
client_id: local-llm-01                 # 客户端标识
route_prefix: /ollama                   # 路由前缀
auth_key: "your-secret-key"             # 认证密钥（与服务端一致）
```

### 4. 启动服务

**服务端启动：**

```bash
./main_server
```

输出示例：

```
服务端启动 :9082
SOCKS5代理监听端口: 9083
```

**客户端启动：**

```bash
./main_client
```

输出示例：

```
客户端启动，目标服务端: 123.207.20.105:9083，本地目标: http://192.168.3.25:11434
注册成功，客户端ID: local-llm-01，路由前缀: /ollama
```

### 5. 访问内网服务

```bash
# 通过服务端访问内网 Ollama 服务
curl http://your-server:9082/ollama/api/generate \
  -H "Content-Type: application/json" \
  -d '{"model": "llama3", "prompt": "Hello"}'
```

## 工作原理

### 1. 客户端注册流程

客户端通过 SOCKS5 协议连接服务端，携带客户端标识、路由前缀和认证密钥：

```
客户端 → 服务端: [0x05, 0x01, 0x00]          // SOCKS5 握手，无认证
服务端 → 客户端: [0x05, 0x00]               // 同意无认证
客户端 → 服务端: [0x05, 0x80, len(id), id, len(prefix), prefix, len(key), key]  // 注册命令 + 密钥
服务端 → 客户端: [0x05, 0x00, ...]          // 注册成功
```

**认证机制：**
- 客户端在注册时发送 `auth_key` 密钥
- 服务端验证密钥是否匹配配置文件中的密钥
- 密钥不匹配时返回 `[0x05, 0x01, ...]` 拒绝注册
- 防止未授权客户端使用隧道

### 2. 请求转发流程

外部 HTTP 请求通过服务端路由匹配转发到对应客户端：

```
外部请求 → 服务端: GET /ollama/api/generate
         │
         ▼
   路由匹配: /ollama → ClientA
         │
         ▼
   服务端 → ClientA: [0x81, len(json), {"reqId": "...", "method": "GET", "path": "/api/generate", ...}]
         │
         ▼
   ClientA → 本地服务: GET http://127.0.0.1:11434/api/generate
         │
         ▼
   ClientA → 服务端: [0x82, len(json), {"reqId": "...", "status": 200, "body": "..."}]
         │
         ▼
   服务端 → 外部请求: HTTP 200 OK + response body
```

### 3. 心跳机制

服务端定时发送心跳检测，确保客户端在线：

```
服务端 → 客户端: [0x05, 0xFF]  // 心跳请求 (每60秒)
客户端 → 服务端: [0x05, 0xFF]  // 心跳响应
```

- 客户端超时时间：120秒
- 服务端检测间隔：30秒
- 超时自动清理客户端连接和路由映射

## 配置说明

### 服务端配置 (serverConfig.yaml)

| 配置项 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| server_port | string | 是 | HTTP 请求入口端口，默认 9082 |
| socks_port | string | 是 | SOCKS5 客户端连接端口，默认 9083 |
| auth_key | string | 是 | 认证密钥，用于验证客户端身份 |

### 客户端配置 (clientConfig.yaml)

| 配置项 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| server_url | string | 是 | 服务端 SOCKS5 地址，格式: http://IP:端口 |
| client_url | string | 是 | 本地服务地址，格式: http://IP:端口 |
| client_id | string | 是 | 客户端唯一标识 |
| route_prefix | string | 是 | 路由前缀，如 /ollama, /api, /web |
| auth_key | string | 是 | 认证密钥，必须与服务端一致 |

**密钥生成建议：**

```bash
# 使用 openssl 生成强随机密钥
openssl rand -hex 16

# 示例输出
# 5f4dcc3b5aa765d61d8327deb882cf99
```

## 多客户端支持

不同客户端注册不同路由前缀，共享同一个服务端口：

```
客户端 A 注册 /ollama    → 转发到本地 11434 (Ollama)
客户端 B 注册 /api       → 转发到本地 8080 (API 服务)
客户端 C 注册 /web       → 转发到本地 3000 (Web 服务)
```

外部请求自动根据路径前缀路由到对应客户端：

```
http://server:9082/ollama/* → 客户端 A
http://server:9082/api/*    → 客户端 B
http://server:9082/web/*    → 客户端 C
```

## 防火墙配置

服务端需要开放两个端口：

```bash
# 开放 HTTP 端口和 SOCKS5 端口
sudo ufw allow 9082/tcp
sudo ufw allow 9083/tcp

# 或者使用 iptables
sudo iptables -A INPUT -p tcp --dport 9082 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 9083 -j ACCEPT
```

## 状态查询

查看客户端在线状态：

```bash
curl http://your-server:9082/api/client/status?clientId=local-llm-01
```

返回示例：

```json
{
  "clientId": "local-llm-01",
  "online": true,
  "routePrefix": "/ollama",
  "lastActive": "2024-01-15T10:30:00Z",
  "delaySecond": 5.2
}
```

## 故障排查

### 客户端无法连接

1. **检查网络连接**：确保客户端可以访问服务端
   ```bash
   ping your-server-ip
   ```

2. **检查防火墙**：确保服务端 9083 端口已开放
   ```bash
   telnet your-server-ip 9083
   ```

3. **检查密钥**：确保客户端和服务端的 `auth_key` 一致

4. **查看日志**：查看服务端和客户端的输出日志

### 请求超时

1. **检查本地服务**：确保本地服务正常运行
   ```bash
   curl http://127.0.0.1:11434/api/generate
   ```

2. **检查路由前缀**：确保请求路径匹配客户端注册的路由前缀

3. **检查客户端连接**：确保客户端已成功注册

### 连接断开

1. **检查网络稳定性**：网络不稳定可能导致连接断开
2. **客户端会自动重连**：无需手动干预

## 技术特点

- **SOCKS5 协议扩展**：基于标准协议，兼容性好
- **密钥认证机制**：防止未授权客户端使用隧道
- **路由前缀映射**：一个端口支持多客户端
- **长连接保持**：TCP 长连接，低延迟
- **自动重连**：客户端断线后自动重连
- **并发安全**：互斥锁保护并发写入
- **超时清理**：自动清理不活跃客户端

## 文件结构

```
socket5go/
├── cmd/
│   ├── server/
│   │   └── main_server.go    # 服务端代码
│   └── client/
│       └── main_client.go    # 客户端代码
├── serverConfig.yaml         # 服务端配置（运行时）
├── serverConfig.yaml.example # 服务端配置示例
├── clientConfig.yaml         # 客户端配置（运行时）
├── clientConfig.yaml.example # 客户端配置示例
├── build.bat                 # Windows 编译脚本
├── go.mod                    # Go 模块依赖
├── go.sum                    # Go 模块校验
├── LICENSE                   # MIT 许可证
├── .gitignore               # Git 忽略文件
└── README.md                # 项目文档
```