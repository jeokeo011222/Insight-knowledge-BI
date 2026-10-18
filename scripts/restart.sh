#!/usr/bin/env bash
#
# 智观 BI（Insight Knowledge BI）重启脚本 —— Linux / macOS
# 等价于 stop.sh && start.sh，参数原样转发。
#
#   ./scripts/restart.sh                      完整重启（含容器/编译）
#   ./scripts/restart.sh --skip-build         不重编译，只重启进程
#   ./scripts/restart.sh --skip-frontend      不动前端 dev server
#   ./scripts/restart.sh --skip-infra         使用外部数据库时
#   ./scripts/restart.sh --include-infra      连容器一起重启
#
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SKIP_INFRA=0
SKIP_BUILD=0
SKIP_FRONTEND=0
INCLUDE_INFRA=0
for arg in "$@"; do
  case "$arg" in
    --skip-infra)    SKIP_INFRA=1 ;;
    --skip-build)    SKIP_BUILD=1 ;;
    --skip-frontend) SKIP_FRONTEND=1 ;;
    --include-infra) INCLUDE_INFRA=1 ;;
    *) echo "未知参数: $arg" >&2; exit 1 ;;
  esac
done

echo
echo '  智观 BI · 重启'
echo '  ------------------------------------------------'

stop_args=()
[ "$INCLUDE_INFRA" = "1" ] && stop_args+=(--include-infra)
[ "$SKIP_FRONTEND" = "1" ] && stop_args+=(--keep-frontend)
"$DIR/stop.sh" "${stop_args[@]:-}" 2>/dev/null || true

# 给端口和文件句柄一点释放时间
sleep 2

start_args=()
[ "$SKIP_INFRA" = "1" ]    && start_args+=(--skip-infra)
[ "$SKIP_BUILD" = "1" ]    && start_args+=(--skip-build)
[ "$SKIP_FRONTEND" = "1" ] && start_args+=(--skip-frontend)
"$DIR/start.sh" "${start_args[@]:-}"
