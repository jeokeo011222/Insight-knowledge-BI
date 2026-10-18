<#
.SYNOPSIS
    智观 BI（Insight Knowledge BI）重启脚本 = 先 stop，再 start。

.DESCRIPTION
    参数会原样转发给 stop.ps1 / start.ps1：
      -SkipBuild      跳过编译（只重启进程，适合改前端或改配置）
      -SkipInfra      跳过容器（使用外部数据库时）
      -SkipFrontend   本次不重启前端（前端会被保留不杀）
      -IncludeInfra   重启时连数据库容器一起 stop / start

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts\restart.ps1

.EXAMPLE
    # 改完后端代码后：重编译 + 重启
    powershell -ExecutionPolicy Bypass -File scripts\restart.ps1

.EXAMPLE
    # 只重启后端，前端 dev server 不动
    powershell -ExecutionPolicy Bypass -File scripts\restart.ps1 -SkipFrontend
#>
[CmdletBinding()]
param(
    [switch]$SkipInfra,
    [switch]$SkipBuild,
    [switch]$SkipFrontend,
    [switch]$IncludeInfra
)

$ErrorActionPreference = 'Stop'

Write-Host ''
Write-Host '  智观 BI · 重启' -ForegroundColor White
Write-Host '  ------------------------------------------------' -ForegroundColor DarkGray

# ---- stop ----
$stopArgs = @()
if ($IncludeInfra)  { $stopArgs += '-IncludeInfra' }
if ($SkipFrontend)  { $stopArgs += '-KeepFrontend' }
& (Join-Path $PSScriptRoot 'stop.ps1') @stopArgs

# 给端口和文件句柄一点释放时间，否则紧接着启动容易撞"端口被占用"
Start-Sleep -Seconds 2

# ---- start ----
$startArgs = @()
if ($SkipInfra)    { $startArgs += '-SkipInfra' }
if ($SkipBuild)    { $startArgs += '-SkipBuild' }
if ($SkipFrontend) { $startArgs += '-SkipFrontend' }
& (Join-Path $PSScriptRoot 'start.ps1') @startArgs
