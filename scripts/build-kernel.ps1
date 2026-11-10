<#
.SYNOPSIS
  编译快马/天马内核：Windows、Linux、Android(libbox.aar)。

.DESCRIPTION
  统一从一个源码树（SkyHorse）产出三端内核，避免手工复制字段和标签。
  - Windows: SkyHorse.exe
  - Linux:   skyhorse-linux-amd64 / skyhorse-linux-arm64
  - Android: libbox.aar（gomobile bind，含完整构建标签）

.PARAMETER Targets
  all / windows / linux / android 的任意组合。

.PARAMETER OutDir
  产物目录，默认 SkyHorse\dist\kernel。

.PARAMETER Install
  编译后把产物复制到 HorseBox 对应位置（Windows core、Android libs/core）。

.EXAMPLE
  .\build-kernel.ps1 -Targets windows,linux

.EXAMPLE
  .\build-kernel.ps1 -Targets android -Install
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'windows', 'linux', 'android')]
    [string[]]$Targets = @('all'),

    [string]$OutDir = '',

    [string]$Version = '1.14.4',

    [string]$AndroidProject = '',

    [string]$AndroidAbis = 'arm64,amd64',

    [string]$JdkPath = '',

    [string]$NdkPath = '',

    [switch]$Install
)

$ErrorActionPreference = 'Stop'
$ScriptRoot = Split-Path -Parent $PSCommandPath
if (-not $OutDir) { $OutDir = Join-Path $ScriptRoot '..\dist\kernel' }
if (-not $AndroidProject) { $AndroidProject = Join-Path $ScriptRoot '..\..\HorseBox\android-libbox\sing-box' }
$Root = (Resolve-Path (Join-Path $ScriptRoot '..')).Path
$OutDir = [IO.Path]::GetFullPath($OutDir)
$AndroidProject = [IO.Path]::GetFullPath($AndroidProject)

# Windows / Linux 内核标签
$DesktopTags = @(
    'with_clash_api', 'with_v2ray_api', 'with_quic', 'with_dhcp', 'with_gvisor'
) -join ' '

# Android 必须与原版 libbox 标签一致，少一个 JNI 符号客户端就可能启动失败
$AndroidTags = @(
    'with_gvisor', 'with_quic', 'with_dhcp', 'with_wireguard', 'with_utls',
    'with_acme', 'with_clash_api', 'with_tailscale', 'with_ccm', 'with_ocm',
    'with_cloudflared', 'with_naive_outbound', 'with_purego', 'with_usbip',
    'with_openvpn', 'with_openconnect', 'badlinkname', 'tfogo_checklinkname0'
) -join ','

$LdFlags = "-s -w -X github.com/sagernet/sing-box/constant.Version=$Version"

function Write-Step([string]$Text) {
    Write-Host ''
    Write-Host "==> $Text" -ForegroundColor Cyan
}

function Assert-Command([string]$Name, [string]$Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "找不到 $Name。$Hint"
    }
}

function Invoke-GoBuild {
    param(
        [string]$Goos,
        [string]$Goarch,
        [string[]]$Tags,
        [string]$Output
    )
    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    $oldCgo = $env:CGO_ENABLED
    Push-Location (Join-Path $Root 'singbox')
    try {
        $env:GOOS = $Goos
        $env:GOARCH = $Goarch
        $env:CGO_ENABLED = '0'
        & go build -trimpath -tags ($Tags -join ' ') -ldflags $LdFlags -o $Output .\cmd\skyhorse
        if ($LASTEXITCODE -ne 0) { throw "go build 失败: $Goos/$Goarch" }
    }
    finally {
        Pop-Location
        $env:GOOS = $oldGoos
        $env:GOARCH = $oldGoarch
        $env:CGO_ENABLED = $oldCgo
    }
}

function Sync-AndroidSources {
    $thirdParty = Join-Path (Split-Path $AndroidProject -Parent) 'third_party'
    if (-not (Test-Path $thirdParty)) {
        throw "找不到 Android third_party 目录: $thirdParty"
    }

    Write-Step "同步 tianma -> $thirdParty\tianma"
    $dstTianma = Join-Path $thirdParty 'tianma'
    if (Test-Path $dstTianma) { Remove-Item -Recurse -Force $dstTianma }
    Copy-Item (Join-Path $Root 'tianma') $dstTianma -Recurse -Force
    Write-Step "同步 kuaima -> $thirdParty\kuaima"
    $dstKuaima = Join-Path $thirdParty 'kuaima'
    if (Test-Path $dstKuaima) { Remove-Item -Recurse -Force $dstKuaima }
    Copy-Item (Join-Path $Root 'kuaima') $dstKuaima -Recurse -Force
    Write-Step "同步 sing-mux -> $thirdParty\sing-mux"
    $dstMux = Join-Path $thirdParty 'sing-mux'
    if (Test-Path $dstMux) { Remove-Item -Recurse -Force $dstMux }
    Copy-Item (Join-Path $Root 'sing-mux') $dstMux -Recurse -Force
    foreach ($skip in @('bin', 'cmd', 'e2e')) {
        $p = Join-Path $dstTianma $skip
        if (Test-Path $p) { Remove-Item -Recurse -Force $p }
    }

    Write-Step "同步 singbox 适配层 -> $thirdParty\skyhorse-adapter"
    $dstAdapter = Join-Path $thirdParty 'skyhorse-adapter'
    if (-not (Test-Path $dstAdapter)) { New-Item -ItemType Directory -Force $dstAdapter | Out-Null }
    foreach ($name in @(
            'inbound_native.go', 'inbound_kuaima.go', 'mux_service.go', 'buffered_conn.go',
            'ratelimit_conn.go',
            'options.go', 'options_kuaima.go', 'options_test.go',
            'outbound_skyhorse.go', 'outbound_kuaima.go', 'go.mod', 'go.sum'
        )) {
        $src = Join-Path $Root "singbox\$name"
        if (Test-Path $src) { Copy-Item $src (Join-Path $dstAdapter $name) -Force }
    }
}

