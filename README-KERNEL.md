# SkyHorse Core

天马（SkyHorse）内核源码与三端构建产物。

## 目录

| 目录 | 内容 |
|---|---|
| `singbox/` | sing-box 适配层、桌面内核入口 `cmd/skyhorse` |
| `tianma/` | 天马 QUIC 协议核心 |
| `kuaima/` | 快马 TCP/H2 协议核心 |
| `sing-mux/` | 多路复用依赖 |
| `scripts/` | 内核构建脚本 |
| `docs/` | 协议、配置、构建和安全文档 |
| `kernels/` | 本次构建的 Windows / Linux / Android 原始产物 |
| `SkyHorse-Kernels-1.14.4/` | 三端发布目录 |
| `SkyHorse-Kernels-1.14.4.zip` | 三端内核压缩包 |

## 构建

从本目录运行：

```powershell
# Windows + Linux
.\scripts\build-kernel.ps1 -Targets windows -OutDir .\kernels
.\scripts\build-kernel.ps1 -Targets linux -OutDir .\kernels

# Android（需要 Android 工程、gomobile、JDK、SDK、NDK）
.\scripts\build-kernel.ps1 -Targets android -OutDir .\kernels `
  -AndroidProject D:\github_code\code\Android\project\HorseBox\android-libbox\sing-box
```

桌面端构建标签：

```
with_clash_api with_v2ray_api with_quic with_dhcp with_gvisor
```

三端产物：

```
windows-amd64/SkyHorse.exe
linux-amd64/skyhorse
linux-arm64/skyhorse
android/libbox.aar
android/libbox-sources.jar
```

## 说明

- 服务端内核包含天马 IPv4 优先修复：域名解析后优先 IPv4，避免无 IPv6 路由时 SOCKS 请求失败。
- Android `libbox.aar` 使用 `gomobile bind`，ABI 为 `android/arm64,android/amd64`。
- 替换内核前先停止面板/客户端内核，替换后恢复执行权限。
- `kernels/` 和发布 zip 是构建产物；源码在 `singbox/`、`tianma/`、`kuaima/`、`sing-mux/`。
