<#
.SYNOPSIS
    智观 BI（Insight Knowledge BI）停止脚本。

.DESCRIPTION
    依次停止：
      1. 后端三门进程 run / worker / beat
      2. 前端 Vite dev server        （-KeepFrontend 可保留）
      3. PostgreSQL / Redis 容器      （仅当显式传入 -IncludeInfra）

    默认不动容器：容器里是元数据库，随时可以再起，停掉只会让下次启动变慢。

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts\stop.ps1

.EXAMPLE
    # 连数据库容器一起停
    powershell -ExecutionPolicy Bypass -File scripts\stop.ps1 -IncludeInfra
#>
[CmdletBinding()]
param(
    [switch]$KeepFrontend,
    [switch]$IncludeInfra
)

$ErrorActionPreference = 'Stop'

$Root        = Split-Path -Parent $PSScriptRoot
$ComposeFile = Join-Path $Root 'docker\docker-compose.yml'
$WebPort     = 3000

function Write-Step { param([string]$Message) Write-Host "==> $Message" -ForegroundColor Cyan }
function Write-Ok   { param([string]$Message) Write-Host "    OK    $Message" -ForegroundColor Green }
function Write-Skip { param([string]$Message) Write-Host "    SKIP  $Message" -ForegroundColor DarkGray }

Write-Host ''
Write-Host '  智观 BI · 停止' -ForegroundColor White
Write-Host '  ------------------------------------------------' -ForegroundColor DarkGray

# ------------------------------------------------------------------ 1. 后端进程
Write-Step '停止后端进程（run / worker / beat）'
$backendProcs = @(Get-Process superset-dev -ErrorAction SilentlyContinue)
if ($backendProcs.Count -gt 0) {
    $backendProcs | Stop-Process -Force
    Write-Ok "已停止 $($backendProcs.Count) 个后端进程"
} else {
    Write-Skip '后端进程未在运行'
}

# ------------------------------------------------------------------ 2. 前端进程
if ($KeepFrontend) {
    Write-Skip '按参数要求保留前端'
} else {
    Write-Step '停止前端 dev server'
    $targets = @()
    Get-CimInstance Win32_Process -Filter "Name='node.exe'" -ErrorAction SilentlyContinue |
        ForEach-Object {
            # 只清理指向本项目前端的 vite 进程，避免误杀其它 Node 应用
            if ($_.CommandLine -and $_.CommandLine -match 'vite' -and $_.CommandLine -match 'superset-go') {
                $targets += $_.ProcessId
            }
        }
    # 兜底：监听 3000 且确实是 vite 的进程
    $conn = Get-NetTCPConnection -LocalPort $WebPort -State Listen -ErrorAction SilentlyContinue
    foreach ($c in $conn) {
        $p = Get-CimInstance Win32_Process -Filter "ProcessId=$($c.OwningProcess)" -ErrorAction SilentlyContinue
        if ($p -and $p.CommandLine -and $p.CommandLine -match 'vite') { $targets += $p.ProcessId }
    }
    $targets = $targets | Sort-Object -Unique
    if ($targets.Count -gt 0) {
        foreach ($t in $targets) { Stop-Process -Id $t -Force -ErrorAction SilentlyContinue }
        Write-Ok "已停止前端 vite（PID $($targets -join ', ')）"
    } else {
        Write-Skip "端口 $WebPort 上没有本项目的 vite 进程"
    }
}

# ------------------------------------------------------------------ 3. 容器
if ($IncludeInfra) {
    Write-Step '停止基础设施容器'
    if (Get-Command docker -ErrorAction SilentlyContinue) {
        docker compose -f $ComposeFile down 2>&1 | Out-Null
        Write-Ok '容器已停止（数据卷保留）'
    } else {
        Write-Skip '未检测到 docker'
    }
} else {
    Write-Skip '容器保持运行（如需停止请加 -IncludeInfra）'
}

Write-Host ''
Write-Host '  已停止' -ForegroundColor White
Write-Host ''
