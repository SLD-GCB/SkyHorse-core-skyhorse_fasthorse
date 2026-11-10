# 天马 / SkyHorse 文档

天马（Tianma）是一个基于 QUIC + HTTP/3 的代理协议，以 Hysteria 2 为设计参考，不是它的分支，也不与它互通。SkyHorse 是把天马编进 sing-box 内核后的产物。

## 文档索引

| 文档 | 内容 |
|---|---|
| [PROTOCOL-QUIC.md](PROTOCOL-QUIC.md) | **迁移目标**：原生 QUIC 流 + QUIC DATAGRAM |
| [PROTOCOL-H3.md](PROTOCOL-H3.md) | 旧 H3 规范，已决定废弃 |
| [ADAPTER.md](ADAPTER.md) | **内核与面板适配**：字段映射、配置生成、已知缺口 |
| [PROTOCOL.md](PROTOCOL.md) | 旧裸流协议，已废弃，仅存档 |
| [CONFIG.md](CONFIG.md) | 配置字段（部分内容已过时，以 ADAPTER.md 为准） |
| [DEPLOY.md](DEPLOY.md) | 面板、Linux 服务端、Windows/Android 客户端、TUN |
| [BUILD.md](BUILD.md) | 从源码构建内核、面板、客户端，含 Android libbox |
| [SECURITY.md](SECURITY.md) | 威胁模型、安全边界、已知缺口 |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | 常见故障与定位命令 |

## 组成

```
tianma/                 协议核心（Go module）
  internal/proto        认证常量与 token 编解码
  internal/wire         TLS、QUIC、H3 门控
  internal/congestion   Brutal、BBR、loss guard
  internal/policy       目的地策略
  internal/guard        限流、配额、内存水位
  internal/client       H3 客户端、连接池
  boxapi/               给内核用的公开门面

singbox/                sing-box 适配层（type: "skyhorse"）
HorseUI/                面板（s-ui fork + back-machine）
HorseBox/               Windows / Android 客户端
```

## 当前状态

| 项 | 状态 |
|---|---|
| 协议核心 | 可用 |
| sing-box 内核 | 可用（Windows / Linux） |
| 面板 | 可用（节点、订阅、轮换、策略） |
| Windows 客户端 | 可用，TUN 已验证 |
| Android 客户端 | 9.7 已对齐 libbox 契约，待真机验证 |
| 真实网络性能 | 单点验证，未做多用户/晚高峰压测 |

## 一句话

天马不是更强的 Hy2，是更可控的 Hy2 分支：认证更强、治理内建、面板客户端一体化；伪装与成熟度不如 Hy2。

> `docs/HORSE2-SPEC.md` 是旧版设计稿，已废弃，以本目录为准。
