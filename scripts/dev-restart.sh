#!/usr/bin/env bash
# dev-restart.sh —— 一条命令重启 studio 开发后端（studiod）。
#
# 把「每次开发会话手动做的那套」固化成幂等脚本：编译 → 杀旧进程 → 以真实模型
# 模式重新拉起 → 健康门等就绪 → 打印端口状态。前端 Vite(:5173) 不由本脚本
# 管理（它 HMR 常驻），只做一次「清理重复实例」的巡检提示。
#
# 用法：
#   DEEPSEEK_API_KEY=sk-xxx bash scripts/dev-restart.sh
# 前置（缺任一则快速失败）：
#   - /tmp/studio-enc-key.txt    STUDIO_CONFIG_ENC_KEY（配置加密密钥，持久化在文件里，勿写死进脚本）
#   - /tmp/studio-jwt-secret.txt JWT_SECRET（签发登录 token 的密钥，同上）
#   - $DEEPSEEK_API_KEY          真实文本模型 key（从环境读，绝不入库/入仓）
#
# 环境变量逐条说明（拉起 studiod 时注入，见下方 launch 段）：
#   HTTP_ADDR=:8083                  后端监听端口（Vite /api 代理指向它）
#   PG_URL=postgres://…/studio_dev   开发库（本机 172.17.0.3 上的 PG 容器）
#   PER_USER_LIMIT=6000              放宽单用户配额，避免开发时反复撞限额
#   STUDIO_CONFIG_ENC_KEY / JWT_SECRET  见上，从文件读
#   PLATFORM_ADMIN_EMAILS            平台管理员白名单（开发用占位邮箱）
#   API_KEY / DEEPSEEK_API_KEY / PROVIDER / MODEL  真实 deepseek 文本模型接线。
#
# ⚠️ 杀进程只按「精确 PID + 精确 exe 路径」来（见 kill_old_studiod）：
#   绝不用 `pkill -f studiod` —— 那个模式会匹配到本脚本自身（命令行含 "studiod"），
#   等于自杀。只认 exe 真身是 /tmp/studiod 的进程，逐个按 PID kill。
#
# 校验：`bash -n scripts/dev-restart.sh`（仅语法）。真正的 test-run 由人手动跑，
# 因为它会杀掉/重拉当前正在被观察的 studiod。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN=/tmp/studiod
LOG=/tmp/studiod-dev.log
ENC_KEY_FILE=/tmp/studio-enc-key.txt
JWT_SECRET_FILE=/tmp/studio-jwt-secret.txt
HTTP_PORT=8083
VITE_PORT=5173

log() { echo "[dev-restart] $*"; }
die() { echo "[dev-restart] ERROR: $*" >&2; exit 1; }

# ── 0. 前置校验：密钥文件 + DEEPSEEK_API_KEY 齐备，否则快速失败 ───────────────
[ -r "$ENC_KEY_FILE" ] || die "缺少配置加密密钥文件 $ENC_KEY_FILE（STUDIO_CONFIG_ENC_KEY）"
[ -r "$JWT_SECRET_FILE" ] || die "缺少 JWT 密钥文件 $JWT_SECRET_FILE（JWT_SECRET）"
[ -n "${DEEPSEEK_API_KEY:-}" ] || die "环境变量 \$DEEPSEEK_API_KEY 为空——先 export 真实文本模型 key"

# ── 1. 编译（GOWORK=off 对齐 CI，用子仓自身 go.mod 而非 workspace）────────────
log "编译 studiod → $BIN"
( cd "$REPO_ROOT" && GOWORK=off go build -o "$BIN" ./cmd/studiod )

# ── 2. 杀旧进程：只认 exe 真身 == /tmp/studiod 的 PID，逐个 kill ─────────────
kill_old_studiod() {
  local killed=0 pid exe
  # pgrep 只用来「产候选 PID」，最终真身以 /proc/<pid>/exe 的 readlink 为准，
  # 避免误杀命令行里恰好含 "studiod" 的无关进程（比如本脚本）。
  for pid in $(pgrep -f "$BIN" || true); do
    exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
    if [ "$exe" = "$BIN" ]; then
      log "杀掉旧 studiod（pid=$pid, exe=$exe）"
      kill "$pid" 2>/dev/null || true
      killed=1
    fi
  done
  if [ "$killed" = 1 ]; then
    sleep 1
    # 兜底：仍在则 KILL。
    for pid in $(pgrep -f "$BIN" || true); do
      exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
      [ "$exe" = "$BIN" ] && kill -9 "$pid" 2>/dev/null || true
    done
  else
    log "没有正在运行的旧 studiod"
  fi
}
kill_old_studiod
sleep 1

# ── 3. 以真实模型模式重新拉起（detached：setsid + & + disown，日志落文件）──────
log "拉起 studiod（真实 deepseek 文本 + minimax BYOK 图/音），日志 → $LOG"
: > "$LOG"
(
  cd "$REPO_ROOT"
  HTTP_ADDR=":$HTTP_PORT" \
  PG_URL="postgres://postgres:pw@172.17.0.3:5432/studio_dev?sslmode=disable" \
  PER_USER_LIMIT=6000 \
  STUDIO_CONFIG_ENC_KEY="$(cat "$ENC_KEY_FILE")" \
  JWT_SECRET="$(cat "$JWT_SECRET_FILE")" \
  PLATFORM_ADMIN_EMAILS=pfadmin@s.com \
  API_KEY="$DEEPSEEK_API_KEY" \
  DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
  PROVIDER=deepseek \
  MODEL=deepseek-chat \
  setsid "$BIN" >>"$LOG" 2>&1 &
  disown
)

# ── 4. 健康门：dev 无 /healthz，登录 200 即就绪信号（轮询 ~30s）────────────────
log "等待就绪（POST /api/auth/login → 200，超时 ~30s）…"
ready=0
for _ in $(seq 1 30); do
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "http://localhost:$HTTP_PORT/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d '{"email":"demo@studio.com","password":"SmokeP2A#2026"}' 2>/dev/null || true)"
  if [ "$code" = "200" ]; then ready=1; break; fi
  sleep 1
done
if [ "$ready" = 1 ]; then
  log "OK —— studiod 已就绪（登录 200）"
else
  log "FAIL —— 30s 内未就绪；最后 20 行日志："
  tail -n 20 "$LOG" >&2 || true
  exit 1
fi

# ── 5. Vite 巡检：保留真正在监听 :5173 的那一个，重复实例只提示不强杀 ──────────
vite_pids="$(ss -ltnp 2>/dev/null | grep ":$VITE_PORT " | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u || true)"
if [ -z "$vite_pids" ]; then
  log "注意：没有进程在监听 :$VITE_PORT —— 需要另起 (cd web && pnpm dev)"
else
  count="$(echo "$vite_pids" | wc -w | tr -d ' ')"
  if [ "$count" -gt 1 ]; then
    log "注意：检测到多个 Vite 实例在 :$VITE_PORT（pid: $vite_pids）——请人工确认后清理多余的，本脚本不强杀"
  else
    log "Vite 单实例正常（pid=$vite_pids）"
  fi
fi

# ── 6. 打印最终端口态 ─────────────────────────────────────────────────────────
log "当前端口状态："
ss -ltnp 2>/dev/null | grep -E ":$HTTP_PORT|:$VITE_PORT" || log "（未发现 :$HTTP_PORT / :$VITE_PORT 监听）"
