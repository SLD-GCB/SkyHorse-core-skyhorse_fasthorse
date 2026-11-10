# SkyHorse Core

天马（SkyHorse）与快马（FastHorse）代理协议的 sing-box 内核源码，包含协议核心、sing-box 适配层、构建脚本和三端产物说明。

## 项目组成

| 目录 | 内容 |
|---|---|
| `singbox/` | sing-box 适配层：`type: "skyhorse"` / `fasthorse` 入站与出站，桌面入口 `cmd/skyhorse` |
| `tianma/` | 天马协议核心：QUIC 客户端/服务端、SOCKS5、DATAGRAM、OBFS 轮换、策略与限速 |
| `kuaima/` | 快马协议核心：TCP/H2 CONNECT、单连接多流、上下行限速 |
| `sing-mux/` | 多路复用依赖 |
| `scripts/` | 内核构建脚本 |
| `docs/` | 协议、配置、适配、部署、安全与排障文档 |

## 协议

### SkyHorse（天马）第四代

天马是基于原生 QUIC/UDP 的代理协议，不走 HTTP/3。

```
QUIC v1 + TLS 1.3
├── TCP：QUIC 双向流 + SOCKS5 CONNECT
└── UDP：QUIC DATAGRAM + SOCKS5 UDP
```

- 认证：TLS exporter token 与当前连接绑定，通过 SOCKS5 用户名/密码传递。
- 混淆：Salamander 多密钥轮换，隐藏 QUIC Initial 与 ClientHello。
- 拥塞：BBR 可选档位；Brutal 使用真实上下行。
- 并发：`max_streams_per_connection` 由客户端信号量强制。
- 端口：端口段多监听，端口轮换默认关闭，避免打断长连接。
- 修复：服务端解析目标域名后优先 IPv4，避免服务器没有 IPv6 路由时 SOCKS 请求失败。

### FastHorse（快马）第二代

快马定位是 UDP 被封时的 TCP 兜底，不追求峰值。

```
TCP + OBFS + 动态门 + TLS 1.3 + H2 CONNECT
```

- 单条 H2 连接 + 原生多流，减少连接池和并发失控。
- 客户端流信号量 + 服务端 `MaxConcurrentStreams` 双重限制。
- `up_mbps` 客户端上行、`down_mbps` 服务端下行，令牌桶实现。
- 端口轮换默认关闭，端口数 <= 1 不轮换。

## 构建

### 桌面端（Windows / Linux）

需要 Go 1.26+。

```powershell
.\scripts\build-kernel.ps1 -Targets windows -OutDir .\kernels
.\scripts\build-kernel.ps1 -Targets linux -OutDir .\kernels
```

构建标签：

```
with_clash_api with_v2ray_api with_quic with_dhcp with_gvisor
```

输出：

```
kernels/SkyHorse.exe
kernels/skyhorse-linux-amd64
kernels/skyhorse-linux-arm64
```

### Android

需要 Android 工程、`gomobile`、JDK、Android SDK 和 NDK：

```powershell
.\scripts\build-kernel.ps1 -Targets android -OutDir .\kernels `
  -AndroidProject D:\github_code\code\Android\project\HorseBox\android-libbox\sing-box
```

输出：

```
kernels/libbox.aar
kernels/libbox-sources.jar
```

Android ABI：`android/arm64,android/amd64`。

## 三端产物

本地构建后的发布结构：

```
SkyHorse-Kernels-1.14.4/
├── windows-amd64/SkyHorse.exe
├── linux-amd64/skyhorse
├── linux-arm64/skyhorse
├── android/libbox.aar
├── android/libbox-sources.jar
├── README.txt
└── SHA256SUMS.txt
```

部署位置：

| 平台 | 文件 | 目标 |
|---|---|---|
| Windows | `SkyHorse.exe` | HorseBox `core/sing-box.exe` |
| Linux amd64 | `skyhorse` | `/etc/horseui/bin/sing-box` |
| Linux arm64 | `skyhorse` | `/etc/horseui/bin/sing-box` |
| Android | `libbox.aar` | `UI/android/app/libs/libbox.aar` |

> `kernels/`、`SkyHorse-Kernels-*/` 和 zip 是本地构建产物，不提交到源码仓库。

## 文档

- [协议规范](docs/PROTOCOL-QUIC.md)
- [内核与面板适配](docs/ADAPTER.md)
- [配置](docs/CONFIG.md)
- [部署](docs/DEPLOY.md)
- [构建](docs/BUILD.md)
- [安全模型](docs/SECURITY.md)
- [故障排查](docs/TROUBLESHOOTING.md)
- [天马 / 快马面板配置](tianma/PANEL-CONFIG-GUIDE.md)


## 许可证与上游声明

- 本仓库原创部分以 **GNU Affero General Public License v3.0（AGPL-3.0）** 发布，完整文本见 [LICENSE](LICENSE)。
- `singbox/` 适配层链接并使用 **sing-box**，Copyright (C) 2022 SagerNet，遵循 **GNU General Public License v3.0（GPL-3.0）**，上游源码：<https://github.com/SagerNet/sing-box>。
- AGPL-3.0 第 13 条提供了与 GPL-3.0 代码结合的许可；sing-box 部分仍遵循 GPL-3.0，相关版权与许可证声明保留在 [NOTICE](NOTICE)。
- `tianma/internal/congestion` 的部分拥塞控制代码来自 Hysteria（MIT），QUIC 传输使用 `github.com/apernet/quic-go`（MIT），详见 [NOTICE](NOTICE)。
- 通过网络使用本项目的修改版时，应按 AGPL-3.0 向用户提供对应源码；分发时请保留本仓库和上游的版权、许可证与 NOTICE。

> 以上是项目许可证说明，不构成法律意见；商业闭源集成前请自行确认 AGPL-3.0 / GPL-3.0 的合规边界。
## 说明

- 天马不与 Hysteria 2 互通，也不是它的分支。
- 自研协议没有公开审计，不能宣称“绝对安全”或“完全隐藏”。
- 服务端内核包含 IPv4 优先修复；客户端内核与 Android libbox 来自同一份源码。
- 替换内核前先停止对应服务或客户端内核，替换后恢复执行权限。

