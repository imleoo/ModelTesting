#!/bin/bash
# 容器 PID1：拉起 materials-server / api-server / Next.js 三个进程，
# 任意一个退出就整体退出（让 Docker 的 restart policy 接管），并在收到
# 终止信号时把三个子进程一起收掉，避免僵尸进程。
set -euo pipefail

mkdir -p "$(dirname "$DB_PATH")" "$REPORTS_ROOT"

pids=()

cleanup() {
  trap - TERM INT
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait
}
trap cleanup TERM INT

/app/bin/materials-server -addr ":${MATERIALS_PORT}" -root "$SUITES_ROOT" &
pids+=("$!")

/app/bin/api-server \
  -addr ":${API_PORT}" \
  -db "$DB_PATH" \
  -suites-root "$SUITES_ROOT" \
  -reports-root "$REPORTS_ROOT" \
  -materials-base-url "http://127.0.0.1:${MATERIALS_PORT}" \
  -repo-root /app \
  -auth-token "$AUTH_TOKEN" &
pids+=("$!")

PORT="$WEB_PORT" HOSTNAME=0.0.0.0 node /app/web/server.js &
pids+=("$!")

# 任意一个进程退出就返回（触发容器退出），而不是等所有进程都退出。
wait -n "${pids[@]}"
exit_code=$?
cleanup
exit "$exit_code"
