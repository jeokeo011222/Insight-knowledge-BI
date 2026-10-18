#!/usr/bin/env bash
#
# 智观 BI（Insight Knowledge BI）停止脚本 —— Linux / macOS
#
#   ./scripts/stop.sh                 停止后端 + 前端（容器保留）
#   ./scripts/stop.sh --keep-frontend 只停后端
#   ./scripts/stop.sh --include-infra 连数据库容器一起停
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOGDIR="$ROOT/logs"
COMPOSE="$ROOT/docker/docker-compose.yml"
WEB_PORT=3000

KEEP_FRONTEND=0
INCLUDE_INFRA=0
for arg in "$@"; do
  case "$arg" in
    --keep-frontend)  KEEP_FRONTEND=1 ;;
    --include-infra)  INCLUDE_INFRA=1 ;;
    -h|--help)        sed -n '2,9p' "$0"; exit 0 ;;
    *) echo "未知参数: $arg（可用：--keep-frontend --include-infra）" >&2; exit 1 ;;
  esac
done

step() { printf '\033[36m==> %s\033[0m\n' "$1"; }
ok()   { printf '\033[32m    OK    %s\033[0m\n' "$1"; }
skip() { printf '\033[90m    SKIP  %s\033[0m\n' "$1"; }

kill_pidfile() {
  local name="$1" file="$LOGDIR/$1.pid"
  if [ -f "$file" ]; then
    local pid; pid="$(cat "$file" 2>/dev/null || true)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      ok "已停止 $name（pid $pid）"
      rm -f "$file"
      return 0
    fi
    rm -f "$file"
  fi
  return 1
}

echo
echo '  智观 BI · 停止'
echo '  ------------------------------------------------'

# ---------------------------------------------------------------- 1. 后端进程
step '停止后端进程（run / worker / beat）'
stopped=0
for role in run worker beat; do
  if kill_pidfile "$role"; then stopped=$((stopped + 1)); continue; fi
  if pkill -f "superset-dev $role" 2>/dev/null; then
    ok "$role 已按进程名停止"
    stopped=$((stopped + 1))
  fi
done
[ "$stopped" -gt 0 ] || skip '后端进程未在运行'

# ---------------------------------------------------------------- 2. 前端进程
if [ "$KEEP_FRONTEND" = "1" ]; then
  skip '按参数要求保留前端'
else
  step '停止前端 dev server'
  if kill_pidfile frontend; then :; elif pkill -f 'vite' 2>/dev/null; then ok '前端 vite 已停止'; else skip '端口 3000 上没有本项目的前端进程'; fi
fi

# ---------------------------------------------------------------- 3. 容器
if [ "$INCLUDE_INFRA" = "1" ]; then
  step '停止基础设施容器'
  if command -v docker >/dev/null 2>&1; then
    docker compose -f "$COMPOSE" down >/dev/null 2>&1 && ok '容器已停止（数据卷保留）' || skip '容器未在运行'
  else
    skip '未检测到 docker'
  fi
else
  skip '容器保持运行（如需停止请加 --include-infra）'
fi

echo
echo '  已停止'
echo
