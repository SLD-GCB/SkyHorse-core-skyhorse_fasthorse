# 快马 Kuaima

TCP + obfs + 动态门 + TLS 1.3 + H2 CONNECT 的代理协议原型。

## 结构

```
TCP
└── obfs（ChaCha20，时间窗口轮换）
    └── 动态门（时间戳 + nonce + HMAC）
        └── TLS 1.3（自签 + 信任根）
            └── H2 CONNECT（cronet 全双工）
```

## 构建

```bash
# 服务端 / 无 cronet
go build ./cmd/kuaima

# 客户端（需要 libcronet）
go build -tags with_purego ./cmd/kuaima
```

`libcronet` 需要放在可执行文件同目录或 PATH 中。

## 服务端

```json
{
  "listen": "0.0.0.0",
  "port_range": "20000-20002",
  "certificate_path": "/etc/kuaima/cert.pem",
  "key_path": "/etc/kuaima/key.pem",
  "obfs_password": "change-me",
  "obfs_keys": [
    { "id": 1, "password": "old-key-for-rotation" }
  ],
  "obfs_rotation_seconds": 3600,
  "obfs_tolerance_seconds": 3600,
  "obfs_pad_to": 1024,
  "users": [
    { "name": "alice", "password": "change-me-too" }
  ],
  "policy": {
    "allow_private": false,
    "allow_loopback": false,
    "allowed_ports": []
  },
  "guard": {
    "max_connections_per_ip": 64
  },
  "failure_mode": "drop",
  "failure_delay_ms": 1500
}
```

```bash
kuaima serve -c server.json
```

## 客户端

```json
{
  "listen": "127.0.0.1:1080",
  "server_name": "kuaima",
  "username": "alice",
  "password": "change-me-too",
  "certificate_path": "/etc/kuaima/cert.pem",
  "obfs_password": "change-me",
  "obfs_key_id": 0,
  "obfs_rotation_seconds": 3600,
  "obfs_tolerance_seconds": 3600,
  "obfs_pad_to": 1024,
  "connections_per_entry": 2,
  "max_connections": 8,
  "target_mbps": 200,
  "receive_window_mb": 16,
  "port_rotate_seconds": 600,
  "entries": [
    { "server": "1.2.3.4", "port_range": "20000-20002" },
    { "server": "5.6.7.8", "port_range": "20000-20002" }
  ]
}
```

```bash
kuaima client -c client.json
```

客户端在 `127.0.0.1:1080` 提供 SOCKS5 CONNECT。

## 吞吐策略

TCP 不做每连接限速，目标是跑满：

- 每条连接不设速率上限；
- 连接池每秒采样吞吐，按在途流和实测速率选择连接；
- 总吞吐低于 `target_mbps × 85%` 时自动加连接，最高 `max_connections`；
- 总吞吐高于 `target_mbps × 95%` 且有空闲连接时，30 秒后回收；
- H2 接收窗口默认 16 MB，服务端连接/流窗口 3 MB；
- TCP 收发缓冲 4 MB；
- `port_rotate_seconds` 到期后把空闲连接换成同入口的下一个端口，带 10% 抖动；有流在跑的连接先排空，不中断现有连接。

## 已完成

- obfs：ChaCha20、16 KiB 分块、时间窗口轮换；
- 动态门：128 字节记录、HMAC-SHA256、时间窗、nonce 防重放；
- TLS 1.3 + 自签信任根；
- 服务端：端口段监听、Basic 用户校验、目标策略、每 IP 连接数；
- 客户端：cronet H2 全双工、多入口连接池、按在途流选择连接、失败切换；
- CLI：`serve` / `client` / `version`。

## 隐蔽与轮换

- `failure_mode`：`drop`（默认，探测看到超时）、`reset`、`close`；
- `failure_delay_ms`：drop 模式下的基础延迟，再加 0~1 秒随机抖动；
- H2 认证/配额失败不再返回 403/404，直接断流；
- 端口轮换触发：定时、拨号失败立即换、吞吐低于峰值 1/3 持续 5 秒；
- `disable_rotate_on_failure` / `disable_rotate_on_degrade` 可关闭对应触发；
- 混淆密钥带头部版本号，服务端 `obfs_keys` 可同时接受旧版本，客户端 `obfs_key_id` 选当前版本；
- `obfs_pad_to`：开启后所有块按 1 KiB / 4 KiB / 16 KiB 三档补齐，大流量块也统一到 16 KiB，`-1` 关闭；
- 活连接读写错误立即替换连接，不必等下次拨号；
- 服务端每 5 秒检查配置，用户、密钥、配额热加载，已有连接继续用完；
- 服务端启动时校验证书 SAN 与 `server_name` 一致。

## 限制

- 客户端必须 `with_purego` + `libcronet`；
- 自签证书有效期不超过 398 天（当前 90 天）；
- 内层 Basic 是静态密码，动态性由外层门提供；
- 暂无配额和每连接限速；
- 暂无 sing-box 内核适配、面板和订阅。
