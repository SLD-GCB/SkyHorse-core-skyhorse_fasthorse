# SkyHorse H3 协议规范

版本：`1.14.4-skyhorse`  
状态：当前内核实现以此文档为准。旧的“天马线协议 / 0x401 裸流”文档已废弃。

## 1. 范围

SkyHorse（天马）是一个基于标准 HTTP/3 CONNECT 的代理协议。

```
UDP
└── QUIC v1
    └── TLS 1.3
        └── HTTP/3
            ├── 普通请求：拒绝或关闭
            └── CONNECT：代理隧道
                ├── TCP
                └── UDP（长度前缀）
```

设计约束：

- 不使用 QUIC 原始双向流承载业务；
- 不使用自定义 0x401 帧；
- 所有业务流量都走 H3 CONNECT；
- TCP 和 UDP 共用同一个 H3 端口；
- 可选前置 UDP 门控，未通过门控的包直接丢弃。

## 2. 传输层

| 项 | 值 |
|---|---|
| 传输 | QUIC v1 |
| TLS | TLS 1.3 |
| ALPN | `h3` |
| UDP | 必需 |
| 认证 | TLS exporter token |
| 默认端口 | 由部署决定，不强制 443 |

QUIC 传输参数由 `tianma/internal/wire/QUICConfig()` 生成，包含随机化字段，避免固定指纹。

## 3. 前置门控（可选）

配置了 `obfs_password` 时启用。

### 3.1 密钥

```
secret = obfs_password || node_id
key    = SHA256("tianma-h3-gate-v1" || secret)
```

客户端和服务端的 `obfs_password`、`node_id` 必须完全一致。

### 3.2 门记录

固定 44 字节：

| 偏移 | 长度 | 内容 |
|---|---|---|
| 0 | 4 | ASCII `TMG1` |
| 4 | 8 | Unix 秒，大端 |
| 12 | 16 | 随机 nonce |
| 28 | 16 | HMAC-SHA256(key, 前 28 字节) 截断前 16 字节 |

### 3.3 行为

客户端：

- 在第一条 QUIC 包之前发送一次门记录；
- 之后正常发送 QUIC。

服务端：

- 收到 `TMG1` 记录时校验 HMAC、时间窗口、nonce 重放；
- 校验通过后，该源地址放行 2 分钟；
- 未通过或未发送门记录的包直接丢弃，不返回 ICMP、QUIC、TLS 或 H3 响应。

时间窗口：

```
-1 分钟 <= now - timestamp <= 2 分钟
```

nonce 缓存 2 分钟。

### 3.4 兼容

- 未配置 `obfs_password` 时，双方都不启用门控；
- 服务端启用门控、客户端未配置时，所有 QUIC 包被丢弃。

## 4. TLS 与信任

服务端必须配置证书和私钥。

客户端两种模式：

| 模式 | 字段 | 行为 |
|---|---|---|
| pin | `tls.certificate_public_key_sha256` | 校验 SPKI SHA-256，防 MITM |
| insecure | `tls.insecure: true` | 跳过证书校验，仅建议自签测试 |

自签证书可以正常工作，因为 QUIC 使用 Go TLS，不受浏览器证书策略限制。

## 5. HTTP/3 认证

### 5.1 Token

```
token = TLS.ExportKeyingMaterial(
    label   = uuid,
    context = password,
    length  = 32,
)
```

### 5.2 请求头

```
Authorization: Bearer <uuid>:<hex(token)>
```

说明：

- token 绑定当前 TLS 连接；
- 换连接不能重放；
- 服务端使用常量时间比较；
- 服务端兼容 `Authorization: Basic base64(uuid:password)`，仅用于旧客户端/调试，不推荐生产使用。

### 5.3 失败行为

任何认证失败：

- 不返回 401/403；
- 不返回错误页面；
- 直接关闭 QUIC 连接；
- 服务端记录失败次数，超过阈值后按 guard 策略封禁。

## 6. CONNECT 隧道

### 6.1 请求

```
:method = CONNECT
:authority = 服务端地址
Authorization: Bearer <token>
-connect-authority: <目标 host:port>
X-Tianma-UDP: 1        # 仅 UDP
```

`-connect-authority` 是实际目标地址，不是服务端地址。

### 6.2 响应

认证成功后服务端立即返回：

```
HTTP/3 200
```

然后：

- TCP：双向字节流；
- UDP：长度前缀数据报。

注意：

> 服务端在确认目标可连接之前就返回 200。  
> 目标拨号失败时表现为流被关闭，而不是 HTTP 错误码。

### 6.3 TCP

CONNECT 请求体是客户端到目标的字节流。  
CONNECT 响应体是目标到客户端的字节流。  
没有额外帧头。

### 6.4 UDP

UDP 使用同一条 CONNECT 流的 DATA 帧，帧格式：

```
uint16 length (大端)
payload (length 字节)
```

两个方向相同。单帧 payload 上限 65535，实际受 QUIC 流控和 MTU 限制。

## 7. 连接池与端口

连接池是客户端行为，不属于服务端协议。

客户端可以：

- 建立多条独立 H3 连接；
- 每条连接独立认证；
- 按 inflight 选择连接；
- 空闲连接按 `port_rotate_seconds` 轮换到下一个服务端端口；
- 连接失败后替换；
- 达到流上限且未到 `max_connections` 时自动扩容；
- 空闲且吞吐低于池平均 50% 时淘汰。

服务端只负责监听端口，不参与客户端的连接池调度。

## 8. 错误与关闭

| 场景 | 行为 |
|---|---|
| 未过门控 | 丢包，无响应 |
| 非 CONNECT 请求 | 关闭 QUIC |
| 认证失败 | 关闭 QUIC，记录失败 |
| 目标拨号失败 | CONNECT 已返回 200，随后关闭流 |
| 连接数超限 | `CloseWithError(0x1, "too many connections")` |
| 客户端关闭 | 正常 QUIC close |

## 9. 版本与兼容

当前没有版本协商字段。

因此：

- 客户端和服务端必须使用同一代 H3 内核；
- 旧裸流客户端无法连接；
- 协议升级需要双端同步；
- 后续如需灰度，必须新增版本字段。

## 10. 安全边界

已实现：

- TLS 1.3；
- 连接绑定 token；
- 可选门控；
- 重放缓存；
- 失败静默；
- 自签 + pin；
- 服务端连接数/流数上限。

未解决：

- UDP 在 IP 层可见；
- 无法隐藏“这是 UDP”；
- 无法抵抗 UDP 整体封锁；
- 未做真实 DPI 验证；
- 没有 ECH；
- 没有版本协商。
