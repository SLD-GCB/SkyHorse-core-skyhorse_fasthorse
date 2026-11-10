# SkyHorse（天马）第三代

天马是一个基于 **原生 QUIC/UDP** 的代理协议。第三代彻底移除了 HTTP/3，改用：

```
QUIC v1 + TLS 1.3
├── QUIC 双向流：SOCKS5 TCP CONNECT
└── QUIC DATAGRAM：SOCKS5 UDP
```

外层可用 Salamander 混淆轮换 + 前置门，隐藏 QUIC 指纹并抗主动探测。

> 天马不与 Hysteria 2 互通，也不是它的分支。

## 组成

| 目录 | 内容 |
|---|---|
| [`tianma/`](tianma) | 协议核心：QUIC 客户端、SOCKS5、DATAGRAM、OBFS 轮换、门控、策略、限流 |
| [`singbox/`](singbox) | sing-box 适配层：`type: "skyhorse"` 入站/出站 |
| [`docs/`](docs) | 协议、适配、配置、构建、安全、排障文档 |

## 特性

- **传输**：QUIC v1 + TLS 1.3，不再使用 HTTP/3
- **TCP**：QUIC 双向流 + 标准 SOCKS5 CONNECT
- **UDP**：QUIC DATAGRAM + 标准 SOCKS5 UDP 头，不进入可靠流
- **认证**：TLS exporter token，通过 SOCKS5 用户名/密码传递，绑定连接
- **混淆**：Salamander（BLAKE2b），多密钥轮换，服务端接受新旧密钥
- **前置门**：HMAC + 时间戳 + nonce，未认证直接丢弃
- **连接池**：多连接、自动扩容、坏连接隔离
- **端口**：端口段多监听、端口跳跃（新建连接换端口，不打断已有流）
- **治理**：目的地策略、每 IP 连接数、认证失败封禁、BBR/Brutal

## 快速开始

```bash
cd singbox
go build -ldflags "-s -w" -o skyhorse ./cmd/skyhorse
./skyhorse check -c server.json
./skyhorse run -c server.json
```

测试：

```bash
cd tianma && go test ./...
cd singbox && go test ./... -count=1
```

## 文档

- [协议规范](docs/PROTOCOL-QUIC.md)
- [内核与面板适配](docs/ADAPTER.md)
- [配置](docs/CONFIG.md)
- [部署](docs/DEPLOY.md)
- [构建](docs/BUILD.md)
- [安全模型](docs/SECURITY.md)
- [故障排查](docs/TROUBLESHOOTING.md)
- [天马 / 快马面板配置指南](tianma/PANEL-CONFIG-GUIDE.md)

## 状态

| 项 | 状态 |
|---|---|
| 协议核心 | 原生 QUIC，可用 |
| sing-box 内核 | 可用（Windows / Linux / Android） |
| TCP | 已验证 |
| UDP | 已验证 |
| OBFS 轮换 | 已验证 |
| 真实弱网 / DPI | 未充分验证 |

## 已知限制

- 链路仍然是 UDP，无法隐藏 IP 协议号；
- UDP 会话各自占一条 QUIC 连接，会话数受 `max_connections` 限制；
- `failure_mode` 字段存在但未差异化执行；
- `obfs_tolerance_seconds` 未做时间窗口校验；
- 无多服务器集群、无 ECH；
- 不与 Hysteria 2 互通。

## 许可

本仓库以 **AGPL-3.0** 发布，见 [LICENSE](LICENSE) 与 [NOTICE](NOTICE)。

部分拥塞控制实现移植自 Hysteria（MIT）；内核适配层基于 sing-box（GPL-3.0）。
