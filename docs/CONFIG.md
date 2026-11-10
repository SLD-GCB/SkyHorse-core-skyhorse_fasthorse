# 配置参考

分三套配置：协议库（server/client JSON）、sing-box 内核（`type: "skyhorse"`）、面板。

## 1. 服务端（tianma/internal/server）

```json
{
  "listen": "0.0.0.0",
  "port": 4433,
  "certificate_path": "/etc/skyhorse/cert.pem",
  "key_path": "/etc/skyhorse/key.pem",
  "obfs_password": "master-secret",
  "obfs_rotation_seconds": 0,
  "obfs_tolerance_seconds": 0,
  "users": [
    { "name": "alice", "uuid": "alice-1", "password": "secret" }
  ]
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `::` | 监听地址 |
| `port` | 必填 | UDP 端口 |
| `certificate_path` / `key_path` | 必填 | TLS 证书 |
| `obfs_password` | 空 | 空 = 不混淆 |
| `obfs_rotation_seconds` | 0 | 0 = 静态密钥 |
| `obfs_tolerance_seconds` | 0 | 接受前后多少秒的窗口 |
| `up_mbps` | 0 | 服务端接收上限（限制客户端上传） |
| `down_mbps` | 0 | 服务端发送上限（限制客户端下载） |
| `ignore_client_bandwidth` | false | 忽略客户端声明，强制用 BBR |
| `congestion` | 空 | 保留字段 |
| `bbr_profile` | `standard` | `conservative` / `standard` / `aggressive` |
| `disable_loss_compensation` | false | Brutal 不做丢包补偿 |
| `users` | 空 | UUID + 密码，任一为空会被跳过 |

### 目的地策略 `policy`

| 字段 | 默认 | 说明 |
|---|---|---|
| `allow_private` | false | 允许 10/8、172.16/12、192.168/16、100.64/10 |
| `allow_loopback` | false | 允许 127/8、::1 |
| `allowed_ports` | 空 | 非空时只允许这些端口 |
| `denied_cidrs` | 空 | 优先于允许 |
| `allowed_cidrs` | 空 | 非空时只允许这些网段 |

域名会先解析，所有解析结果都检查，再用解析出的 IP 拨号。

### 滥用边界 `guard`

| 字段 | 默认 |
|---|---|
| `max_connections_per_ip` | 64 |
| `max_auth_failures` | 20 |
| `auth_failure_window_seconds` | 60 |
| `auth_block_seconds` | 300 |
| `max_streams_per_connection` | 256 |
| `max_udp_sessions_per_connection` | 256 |

失败记录每分钟清理一次。

### 配额 `quota`

| 字段 | 默认 | 说明 |
|---|---|---|
| `bytes_per_period` | 0 | 0 = 不限 |
| `period_seconds` | 30 天 | 周期 |
| `max_rate_bps` | 0 | 0 = 不限速；非 0 才启用令牌桶 |

### 其他

| 字段 | 默认 | 说明 |
|---|---|---|
| `max_memory_mb` | 0 | 0 = 不限制；每条 TCP relay 预留 64 KiB |
| `masquerade.type` | `404` | `404` / `string` / `file` / `proxy` |
| `masquerade.content` | 空 | `string` 的 HTML |
| `masquerade.status_code` | 200 | `string` 的状态码 |
| `masquerade.headers` | 空 | `string` 的响应头，值可为字符串或数组 |
| `masquerade.directory` | 空 | `file` 的目录 |
| `masquerade.url` | 空 | `proxy` 的目标地址 |
| `masquerade.rewrite_host` | false | `proxy` 是否改写 Host |

## 2. 客户端（tianma/internal/client）

```json
{
  "server": "1.2.3.4",
  "server_port": 4433,
  "server_name": "localhost",
  "pin": "<hex SPKI SHA-256>",
  "uuid": "alice-1",
  "password": "secret",
  "obfs_password": "master-secret",
  "up_mbps": 100,
  "down_mbps": 100
}
```

| 字段 | 说明 |
|---|---|
| `server` / `servers` | 单入口或多入口；`servers` 优先 |
| `server_name` | TLS SNI |
| `pin` | hex 编码的 SPKI SHA-256；为空时必须 `insecure: true` |
| `insecure` | 跳过证书校验 |
| `obfs_password` | 必须与服务端一致 |
| `obfs_rotation_seconds` / `obfs_tolerance_seconds` | 必须与服务端一致 |
| `up_mbps` / `down_mbps` | 声明带宽，触发 Brutal |
| `bbr_profile` | 走 BBR 时生效 |

## 3. sing-box 内核（`type: "skyhorse"`）

### 入站

```json
{
  "type": "skyhorse",
  "tag": "sky-in",
  "listen": "0.0.0.0",
  "listen_port": 4433,
  "users": [{ "name": "alice", "uuid": "alice-1", "password": "secret" }],
  "obfs_password": "master-secret",
  "obfs_rotation_seconds": 0,
  "policy": { "allow_private": false, "allow_loopback": false },
  "guard": { "max_connections_per_ip": 64 },
  "quota": { "bytes_per_period": 0 },
  "max_memory_mb": 256,
  "masquerade": { "type": "404" },
  "tls": {
    "enabled": true,
    "certificate_path": "/etc/skyhorse/cert.pem",
    "key_path": "/etc/skyhorse/key.pem"
  }
}
```

### 出站

```json
{
  "type": "skyhorse",
  "tag": "proxy",
  "server": "1.2.3.4",
  "server_port": 4433,
  "uuid": "alice-1",
  "password": "secret",
  "obfs_password": "master-secret",
  "obfs_rotation_seconds": 0,
  "up_mbps": 100,
  "down_mbps": 100,
  "tls": {
    "enabled": true,
    "server_name": "localhost",
    "certificate_public_key_sha256": ["<base64 SPKI>"]
  }
}
```

自签场景把 pin 换成 `"insecure": true`。内核会忽略 `tls.alpn` 和 `tls.min/max_version`，固定用 TLS 1.3 + `h3`。

## 4. 面板

面板的 `skyhorse` 入站有独立表单字段，保存时写入：

- 顶层：`users`、`obfs_password`、`up_mbps`、`down_mbps`；
- `extra`：`obfs_rotation_seconds`、`obfs_tolerance_seconds`、`policy`、`masquerade`、`guard`、`quota`。

面板订阅会把 `obfs_rotation_seconds` / `obfs_tolerance_seconds` 透传给客户端；如果面板旧版本没有透传，服务端开轮换后所有客户端都会超时。

## 5. 默认值速查

| 项 | 默认 |
|---|---|
| 轮换 | 关闭 |
| 伪装 | 404 |
| 私网/回环 | 拒绝 |
| 连接/IP | 64 |
| 认证失败 | 20 次/分钟，封 5 分钟 |
| 流/连接 | 256 |
| UDP 会话/连接 | 256 |
| 内存水位 | 不限 |
| 配额 | 不限 |
