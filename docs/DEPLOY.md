# 部署

## 1. 面板（Linux）

安装包：`horseui-<版本>-linux-<arch>.zip`，内含面板二进制、天马内核、`install.sh`。

```bash
unzip horseui-1.0.0-skyhorse-rc1-linux-amd64.zip
cd horseui-1.0.0-skyhorse-rc1-linux-amd64
sudo bash service/install.sh
```

安装位置：

| 路径 | 内容 |
|---|---|
| `/usr/local/horseui/horseui` | 面板 |
| `/etc/horseui/config.json` | 内核配置 |
| `/etc/horseui/bin/sing-box` | 天马内核 |
| `/etc/horseui/certs/` | 证书 |

面板默认 `0.0.0.0:8080`，用 `HORSEUI_LISTEN` 覆盖：

```bash
sudo HORSEUI_LISTEN=0.0.0.0:8443 bash service/install.sh
```

### 更新面板

把新二进制放到 `/usr/local/horseui/horseui.new`，面板菜单「更新面板」会自动停服、备份、替换、重启。

## 2. 服务端（纯内核，不带面板）

```bash
install -m 0755 skyhorse-linux-amd64 /usr/local/bin/skyhorse
mkdir -p /etc/skyhorse && cd /etc/skyhorse
openssl ecparam -name prime256v1 -genkey -noout -out key.pem
openssl req -new -x509 -key key.pem -out cert.pem -days 3650 -subj "/CN=localhost"

# 客户端用的 pin（base64 SPKI）
openssl x509 -in cert.pem -pubkey -noout \
  | openssl pkey -pubin -outform der \
  | openssl dgst -sha256 -binary | base64

skyhorse check -c server.json
skyhorse run -c server.json
```

systemd：

```ini
[Unit]
Description=SkyHorse
After=network-online.target

[Service]
ExecStart=/usr/local/bin/skyhorse run -c /etc/skyhorse/server.json
Restart=on-failure
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

**只开 UDP**，TCP 不监听：

```bash
ufw allow 4433/udp
```

## 3. Windows 客户端

- 包：`HorseBox-<版本>-windows-x64.zip`，`core/sing-box.exe` 必须显示 `SkyHorse 1.14.3-skyhorse`；
- TUN 需要**管理员身份运行**；
- 不开 TUN 时用混合端口 `7890`。

验证：

```powershell
.\core\sing-box.exe version
curl.exe --socks5 127.0.0.1:7890 http://www.gstatic.com/generate_204 -w "%{http_code}`n"
```

`204` 为通。

## 4. Android 客户端

- 安装 APK，导入订阅；
- 连接时授权 VpnService；
- 内核是 `libbox.so`，必须与 `classes.jar` 来自同一次构建（见 [BUILD.md](BUILD.md)）。

## 5. 面板节点

1. 入站类型选 `skyhorse`；
2. 填端口、用户 UUID/密码、混淆密码、上下行；
3. 证书选面板证书或填路径；
4. 保存，确认面板提示应用成功；
5. 用户 → 链接 → sing-box 订阅地址，导入客户端。

## 6. 上机检查清单

```bash
# 内核版本
/etc/horseui/bin/sing-box version        # 应为 SkyHorse 1.14.3-skyhorse

# 端口是否真的监听
ss -lunp | grep -E '4433|1000[0-9]'

# 运行配置里的入站类型
python3 -c "import json;d=json.load(open('/etc/horseui/config.json'));print([(i.get('type'),i.get('listen_port')) for i in d['inbounds']])"

# 本机回环验证
/etc/horseui/bin/sing-box run -c /tmp/probe.json
curl --socks5 127.0.0.1:21086 http://www.gstatic.com/generate_204 -w '%{http_code}\n'
```

## 7. 证书与 pin

- 自签证书：客户端用 `insecure` 或 pin；
- 真证书：可把 `certificate_path` 指向 Let's Encrypt 证书；
- 自签 + 不开混淆会在启动时打印 WARNING，不建议这样上机。
