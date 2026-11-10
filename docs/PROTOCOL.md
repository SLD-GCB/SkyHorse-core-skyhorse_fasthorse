# 天马线协议（已废弃）

> 本文描述旧的 QUIC 裸流 / 0x401 帧协议，已不再适用。  
> 当前 H3 协议规范见 [PROTOCOL-H3.md](PROTOCOL-H3.md)，内核适配见 [ADAPTER.md](ADAPTER.md)。

## 1. 角色与传输

- 客户端与服务端之间是一条 **QUIC 连接**（TLS 1.3，ALPN `h3`）。
- 认证走 HTTP/3 请求；TCP 代理走 QUIC 原始双向流；UDP 走 QUIC Datagram（RFC 9221）。
- 一个连接上可以同时承载多条 TCP 流和多个 UDP 会话。

QUIC 参数（`tianma/internal/wire/tls.go`）：

| 参数 | 值 |
|---|---|
| MaxIdleTimeout | 60s |
| KeepAlivePeriod | 15s |
| MaxIncomingStreams | 1024 |
| 流接收窗口 | 1 MiB → 8 MiB |
| 连接接收窗口 | 2 MiB → 64 MiB |
| InitialPacketSize | 1452 |
| Datagram | 启用，单帧上限 1200 |

## 2. TLS 与证书

- 服务端只提供 TLS 1.3，ALPN 只有 `h3`。
- 客户端两种信任方式：
  - **pin**：`certificate_public_key_sha256`，校验 SPKI SHA-256；
  - **insecure**：跳过证书校验，仅建议自签场景使用。
- 两者都没有时，客户端拒绝启动。

## 3. 认证

客户端发起：

```
POST https://tianma/auth
Tianma-Auth: <uuid>:<hex(token)>
Tianma-Padding: <随机长度>
Tianma-CC-RX: <客户端接收速率，字节/秒，可选>
```

- `token = TLS.ExportKeyingMaterial(uuid, []byte(password), 32)`
- token 绑定当前 TLS 连接，**换一条连接就不能重放**。
- 服务端校验成功返回 HTTP `233`，并在响应头回自己的 `Tianma-CC-RX`。
- 失败返回伪装响应（默认 404），不区分“用户不存在”和“密码错误”。

## 4. TCP

认证通过后，客户端打开一条双向流，写入：

```
varint(0x401) | varint(len(addr)) | addr | varint(padding) | padding
```

- `addr` 是 `host:port` 文本；
- 服务端随后回 **1 字节状态**：
  - `0x00` 成功，之后是双向字节流；
  - `0x01` 失败（策略拒绝、拨号失败等），随后关闭流。
- 地址长度上限 1024 字节。

## 5. UDP

每个 QUIC Datagram 是一条消息：

```
Session(4) | Packet(2) | FragmentID(1) | FragmentCount(1) | AddrLen(1) | Addr | Payload
```

- `Session`：客户端随机会话号，服务端按它分流；
- `Packet`：会话内包序号，用于分片重组；
- 单帧负载上限 1150 字节，超出由客户端分片；
- 服务端回程也分片，客户端重组；
- 会话空闲 90 秒回收。

## 6. 混淆（Salamander）

普通模式：

```
salt(8) | XOR(payload, BLAKE2b-256(key || salt))
```

- 每包随机 salt；
- 没有认证，key 不对只会解出垃圾并被 QUIC 丢弃。

轮换模式（`obfs_rotation_seconds > 0`）：

```
epoch(4) | salt(8) | XOR(payload, BLAKE2b-256(master || "tianma-obfs-rotate" || epoch))
```

- `epoch = unix 秒 / 轮换周期`；
- 接收端只接受 `当前 ± tolerance` 的 epoch，其余静默丢弃；
- 主密钥泄露仍然全盘皆输，轮换只限制单窗口密钥的生命周期。

## 7. 拥塞控制

认证时两端交换 `Tianma-CC-RX`：

- 双方都有有效带宽声明 → **Brutal**；
- 否则 → **BBR + loss guard**。

### Brutal

- 固定速率发送，pacer 整形；
- 外层调速器：ACK 率 < 90% 时降速，> 98% 时逐步恢复；
- 降速下限是配置值的 20%。

### BBR

- 移植自 Hysteria 的 BBR，带 `conservative / standard / aggressive` 三种 profile；
- loss guard：丢包率 > 2% 时按 `1 - min(0.5, lossRate×2)` 压缩窗口；
- 只压窗口，不改 pacing。

## 8. 错误语义

| 场景 | 表现 |
|---|---|
| 认证失败 | HTTP 404（伪装），连接关闭 |
| 目标被策略拒绝 | TCP 状态 `0x01` |
| 目标拨号失败 | TCP 状态 `0x01` |
| UDP 目标被拒绝 | 静默丢弃 |
| 混淆 key 不匹配 | 静默丢包，表现为超时 |
| 轮换窗口不匹配 | 静默丢包，表现为超时 |

## 9. 兼容性

- 不与 Hysteria 2 互通；
- 帧类型 `0x401` 与 Hy2 相同，但认证、状态、混淆格式不同；
- 客户端与服务端必须使用相同的 `obfs_password` 和轮换参数。