function Resolve-AndroidEnv {
    if (-not $JdkPath) {
        foreach ($candidate in @('D:\Android_Studio\jbr', "$env:ProgramFiles\Android\Android Studio\jbr")) {
            if (Test-Path (Join-Path $candidate 'bin\javac.exe')) { $JdkPath = $candidate; break }
        }
    }
    if (-not $JdkPath) { throw '找不到 JDK，请用 -JdkPath 指定（需要 javac）' }

    $sdk = $env:ANDROID_HOME
    if (-not $sdk) { $sdk = Join-Path $env:LOCALAPPDATA 'Android\Sdk' }
    if (-not (Test-Path $sdk)) { throw "找不到 Android SDK: $sdk" }

    if (-not $NdkPath) {
        $ndkRoot = Join-Path $sdk 'ndk'
        if (Test-Path $ndkRoot) {
            $NdkPath = Get-ChildItem $ndkRoot -Directory | Sort-Object Name -Descending |
                Select-Object -First 1 -ExpandProperty FullName
        }
    }
    if (-not $NdkPath -or -not (Test-Path $NdkPath)) { throw '找不到 Android NDK，请用 -NdkPath 指定' }

    $env:JAVA_HOME = $JdkPath
    $env:PATH = (Join-Path $JdkPath 'bin') + ';' + $env:PATH
    $env:ANDROID_HOME = $sdk
    $env:ANDROID_NDK_HOME = $NdkPath
}

function Build-Android {
    if (-not (Test-Path $AndroidProject)) {
        throw "找不到 Android 工程: $AndroidProject（用 -AndroidProject 指定）"
    }
    Assert-Command 'gomobile' '先执行: go install golang.org/x/mobile/cmd/gomobile@latest'
    Resolve-AndroidEnv
    Sync-AndroidSources

    $abiTarget = 'android/' + ($AndroidAbis -replace ',', ',android/')
    $out = Join-Path $OutDir 'libbox.aar'
    Write-Step "gomobile bind ($abiTarget)"
    Push-Location $AndroidProject
    try {
        & gomobile bind -target $abiTarget -androidapi 23 -javapkg 'io.nekohasekai' `
            -ldflags='-checklinkname=0' -tags $AndroidTags -o $out `
            github.com/sagernet/sing-box/experimental/libbox
        if ($LASTEXITCODE -ne 0) { throw 'gomobile bind 失败' }
    }
    finally {
        Pop-Location
    }
}

function Copy-IfInstall {
    param([string]$Source, [string]$Destination)
    if (-not $Install) { return }
    $destDir = Split-Path $Destination -Parent
    if (-not (Test-Path $destDir)) { New-Item -ItemType Directory -Force $destDir | Out-Null }
    if (Test-Path $Destination) {
        Copy-Item $Destination ($Destination + '.old') -Force
    }
    Copy-Item $Source $Destination -Force
    Write-Host "    已安装: $Destination" -ForegroundColor DarkGray
}

# ---------------------------------------------------------------------------

if ($Targets -contains 'all') { $Targets = @('windows', 'linux', 'android') }
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Force $OutDir | Out-Null }

Assert-Command 'go' '请先安装 Go 并加入 PATH'

$started = Get-Date
Write-Host "源码: $Root"
Write-Host "输出: $OutDir"
Write-Host "目标: $($Targets -join ', ')"

if ($Targets -contains 'windows') {
    Write-Step 'Windows amd64'
    $exe = Join-Path $OutDir 'SkyHorse.exe'
    Invoke-GoBuild -Goos 'windows' -Goarch 'amd64' -Tags @($DesktopTags) -Output $exe
    if ($Install) {
        Copy-IfInstall $exe (Join-Path $Root '..\HorseBox\core\sing-box.exe')
    }
}

if ($Targets -contains 'linux') {
    foreach ($arch in @('amd64', 'arm64')) {
        Write-Step "Linux $arch"
        $bin = Join-Path $OutDir "skyhorse-linux-$arch"
        Invoke-GoBuild -Goos 'linux' -Goarch $arch -Tags @($DesktopTags) -Output $bin
    }
}

if ($Targets -contains 'android') {
    Build-Android
    if ($Install) {
        $aar = Join-Path $OutDir 'libbox.aar'
        Copy-IfInstall $aar (Join-Path $Root '..\HorseBox\UI\android\app\libs\libbox.aar')
        Copy-IfInstall $aar (Join-Path $Root '..\HorseBox\core\libbox.aar')
    }
}

# 校验和
$sumFile = Join-Path $OutDir 'SHA256SUMS.txt'
$entries = Get-ChildItem $OutDir -File | Where-Object { $_.Name -ne 'SHA256SUMS.txt' } | ForEach-Object {
    $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
    "$hash  $($_.Name)"
}
Set-Content -Path $sumFile -Value $entries -Encoding ASCII

Write-Step '完成'
Get-ChildItem $OutDir -File | Select-Object Name, Length, LastWriteTime | Format-Table -AutoSize
Write-Host ("耗时 {0:n1} 秒" -f ((Get-Date) - $started).TotalSeconds)
