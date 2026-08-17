#!/usr/bin/env bash
# 可复现的素材托管服务验收脚本。
# 用法：从仓库根目录执行 `bash suites/kimi-k3/materials/verify_hosting.sh`
# 行为：编译 cmd/materials-server，在本地临时端口启动，下载两个素材并与源文件比对 sha256，
# 验证路径穿越与未知 suite_id 请求均被拒绝，最后停止进程并清理。
# 任何一步失败都会以非零退出码结束，便于 CI/人工复核判断整体是否通过。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

PORT="${VERIFY_PORT:-18453}"
BIN="$(mktemp -t materials-server-verify.XXXXXX 2>/dev/null || echo "/tmp/materials-server-verify.$$")"

echo "[1/6] go build"
go build -o "$BIN" ./cmd/materials-server

echo "[2/6] start server on 127.0.0.1:${PORT}"
"$BIN" -addr ":${PORT}" -root suites >/tmp/verify_hosting_server.log 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true; rm -f "$BIN"' EXIT
sleep 1

fail=0

check() {
  local desc="$1" expect="$2" actual="$3"
  if [ "$expect" = "$actual" ]; then
    echo "PASS: $desc (got $actual)"
  else
    echo "FAIL: $desc (expect $expect, got $actual)"
    fail=1
  fi
}

echo "[3/6] download image_qa_v1.png"
IMG_STATUS=$(curl -sS -o /tmp/verify_dl_image.png -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/image_qa_v1.png")
check "image http status" "200" "$IMG_STATUS"
IMG_SHA_DL=$(shasum -a 256 /tmp/verify_dl_image.png | awk '{print $1}')
IMG_SHA_SRC=$(shasum -a 256 suites/kimi-k3/materials/image_qa_v1.png | awk '{print $1}')
check "image sha256 matches source" "$IMG_SHA_SRC" "$IMG_SHA_DL"

echo "[4/6] download video_qa_v1.mp4"
VID_STATUS=$(curl -sS -o /tmp/verify_dl_video.mp4 -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/video_qa_v1.mp4")
check "video http status" "200" "$VID_STATUS"
VID_SHA_DL=$(shasum -a 256 /tmp/verify_dl_video.mp4 | awk '{print $1}')
VID_SHA_SRC=$(shasum -a 256 suites/kimi-k3/materials/video_qa_v1.mp4 | awk '{print $1}')
check "video sha256 matches source" "$VID_SHA_SRC" "$VID_SHA_DL"

echo "[5/6] path traversal must be rejected"
TRAVERSAL_STATUS=$(curl -sS -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/../../../go.mod")
if [ "$TRAVERSAL_STATUS" = "200" ]; then
  echo "FAIL: path traversal request returned 200 (should be rejected)"
  fail=1
else
  echo "PASS: path traversal rejected (got $TRAVERSAL_STATUS)"
fi

echo "[6/6] unknown suite_id must be rejected"
UNKNOWN_STATUS=$(curl -sS -o /dev/null -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/does-not-exist/v1/x.png")
if [ "$UNKNOWN_STATUS" = "200" ]; then
  echo "FAIL: unknown suite_id request returned 200 (should be rejected)"
  fail=1
else
  echo "PASS: unknown suite_id rejected (got $UNKNOWN_STATUS)"
fi

rm -f /tmp/verify_dl_image.png /tmp/verify_dl_video.mp4

if [ "$fail" -eq 0 ]; then
  echo "=== ALL CHECKS PASSED ==="
else
  echo "=== SOME CHECKS FAILED ==="
fi
exit "$fail"
