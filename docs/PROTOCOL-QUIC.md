# SkyHorse QUIC 协议规范（迁移中）

状态：H3 已决定废弃，正在迁移到原生 QUIC/UDP。  
在新内核完成前，本文件描述目标协议，不代表当前二进制已实现。

## 1. 目标

```
QUIC v1
└── TLS 1.3
    ├── 双向流：SOCKS5 TCP CONNECT
    └── QUIC DATAGRAM：SOCKS5 UDP
```

硬性约束：

- 不使用 HTTP/3；
- 不使用 H3 CONNECT；
- 不使用 H3 DATA；
- 不使用自定义 0x401 帧；
- 不使用 UDP 长度前缀；
- **禁止 UDP over TCP**；
- **禁止 UDP over 可靠流**；
- UDP 只能走 QUIC DATAGRAM。

## 2. 传输

| 项 | 值 |
|---|---|
| 传输 | QUIC v1 |
| TLS | TLS 1.3 |
| TCP | QUIC 双向流 |
| UDP | QUIC DATAGRAM |
| 认证 | TLS exporter token |

QUIC DATAGRAM 必须启用：

```
EnableDatagrams = true
MaxDatagramFrameSize = 1200
```

## 3. 认证

```
token = TLS.ExportKeyingMaterial(uuid, password, 32)
username = uuid
password = hex(token)
```

使用标准 SOCKS5 用户名/密码认证（RFC 1929）：

- 客户端发送 `05 01 02`；
- 服务端选择 `02`；
- 客户端发送 `01 ulen uuid plen hex(token)`；
- 服务端常量时间校验；
- 失败关闭该流，不关闭整条 QUIC。

token 绑定当前 QUIC 连接，换连接不能重放。

## 4. TCP

每条 TCP 连接对应一条 QUIC 双向流。

客户端在流上执行标准 SOCKS5：

```
05 01 02
01 <ulen> <uuid> <plen> <hex token>
05 01 00 <ATYP> <ADDR> <PORT>
```

服务端返回：

```
05 00 00 01 00 00 00 00 00 00
```

之后是原始 TCP 字节流，没有额外帧头。

## 5. UDP

### 5.1 关联

客户端打开一条 QUIC 流，执行 SOCKS5 认证后发送：

```
05 03 00 <ATYP> <ADDR> <PORT>
```

服务端返回成功，并保持该流直到关闭。

QUIC 连接只有在完成关联后才接受 DATAGRAM。

### 5.2 数据

每个 QUIC DATAGRAM 载荷是标准 SOCKS5 UDP 请求：

```
RSV    2 字节 0x0000
FRAG   1 字节 0x00
ATYP   1 字节
ADDR   目标地址
PORT   2 字节
DATA   UDP 负载
```

服务端回包同样使用 SOCKS5 UDP 头。

禁止：

- 把 UDP 写进流；
- 给 UDP 加长度前缀后走 TCP；
- 用 H3 DATA 承载 UDP。

## 6. 连接池

- 每条 QUIC 连接独立；
- TCP 和 UDP 可以分池；
- 单条连接失败只影响该连接；
- 坏连接停止分配新流；
- 已有流继续；
- 流归零后才丢弃坏连接；
- 端口轮换只影响新连接。

## 7. 前置门

保留在 QUIC 之前：

```
TMG1 + 时间戳 + nonce + HMAC
```

未通过门控的 UDP 包直接丢弃，不建立 QUIC。

## 8. 与 H3 的差异

| 项 | H3（废弃） | QUIC（目标） |
|---|---|---|
| 寻址 | H3 CONNECT | SOCKS5 CONNECT |
| TCP | H3 DATA | QUIC 流 |
| UDP | H3 DATA 长度前缀 | QUIC DATAGRAM |
| 队头阻塞 | UDP 放大 | UDP 不进入可靠流 |
| ALPN | h3 | 由实现决定 |
| 伪装 | HTTP/3 网站 | 不再有 H3 外观 |

## 9. 已知代价

- 失去 H3 网站伪装；
- 链路仍然是 UDP/QUIC；
- 需要重做抗检测；
- SOCKS5 握手增加一次往返；
- 纯 UDP 应用只支持 DATAGRAM 大小以内的包。
