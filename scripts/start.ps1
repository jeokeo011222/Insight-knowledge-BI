<#
.SYNOPSIS
    智观 BI（Insight Knowledge BI）一键启动。

.DESCRIPTION
    幂等脚本：已经在跑的组件会自动跳过，重复执行不会报"端口被占用"。
    依次完成 5 步：
      1. 拉起 PostgreSQL / Redis 容器          （-SkipInfra 可跳过）
      2. 编译后端二进制 superset-dev.exe        （-SkipBuild 可跳过）
      3. 迁移元数据库 superset db upgrade       （幂等）
      4. 启动后端三门进程 run / worker / beat
      5. 启动前端 Vite dev server               （-SkipFrontend 可跳过）

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts\start.ps1

.EXAMPLE
    # 只用外部数据库/Redis 时，跳过容器，直接起应用
    powershell -ExecutionPolicy Bypass -File scripts\start.ps1 -SkipInfra
#>
[CmdletBinding()]
param(
    [switch]$SkipInfra,
    [switch]$SkipBuild,
    [switch]$SkipFrontend
)

$ErrorActionPreference = 'Stop'

# ------------------------------------------------------------------ 路径与端口
$Root        = Split-Path -Parent $PSScriptRoot
$BackendDir  = Join-Path $Root 'backend'
$FrontendDir = Join-Path $Root 'frontend'
$LogDir      = Join-Path $Root 'logs'
$Exe         = Join-Path $BackendDir 'superset-dev.exe'
$ComposeFile = Join-Path $Root 'docker\docker-compose.yml'
$ApiPort     = 8088
$WebPort     = 3000

# ------------------------------------------------------------------ 输出helper
function Write-Step { param([string]$Message) Write-Host "==> $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "    OK    $Message" -ForegroundColor Green }
function Write-Skip { param([string]$Message) Write-Host "    SKIP  $Message" -ForegroundColor DarkGray }
function Write-Warn { param([string]$Message) Write-Host "    WARN  $Message" -ForegroundColor Yellow }
function Write-Die  { param([string]$Message) Write-Host "    FAIL  $Message" -ForegroundColor Red; exit 1 }

Write-Host ''
Write-Host '  智观 BI · 启动' -ForegroundColor White
Write-Host '  ------------------------------------------------' -ForegroundColor DarkGray

if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir -Force | Out-Null }

# ------------------------------------------------------------------ 探测helper
function Get-ListeningPid {
    param([int]$Port)
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
            Select-Object -First 1
    if ($null -eq $conn) { return $null }
    return $conn.OwningProcess
}

function Get-RunningRoles {
    $roles = @()
    Get-CimInstance Win32_Process -Filter "Name='superset-dev.exe'" -ErrorAction SilentlyContinue |
        ForEach-Object {
            if ($_.CommandLine) {
                $roles += ($_.CommandLine.Trim() -split '\s+')[-1]
            }
        }
    return $roles
}

function Resolve-GoToolchain {
    if (Get-Command go -ErrorAction SilentlyContinue) { return }
    $candidates = @()
    if ($env:GOROOT) { $candidates += (Join-Path $env:GOROOT 'bin') }
    if ($env:USERPROFILE) {
        $sdk = Join-Path $env:USERPROFILE 'sdk'
        if (Test-Path $sdk) {
            $candidates += Get-ChildItem $sdk -Directory -ErrorAction SilentlyContinue |
                Sort-Object Name -Descending |
                ForEach-Object { Join-Path $_.FullName 'bin' }
        }
    }
    foreach ($c in $candidates) {
        if (Test-Path (Join-Path $c 'go.exe')) {
            $env:PATH = "$c;$env:PATH"
            return
        }
    }
    Write-Die "未找到 Go 工具链。请安装 Go 1.22+ 并加入 PATH，或设置 GOROOT 环境变量。"
}

function Resolve-PackageManager {
    if (Get-Command pnpm.cmd -ErrorAction SilentlyContinue) { return 'pnpm' }
    if (Get-Command npm.cmd  -ErrorAction SilentlyContinue) { return 'npm' }
    Write-Die "未找到 pnpm / npm，请先安装 Node.js 18+。"
}

