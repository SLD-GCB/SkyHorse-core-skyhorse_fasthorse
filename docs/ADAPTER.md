# SkyHorse 内核与面板适配说明

版本：`1.14.4-skyhorse`

> 迁移中：H3 已决定废弃，协议目标见 [PROTOCOL-QUIC.md](PROTOCOL-QUIC.md)。  
> 本文描述的内核字段在迁移后保持兼容，但 H3 相关实现会被原生 QUIC 替换。

本文说明协议如何映射到 sing-box 内核（`type: "skyhorse"`）、面板表单和订阅生成。  
协议细节见 [PROTOCOL-H3.md](PROTOCOL-H3.md)。

## 1. 分层

```
协议库 tianma/boxapi
    ↓
sing-box 适配层 singbox/
    ↓
面板配置 model.Inbound / model.Outbound
    ↓
配置生成 svc/configgen.go、svc/sub.go
```

四层职责：

| 层 | 职责 |
|---|---|
| 协议库 | QUIC/H3、门控、认证、连接池 |
| 适配层 | 解析 sing-box JSON、暴露入站/出站 |
| 面板模型 | 保存表单、Extra、TLS |
| 配置生成 | 拼 sing-box config、生成订阅 |

## 2. 入站字段

### 2.1 原生字段

```json
{
  "type": "skyhorse",
  "tag": "sky-10000",
  "listen": "::",
  "listen_port": 10000,
  "users": [
    { "name": "alice", "uuid": "uuid", "password": "password" }
  ],
  "tls": {
    "enabled": true,
    "server_name": "localhost",
    "certificate_path": "/path/cert.pem",
    "key_path": "/path/key.pem"
  },
  "obfs_password": "gate-secret",
  "obfs_keys": ["old-secret"],
  "node_id": "hk-01",
  "port_range": "10000:10010",
  "failure_mode": "drop",
  "connections": 4,
  "max_connections": 16,
  "max_streams_per_connection": 32,
  "port_rotate_seconds": 300
}
```

### 2.2 字段状态

| 字段 | 内核接收 | 实际行为 |
|---|---|---|
| `listen` / `listen_port` | 是 | 监听单个 UDP 端口 |
| `users` | 是 | UUID + 密码认证 |
| `tls` | 是 | 证书/私钥 |
| `obfs_password` | 是 | 门控密钥，和 `node_id` 组合 |
| `obfs_keys` | 是 | 接收，但当前门控只用 `obfs_password` |
| `node_id` | 是 | 参与门控密钥 |
| `port_range` | 是 | 支持 `start:end`、`-` 或逗号列表，启动多个 UDP/QUIC 监听 |
| `failure_mode` | 是 | 只保存，当前未认证统一关闭 |
| `connections` | 是 | 服务端不使用；作为生成客户端出站的默认值 |
| `max_connections` | 是 | 服务端全局活跃连接上限，超过直接关闭 |
| `max_streams_per_connection` | 是 | 写入 QUIC `MaxIncomingStreams` |
| `port_rotate_seconds` | 是 | 服务端不使用；作为生成客户端出站的默认值 |

### 2.3 新增字段的兼容原则

入站四个连接池字段必须写在 `InboundOptions` 里，不能只放 `Extra`：

- `connections`
- `max_connections`
- `max_streams_per_connection`
- `port_rotate_seconds`

否则内核反序列化时可能视为未知字段，或者只能被面板使用、服务端完全不认识。

## 3. 出站字段

```json
{
  "type": "skyhorse",
  "tag": "sky-out",
  "server": "1.2.3.4",
  "server_port": 10000,
  "uuid": "uuid",
  "password": "password",
  "obfs_password": "gate-secret",
  "node_id": "hk-01",
  "connections": 4,
  "max_connections": 16,
  "max_streams_per_connection": 32,
  "port_rotate_seconds": 300,
  "server_ports": ["10000", "10001"],
  "tls": {
    "enabled": true,
    "server_name": "localhost",
    "insecure": true
  }
}
```

### 3.1 字段状态

| 字段 | 默认 | 行为 |
|---|---|---|
| `server` | 必填 | 服务端 IP 或域名 |
| `server_port` | 必填 | 服务端端口 |
| `uuid` / `password` | 必填 | 用户凭据 |
| `obfs_password` | 空 | 门控密码，必须和入站一致 |
| `node_id` | 空 | 门控节点 ID，必须和入站一致 |
| `connections` | 4 | 初始 H3 连接数 |
| `max_connections` | 16 | 自动扩容上限 |
| `max_streams_per_connection` | 32 | 单连接流上限 |
| `port_rotate_seconds` | 300 | 空闲连接端口轮换周期；填 0 仍按 300 |
| `server_ports` | 空 | 端口列表；非空时覆盖 `server_port` |
| `prewarm` | false | 接收但当前 H3 不预热 |
| `transport` | 空 | 已强制 H3，字段无意义 |
| `tls.insecure` | false | 自签时跳过校验 |
| `tls.certificate_public_key_sha256` | 空 | 自签 pin |

