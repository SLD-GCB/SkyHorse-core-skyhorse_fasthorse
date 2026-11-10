# 构建

## 1. 环境

| 工具 | 版本 |
|---|---|
| Go | 1.27（1.26.8 亦可） |
| Node | 18+（面板前端） |
| Flutter | 3.47+（客户端） |
| Android NDK | 27.2.12479018 |
| JDK | Android Studio 自带 `jbr` |

## 2. 内核（Windows / Linux）

```bash
cd singbox

# Windows
go build -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=1.14.3" \
  -o SkyHorse.exe ./cmd/skyhorse

# Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
go build -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=1.14.3" \
  -o skyhorse-linux-amd64 ./cmd/skyhorse
```

需要面板/API 兼容时加标签：

```
with_clash_api with_v2ray_api with_quic with_dhcp with_gvisor
```

## 3. 面板

```bash
cd HorseUI/service
build-linux.bat both      # 产物 service/dist/horseui-*-linux-*.zip
build-update.bat amd64    # 产物 service/dist/horseui.new
```

打包前确认 `service/dist/` 里的内核 tar.gz 是天马：

```
sing-box-1.14.3-skyhorse-linux-amd64.tar.gz
  └── sing-box-1.14.3-skyhorse-linux-amd64/sing-box
```

`install.sh` 用 `--strip-components=1` 解压，tar 内必须有顶层目录，否则会报“内核包解压失败”。

## 4. Windows 客户端

```bash
cd HorseBox
build-all.bat win 9.5
```

`HorseBox/core/sing-box.exe` 必须是天马内核；`build-all` 会把它打进 zip。

## 5. Android 客户端

Android 不走可执行文件，走 `libbox.aar`。规则很硬：**`classes.jar` 和 `.so` 必须来自同一次构建**，否则 JNI 初始化失败，表现为“代理启动不了”。

### 5.1 源码

用本机已同步的 sing-box 源码（与 AAR 同版本），不要用 GitHub 最新版。

### 5.2 注册天马

1. 把 `tianma` 和 `singbox` 适配层放到 `third_party/`；
2. 根 `go.mod` 加：

```
require skyhorse.local/skyhorse/singbox v0.0.0
replace skyhorse.local/skyhorse/singbox => ../third_party/skyhorse-adapter
replace tianma => ../third_party/tianma
```

3. 在 `include/registry.go` 注册：

```go
skyhorsebox "skyhorse.local/skyhorse/singbox"
...
skyhorsebox.RegisterInbound(registry)
skyhorsebox.RegisterOutbound(registry)
```

4. `go build ./...` 确认通过。

### 5.3 构建标签

**必须与原版 AAR 一致**：

```
with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,
with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,
with_naive_outbound,with_purego,with_usbip,with_openvpn,with_openconnect,
badlinkname,tfogo_checklinkname0
```

少一个功能，对应的 JNI 符号就不存在。

### 5.4 Java 包名

原版是 `io/nekohasekai/libbox/Libbox.class`。

gomobile 会在 `-javapkg` 后面**再拼 Go 包名**，所以：

```
-javapkg io.nekohasekai     → io/nekohasekai/libbox/...
```

写成 `io.nekohasekai.libbox` 会得到 `io/nekohasekai/libbox/libbox`，客户端编译不过。

### 5.5 编译

```bash
export JAVA_HOME="D:/Android_Studio/jbr"
export PATH="$JAVA_HOME/bin:$PATH"
export ANDROID_HOME="$LOCALAPPDATA/Android/Sdk"
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.2.12479018"

gomobile bind -target "android/arm64,android/amd64" -androidapi 23 \
  -javapkg "io.nekohasekai" \
  -ldflags="-checklinkname=0" \
  -tags "<上面的完整标签>" \
  -o libbox.aar github.com/sagernet/sing-box/experimental/libbox
```

`-ldflags=-checklinkname=0` 是为了绕过 Go 1.27 对 libbox 内部 `linkname` 的链接限制；Go 1.26.8 不需要。

### 5.6 替换与打包

```bash
cp libbox.aar HorseBox/UI/android/app/libs/libbox.aar
cp libbox.aar HorseBox/core/libbox.aar

cd HorseBox
build-all.bat android 9.7
```

**不要只换 `.so`、保留旧 `classes.jar`**，除非两者确认同源。

## 6. 测试

```bash
# 协议
cd tianma && go test ./...

# 内核
cd singbox && go test ./... -count=1

# 面板
cd HorseUI && go test ./back-machine/internal/svc -run SkyHorse -v
```

## 7. 发布包

```
release/skyhorse-<版本>/
  skyhorse-linux-amd64
  skyhorse-linux-arm64
  SkyHorse.exe
  server.json.example
  client.json.example
  skyhorse.service
  INSTALL.md
  SHA256SUMS
```

Windows exe 需要签名（自签代码证书即可降低 Defender 误杀）。
