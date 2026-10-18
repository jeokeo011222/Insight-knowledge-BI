#!/usr/bin/env bash
#
# 智观 BI（Insight Knowledge BI）一键启动 —— Linux / macOS
#
# 幂等：已经在跑的组件会自动跳过。
#   1. 拉起 PostgreSQL / Redis 容器   （--skip-infra 跳过）
#   2. 编译后端二进制                  （--skip-build 跳过）
#   3. 迁移元数据库（幂等）
#   4. 启动后端 run / worker / beat
#   5. 启动前端 Vite dev server        （--skip-frontend 跳过）
#
# 用法：
#   ./scripts/start.sh
#   ./scripts/start.sh --skip-infra --skip-build
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
FRONTEND="$ROOT/frontend"
LOGDIR="$ROOT/logs"
BIN="$BACKEND/superset-dev"
COMPOSE="$ROOT/docker/docker-compose.yml"
API_PORT=8088
WEB_PORT=3000

SKIP_INFRA=0
SKIP_BUILD=0
SKIP_FRONTEND=0
for arg in "$@"; do
  case "$arg" in
    --skip-infra)    SKIP_INFRA=1 ;;
    --skip-build)    SKIP_BUILD=1 ;;
    --skip-frontend) SKIP_FRONTEND=1 ;;
    -h|--help)       sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "未知参数: $arg（可用：--skip-infra --skip-build --skip-frontend）" >&2; exit 1 ;;
  esac
done

step() { printf '\033[36m==> %s\033[0m\n' "$1"; }
ok()   { printf '\033[32m    OK    %s\033[0m\n' "$1"; }
skip() { printf '\033[90m    SKIP  %s\033[0m\n' "$1"; }
warn() { printf '\033[33m    WARN  %s\033[0m\n' "$1"; }
die()  { printf '\033[31m    FAIL  %s\033[0m\n' "$1"; exit 1; }

# 端口是否已被监听（用 bash 内建 /dev/tcp，无需 ss / lsof）
port_busy() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null && { exec 3<&-; return 0; } || return 1
}

role_running() { pgrep -f "superset-dev $1" >/dev/null 2>&1; }

echo
echo '  智观 BI · 启动'
echo '  ------------------------------------------------'

mkdir -p "$LOGDIR"

# ---------------------------------------------------------------- 1. 基础设施
if [ "$SKIP_INFRA" = "1" ]; then
  skip '按参数要求跳过容器'
elif ! command -v docker >/dev/null 2>&1; then
  warn '未检测到 docker，跳过容器。若你没有外部数据库，请先安装 Docker。'
else
  step '拉起基础设施容器（PostgreSQL 5533 / Redis 6379）'
  if docker compose -f "$COMPOSE" up -d >/dev/null 2>&1; then
    ok '容器已就绪'
  else
    warn 'docker compose 启动失败，请确认 5533 / 6379 未被占用'
  fi
fi

# ---------------------------------------------------------------- 2. 编译后端
if [ "$SKIP_BUILD" = "1" ]; then
  skip '按参数要求跳过编译'
else
  step '编译后端'
  command -v go >/dev/null 2>&1 || die '未找到 go，请安装 Go 1.22+'
  ( cd "$BACKEND" && go build -o "$BIN" ./cmd/superset ) || die 'go build 失败'
  ok '已生成 backend/superset-dev'
fi

[ -x "$BIN" ] || die "后端二进制不存在或不可执行：$BIN"

# ---------------------------------------------------------------- 3. 迁移元数据库
step '迁移元数据库（幂等）'
( cd "$BACKEND" && "$BIN" db upgrade ) || die 'db upgrade 失败：请确认数据库已启动、连接串正确'
ok '元数据库已就绪'

# ---------------------------------------------------------------- 4. 后端三门进程
step '启动后端进程（run / worker / beat）'
start_role() {
  local role="$1" pid
  ( cd "$BACKEND" && exec nohup "$BIN" "$role" ) >"$LOGDIR/$role.log" 2>&1 &
  pid=$!
  echo "$pid" > "$LOGDIR/$role.pid"
  ok "$role 已启动（pid $pid，日志 logs/$role.log）"
}
for role in run worker beat; do
  if [ "$role" = "run" ] && port_busy "$API_PORT"; then
    skip 'run 已在运行'
  elif role_running "$role"; then
    skip "$role 已在运行"
  else
    start_role "$role"
  fi
done

step "等待后端就绪（http://127.0.0.1:$API_PORT/health）"
backend_ready=0
for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then backend_ready=1; break; fi
  sleep 1
done
[ "$backend_ready" = "1" ] && ok '后端已就绪' || warn '后端 40 秒内未就绪，请查看 logs/run.log'

# ---------------------------------------------------------------- 5. 前端
if [ "$SKIP_FRONTEND" = "1" ]; then
  skip '按参数要求跳过前端'
elif port_busy "$WEB_PORT"; then
  skip "端口 $WEB_PORT 已在监听，前端跳过"
else
  step '启动前端 dev server'
  if   command -v pnpm >/dev/null 2>&1; then PM=pnpm
  elif command -v npm  >/dev/null 2>&1; then PM=npm
  else die '未找到 pnpm / npm，请先安装 Node.js 18+'
  fi
  if [ ! -d "$FRONTEND/node_modules" ]; then
    step "首次运行，安装前端依赖（$PM install，可能较慢）"
    ( cd "$FRONTEND" && "$PM" install ) || die "$PM install 失败"
  fi
  ( cd "$FRONTEND" && exec nohup "$PM" run dev ) >"$LOGDIR/frontend.log" 2>&1 &
  echo $! > "$LOGDIR/frontend.pid"
  ok "前端已启动（$PM run dev，pid $(cat "$LOGDIR/frontend.pid")）"
fi

echo
echo '  启动完成'
echo '  ------------------------------------------------'
echo "  后端 API   http://localhost:$API_PORT        （健康检查 /health）"
echo "  前端界面   http://localhost:$WEB_PORT"
echo "  默认账号   admin / admin"
echo "  运行日志   logs/"
echo