function Start-Role {
    param([string]$Role)
    $log = Join-Path $LogDir "$Role.log"
    Start-Process -FilePath $env:ComSpec `
        -ArgumentList '/c', "`"$Exe`" $Role > `"$log`" 2>&1" `
        -WorkingDirectory $BackendDir -WindowStyle Hidden | Out-Null
    Write-Ok "$Role 已启动          日志 logs\$Role.log"
}

function Wait-Backend {
    param([int]$Port, [int]$TimeoutSec = 40)
    for ($i = 0; $i -lt ($TimeoutSec * 2); $i++) {
        try {
            $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" -UseBasicParsing -TimeoutSec 2
            if ($resp.StatusCode -eq 200) { return $true }
        } catch { }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

# ------------------------------------------------------------------ 1. 基础设施
if (-not $SkipInfra) {
    Write-Step '拉起基础设施容器（PostgreSQL 5533 / Redis 6379）'
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Write-Warn '未检测到 docker，跳过容器。若你没有外部数据库，请先安装 Docker Desktop。'
    } else {
        try {
            docker compose -f $ComposeFile up -d 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) {
                Write-Ok '容器已就绪（docker\docker-compose.yml）'
            } else {
                Write-Warn "docker compose 返回码 $LASTEXITCODE，请确认 5533 / 6379 未被占用"
            }
        } catch {
            Write-Warn "docker compose 执行失败：$($_.Exception.Message)"
        }
    }
} else {
    Write-Skip '按参数要求跳过容器'
}

# ------------------------------------------------------------------ 2. 编译后端
if (-not $SkipBuild) {
    Write-Step '编译后端'
    Resolve-GoToolchain
    Push-Location $BackendDir
    try {
        & go build -o $Exe ./cmd/superset
        if ($LASTEXITCODE -ne 0) { Write-Die 'go build 失败，请检查上面的编译错误。' }
    } finally {
        Pop-Location
    }
    Write-Ok "已生成 backend\superset-dev.exe"
} else {
    Write-Skip '按参数要求跳过编译'
}

if (-not (Test-Path $Exe)) {
    Write-Die "后端二进制不存在：$Exe  —— 请去掉 -SkipBuild 重新执行。"
}

# ------------------------------------------------------------------ 3. 迁移元数据库
Write-Step '迁移元数据库（表结构 + 角色权限种子，幂等）'
Push-Location $BackendDir
try {
    & $Exe db upgrade
    if ($LASTEXITCODE -ne 0) { Write-Die 'db upgrade 失败：请确认数据库已启动、连接串正确。' }
} finally {
    Pop-Location
}
Write-Ok '元数据库已就绪'

# ------------------------------------------------------------------ 4. 后端三门进程
Write-Step '启动后端进程（run / worker / beat）'
$runningRoles = Get-RunningRoles
foreach ($role in @('run', 'worker', 'beat')) {
    $already = ($runningRoles -contains $role)
    if ($role -eq 'run' -and (Get-ListeningPid -Port $ApiPort)) { $already = $true }
    if ($already) { Write-Skip "$role 已在运行" ; continue }
    Start-Role -Role $role
}

Write-Step "等待后端就绪（http://127.0.0.1:$ApiPort/health）"
if (Wait-Backend -Port $ApiPort) {
    Write-Ok '后端已就绪'
} else {
    Write-Warn "后端 40 秒内未就绪，请查看 logs\run.log 中的报错"
}

# ------------------------------------------------------------------ 5. 前端
if (-not $SkipFrontend) {
    Write-Step '启动前端 dev server'
    if (Get-ListeningPid -Port $WebPort) {
        Write-Skip "端口 $WebPort 已在监听，前端跳过"
    } else {
        $pm = Resolve-PackageManager
        if (-not (Test-Path (Join-Path $FrontendDir 'node_modules'))) {
            Write-Step "首次运行，安装前端依赖（$pm install，可能较慢）"
            Push-Location $FrontendDir
            try { & $pm install } finally { Pop-Location }
            if ($LASTEXITCODE -ne 0) { Write-Die "$pm install 失败" }
        }
        $log = Join-Path $LogDir 'frontend.log'
        Start-Process -FilePath $env:ComSpec `
            -ArgumentList '/c', "$pm run dev > `"$log`" 2>&1" `
            -WorkingDirectory $FrontendDir -WindowStyle Hidden | Out-Null
        Write-Ok "前端已启动（$pm run dev）   日志 logs\frontend.log"
    }
} else {
    Write-Skip '按参数要求跳过前端'
}

# ------------------------------------------------------------------ 汇总
Write-Host ''
Write-Host '  启动完成' -ForegroundColor White
Write-Host '  ------------------------------------------------' -ForegroundColor DarkGray
Write-Host "  后端 API   http://localhost:$ApiPort        （健康检查 /health）" -ForegroundColor Gray
Write-Host "  前端界面   http://localhost:$WebPort" -ForegroundColor Gray
Write-Host "  默认账号   admin / admin" -ForegroundColor Gray
Write-Host "  运行日志   logs\" -ForegroundColor Gray
Write-Host ''