### 3.2 端口规则

`server_ports` 必须是端口列表：

```json
["10000", "10001", "10002"]
```

不能写范围：

```json
["10000:10010"]
```

范围会被当成一个非法端口。

当前服务端只监听 `listen_port`，所以客户端 `server_ports` 只能填实际监听的端口。

## 4. 面板字段映射

### 4.1 入站表单

| 面板字段 | JSON 字段 | 写入位置 |
|---|---|---|
| 门控密码 | `obfs_password` | 顶层 |
| 端口段 | `port_range` | Extra |
| 节点 ID | `node_id` | Extra |
| 失败行为 | `failure_mode` | Extra |
| 旧门控密钥 | `obfs_keys` | Extra |
| 初始连接数 | `connections` | Extra |
| 最大连接数 | `max_connections` | Extra |
| 单连接流上限 | `max_streams_per_connection` | Extra |
| 端口轮换 | `port_rotate_seconds` | Extra |
| 允许私网/回环 | `policy` | Extra |

入站表单字段保存后，配置生成会：

1. 把 `Extra` 合并进入站对象；
2. 把连接池字段作为生成客户端出站的默认值；
3. `max_connections` 和 `max_streams_per_connection` 同时被内核入站使用。

### 4.2 出站表单

| 面板字段 | JSON 字段 | 写入位置 |
|---|---|---|
| 服务器 | `server` | 顶层 |
| 端口 | `server_port` | 顶层 |
| UUID | `uuid` | Config |
| 密码 | `password` | Config |
| 门控密码 | `obfs_password` | Config |
| 节点 ID | `node_id` | Extra |
| 初始连接数 | `connections` | Extra |
| 最大连接数 | `max_connections` | Extra |
| 单连接流上限 | `max_streams_per_connection` | Extra |
| 端口轮换 | `port_rotate_seconds` | Extra |
| 端口段 | `server_ports` | Extra |
| 旧门控密钥 | `obfs_keys` | Extra |

出站 `buildOutbound` 的合并顺序：

```
Config → TLS → Transport → Multiplex → Dial → Extra
```

后者覆盖前者。

## 5. 面板生成客户端出站

`svc/sub.go` 的 SkyHorse 分支会读取入站 `Extra`：

```go
for _, key := range []string{
    "connections", "max_connections", "max_streams_per_connection",
    "port_rotate_seconds", "server_ports", "node_id", "obfs_keys",
} {
    if v, ok := extra[key]; ok {
        o[key] = v
    }
}
```

因此：

- 入站填了连接池，客户端订阅会带上；
- 入站没填，客户端使用内核默认值；
- 出站单独配置可以覆盖订阅值。

## 6. 订阅与分享链接

当前 SkyHorse 的分享链接生成返回空字符串。

这意味着：

- 不能通过普通 `skyhorse://` 链接导入；
- 客户端需要直接使用面板下发的 sing-box 配置；
- 后续如要支持分享链接，必须先定义 URI 规范。

## 7. 已知缺口

以下缺口必须在协议或适配层明确，不能靠面板字段掩盖：

1. 入站 `port_range` 已支持多端口监听，但端口池自动分配仍需面板补全；
2. 入站 `failure_mode` 没有参与拒绝分支；
3. 入站 `obfs_keys` 没有参与门控多密钥轮换；
4. 出站 `prewarm` 没有实现；
5. 出站 `transport` 已废弃；
6. 没有版本协商；
7. 没有真实 DPI 验证；
8. 没有 SkyHorse 分享链接。

## 8. 修改检查清单

任何字段改动必须同时检查：

1. `singbox/options.go` 是否有对应字段；
2. `NewInbound` / `NewOutbound` 是否读取；
3. 内核是否实际执行；
4. 面板 `buildExtra` 是否写入；
5. 面板回显是否读取；
6. `configgen.go` 的合并顺序；
7. `sub.go` 是否透传；
8. 三端内核和面板包是否重打。

只改其中一层，就会出现“面板能填、内核不认识”或“保存成功、回显丢失”。
