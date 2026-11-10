# 故障排查

按现象查，不按猜测改。

## 1. 客户端一直超时（`no recent network activity`）

这是 QUIC 握手没有回包，不是认证失败。按顺序查：

```bash
# 服务端是否真的监听
ss -lunp | grep <端口>

# 内核是否是天马
/etc/horseui/bin/sing-box version

# 运行配置里的入站类型
python3 -c "import json;d=json.load(open('/etc/horseui/config.json'));print([(i.get('type'),i.get('listen_port')) for i in d['inbounds']])"
```

常见原因：

| 原因 | 特征 |
|---|---|
| 轮换参数不一致 | 服务端 `obfs_rotation_seconds > 0`，客户端没有该字段 |
| 混淆密码不一致 | 端口对、包到、无回包 |
| 公网 UDP 没放行 | 本机回环能通，公网不通 |
| 服务端内核不是天马 | 端口被旧协议占着 |
| 运行配置是旧的 | `config.json` 的修改时间晚于进程启动时间 |

本机回环验证：

```bash
cat >/tmp/probe.json <<'EOF'
{
  "log": { "level": "debug" },
  "inbounds": [ { "type": "socks", "tag": "s", "listen": "127.0.0.1", "listen_port": 21086 } ],
  "outbounds": [ {
    "type": "skyhorse", "tag": "proxy",
    "server": "127.0.0.1", "server_port": 10000,
    "uuid": "<uuid>", "password": "<password>",
    "obfs_password": "<obfs>",
    "obfs_rotation_seconds": 3600,
    "tls": { "enabled": true, "server_name": "localhost", "insecure": true }
  } ],
  "route": { "final": "proxy" }
}
EOF

/etc/horseui/bin/sing-box run -c /tmp/probe.json >/tmp/probe.log 2>&1 &
sleep 1
curl --socks5 127.0.0.1:21086 http://www.gstatic.com/generate_204 -w '%{http_code}\n'
cat /tmp/probe.log
```

- `204`：服务端正常，问题在公网 UDP 或客户端；
- 超时：服务端这条链路本身有问题。

## 2. 认证失败（日志 `authentication failed (status 404)`）

- UUID/密码与服务端 `users` 不一致；
- 用户 UUID 或密码为空，被服务端跳过；
- 面板建用户时没有生成 UUID（旧版本 `fillClientSecrets` 缺 skyhorse 分支）。

检查面板用户列表的 UUID、密码是否非空。

## 3. 面板字段保存后变 0

旧面板只把轮换写进 `extra`，编辑时没有回填。更新面板到包含 `sky_obfs_rotation` 回填的版本。

如果仍然出现，检查入站的 `extra` 里是否残留：

```json
"obfs_rotation_seconds": 5
```

## 4. 面板测速失败

- `record not found`：该入站没有启用用户；
- `general SOCKS server failure`：测速临时客户端拨号失败，常见于轮换未透传、TLS 没有 insecure、服务端未监听。

## 5. Windows 客户端代理不出去

先看内核：

```powershell
.\core\sing-box.exe version
```

- `sing-box version 1.14.3`：装的是原版，不认识 `skyhorse`；
- `SkyHorse 1.14.3-skyhorse`：正常。

TUN 开了但全部断网：

- 原因：`auto_route` 把内核自己到服务器的 QUIC 包也抓进 TUN；
- 修复：TUN 入站加 `route_exclude_address`，值为所有节点的 `server/32`；
- 9.5 起客户端会自动生成。

TUN 启动报权限：

```
A required privilege is not held by the client
```

以管理员身份运行客户端。

## 6. Android 代理启动不了

libbox 的 Java 类和 `.so` 必须同源。检查：

- 构建标签是否与原版一致（缺功能 = 缺 JNI 符号）；
- `-javapkg` 是否为 `io.nekohasekai`（gomobile 会再拼 `libbox`）；
- `classes.jar` 和 `.so` 是否来自同一次构建。

日志：

```powershell
adb logcat -d | findstr /i "libbox UnsatisfiedLink dlopen FATAL"
```

## 7. 配置应用失败

```
clash api is not included in this build
v2ray api is not included in this build
gVisor is not included in this build
```

内核缺少对应构建标签，用 `with_clash_api`、`with_v2ray_api`、`with_gvisor` 重编。

## 8. 目标被拒绝

TCP 返回状态 `0x01`，或 UDP 无响应：

- 目标是私网/回环，`policy` 默认拒绝；
- 端口不在 `allowed_ports`；
- 域名解析到了被拒地址。

按需在服务端 `policy` 开 `allow_private` / `allow_loopback`。

## 9. 面板内核包解压失败

`install.sh` 用 `--strip-components=1`，内核 tar.gz 必须有顶层目录：

```
sing-box-1.14.3-skyhorse-linux-amd64/
  sing-box
```

如果 tar 里直接是 `sing-box`，会被剥空，报“内核包解压失败”。

## 10. 速度远低于声明带宽

- 声明值高于实际：Brutal 硬顶 → 丢包 → 调速器降到下限，吞吐反而低；
- VPS 的“500 Mbps”可能是共享峰值；
- 单核 CPU 跑用户态 QUIC + 混淆。

先测原始链路（`iperf3`），再决定填多少。Brutal 填实测值的 80%~90%；不确定就填 `0` 走 BBR。
