# RFRP - Reverse Forward Remote Proxy

基于 Golang 的轻量级内网穿透工具，支持**路径路由**、多端口转发、自动重连、心跳保活等功能。

## 功能特性

- ✅ **路径路由**：通过 HTTP 路径前缀匹配转发到不同内网服务
- ✅ **单端口监听**：服务端只需开放一个公网端口即可转发多个服务
- ✅ **YAML 配置文件**：通过配置文件管理转发规则，简单易用
- ✅ **自动重连**：网络断开后自动重新连接服务端
- ✅ **心跳保活**：定时心跳检测客户端状态
- ✅ **跨平台**：支持 Windows、Linux、macOS
- ✅ **向后兼容**：支持命令行参数和配置文件两种启动方式

## 项目结构

```
rfrp/
├── go.mod                    # Go 模块依赖
├── go.sum                    # 依赖校验文件
├── config.yaml               # 客户端配置文件
├── bin/                      # 编译后的可执行文件
│   ├── server.exe            # 服务端
│   └── client.exe            # 客户端
├── cmd/                      # 命令行入口
│   ├── server/main.go        # 服务端入口
│   └── client/main.go        # 客户端入口
└── internal/                 # 内部模块
    ├── client/client.go      # 客户端核心逻辑
    ├── server/server.go      # 服务端核心逻辑
    ├── config/config.go      # 配置结构体和加载逻辑
    └── protocol/protocol.go  # 通信协议定义
```

## 快速开始

### 1. 编译项目

```bash
# 编译服务端
go build -o bin/server.exe ./cmd/server

# 编译客户端
go build -o bin/client.exe ./cmd/client
```

### 2. 启动服务端

```bash
./bin/server.exe -control :8000 -tunnel :8001 -public :8080
```

### 3. 启动客户端

#### 使用配置文件（推荐）

```bash
./bin/client.exe -config config.yaml
```

## 配置说明

### 服务端参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-control` | 控制连接端口（客户端注册/心跳） | `:8000` |
| `-tunnel` | 隧道连接端口（数据传输） | `:8001` |
| `-public` | 公网 HTTP 监听端口（对外服务） | `:8080` |

### 客户端参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-config` | YAML 配置文件路径 | 空（使用命令行模式） |
| `-control` | 服务端控制地址 | `localhost:8000` |
| `-tunnel` | 服务端隧道地址 | `localhost:8001` |
| `-local` | 本地服务地址（命令行模式） | `localhost:3000` |
| `-id` | 客户端 ID | 自动生成 |

### YAML 配置文件

```yaml
server:
  control_addr: "your-server-ip:8000"
  tunnel_addr: "your-server-ip:8001"

client:
  id: "my_client_001"

forwards:
  - name: "vue_frontend"
    path: "/vue_frontend"
    local_addr: "localhost:3000"
    description: "Vue前端应用"

  - name: "api_service"
    path: "/api"
    local_addr: "localhost:5000"
    description: "API服务"

  - name: "admin_panel"
    path: "/admin"
    local_addr: "localhost:8080"
    description: "管理后台"
```

**配置字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `server.control_addr` | string | 是 | 服务端控制连接地址 |
| `server.tunnel_addr` | string | 是 | 服务端隧道连接地址 |
| `client.id` | string | 否 | 客户端ID，留空自动生成 |
| `forwards[].name` | string | 是 | 转发规则名称 |
| `forwards[].path` | string | 是 | 路径前缀（如 `/vue_frontend`） |
| `forwards[].local_addr` | string | 是 | 本地服务地址（host:port） |
| `forwards[].description` | string | 否 | 规则描述 |

## 使用示例

### 示例1：转发多个 Web 服务

**服务端启动：**
```bash
./bin/server.exe -control :8000 -tunnel :8001 -public :8080
```

**客户端配置文件 `config.yaml`：**
```yaml
server:
  control_addr: "your-server-ip:8000"
  tunnel_addr: "your-server-ip:8001"

forwards:
  - name: "vue_frontend"
    path: "/vue_frontend"
    local_addr: "localhost:3000"

  - name: "api_service"
    path: "/api"
    local_addr: "localhost:5000"

  - name: "admin"
    path: "/admin"
    local_addr: "localhost:8080"
```

**启动客户端：**
```bash
./bin/client.exe -config config.yaml
```

**访问映射：**

| 访问地址 | 转发到本地 |
|----------|------------|
| `http://your-server-ip:8080/vue_frontend` | `localhost:3000` |
| `http://your-server-ip:8080/api/users` | `localhost:5000` |
| `http://your-server-ip:8080/admin/login` | `localhost:8080` |

### 示例2：配合 Nginx 使用

如果你的服务端域名是 `https://testA.com`，可以使用 Nginx 做反向代理：

```nginx
server {
    listen 80;
    server_name testA.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

**访问效果：**

| 访问地址 | 转发到本地 |
|----------|------------|
| `https://testA.com/vue_frontend` | `localhost:3000` |
| `https://testA.com/api/users` | `localhost:5000` |
| `https://testA.com/admin` | `localhost:8080` |

## 工作原理

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   公网用户       │     │    RFRP 服务端    │     │    RFRP 客户端   │
│                 │     │                 │     │                 │
│  GET /api/users │────▶│  解析路径 /api   │────▶│  匹配 path /api  │
│                 │     │  通知客户端       │     │  连接 localhost:5000 │
│                 │◀────│  建立隧道         │◀────│  数据转发        │
│                 │     │                 │     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

1. **客户端注册**：客户端启动后连接服务端控制端口进行注册
2. **心跳保活**：服务端定时发送心跳检测客户端在线状态
3. **路径解析**：当公网请求到达服务端时，解析 HTTP 请求路径
4. **通知客户端**：服务端将路径信息发送给客户端
5. **路径匹配**：客户端根据路径前缀匹配对应的本地服务
6. **数据转发**：客户端建立隧道连接，数据双向转发

## 路径匹配规则

- 使用前缀匹配：`/api/users` 匹配 `path: "/api"`
- 优先匹配最长路径：`/api/v1/users` 优先匹配 `path: "/api/v1"` 而非 `path: "/api"`
- 默认规则：可以配置 `path: "/"` 作为默认转发规则

## 注意事项

1. **防火墙配置**：确保服务端开放以下端口：
   - 控制端口（默认 8000）
   - 隧道端口（默认 8001）
   - 公网 HTTP 端口（默认 8080）

2. **HTTPS**：建议使用 Nginx/Caddy 等反向代理配置 HTTPS，RFRP 服务端只处理 HTTP

3. **路径前缀去除**：当前实现会保留完整路径转发给本地服务。例如访问 `/api/users`，本地服务收到的请求路径也是 `/api/users`

4. **安全建议**：
   - 限制客户端连接来源
   - 设置客户端认证机制
   - 配置防火墙规则

## 技术栈

- Go 1.21+
- github.com/sirupsen/logrus（日志）
- gopkg.in/yaml.v3（YAML 解析）

## License

MIT License