#!/usr/bin/env bash
# 可复现的素材托管服务验收脚本。
# 用法：从仓库根目录执行 `bash suites/kimi-k3/materials/verify_hosting.sh`
# 行为：编译 cmd/materials-server，在本地临时端口启动，下载两个素材并与源文件比对 sha256，
# 验证明文路径穿越、百分号编码路径穿越（suiteID=%2e%2e）、未知 suite_id 均被明确拒绝
# （400/403/404 之一，且响应体不含目标文件内容），最后停止进程并清理。
# 任何一步失败都会以非零退出码结束，便于 CI/人工复核判断整体是否通过。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

PORT="${VERIFY_PORT:-18453}"
BIN="$(mktemp -t materials-server-verify.XXXXXX 2>/dev/null || echo "/tmp/materials-server-verify.$$")"
SERVER_PID=""

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$SERVER_PID" ] && wait "$SERVER_PID" 2>/dev/null || true
  rm -f "$BIN" /tmp/verify_dl_image.png /tmp/verify_dl_video.mp4 /tmp/verify_hosting_server.log
}
trap cleanup EXIT

echo "[1/7] go build"
go build -o "$BIN" ./cmd/materials-server

echo "[2/7] start server on 127.0.0.1:${PORT}"
"$BIN" -addr ":${PORT}" -root suites >/tmp/verify_hosting_server.log 2>&1 &
SERVER_PID=$!
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

echo "[3/7] download image_qa_v1.png"
IMG_STATUS=$(curl -sS -o /tmp/verify_dl_image.png -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/image_qa_v1.png")
check "image http status" "200" "$IMG_STATUS"
IMG_SHA_DL=$(shasum -a 256 /tmp/verify_dl_image.png | awk '{print $1}')
IMG_SHA_SRC=$(shasum -a 256 suites/kimi-k3/materials/image_qa_v1.png | awk '{print $1}')
check "image sha256 matches source" "$IMG_SHA_SRC" "$IMG_SHA_DL"

echo "[4/7] download video_qa_v1.mp4"
VID_STATUS=$(curl -sS -o /tmp/verify_dl_video.mp4 -w "%{http_code}" "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/video_qa_v1.mp4")
check "video http status" "200" "$VID_STATUS"
VID_SHA_DL=$(shasum -a 256 /tmp/verify_dl_video.mp4 | awk '{print $1}')
VID_SHA_SRC=$(shasum -a 256 suites/kimi-k3/materials/video_qa_v1.mp4 | awk '{print $1}')
check "video sha256 matches source" "$VID_SHA_SRC" "$VID_SHA_DL"

# 拒绝类请求的判定：只接受明确的"拒绝"状态码（400/403/404），而不是宽松地把
# "只要不是 200 就算过"——502/500 等异常状态本不该出现，也不该被这条检查悄悄放行。
# 同时校验响应体没有把目标文件内容原样吐出来（双重确认没有内容泄露）。
reject_check() {
  local desc="$1" url="$2" leak_marker="$3"
  shift 3
  local body="/tmp/verify_reject_body.$$"
  local status
  status=$(curl -sS "$@" -o "$body" -w "%{http_code}" "$url")
  case "$status" in
    400|403|404) ;;
    *)
      echo "FAIL: $desc (unexpected status $status, want one of 400/403/404)"
      fail=1
      rm -f "$body"
      return
      ;;
  esac
  if [ -n "$leak_marker" ] && grep -q "$leak_marker" "$body" 2>/dev/null; then
    echo "FAIL: $desc (status $status but response body leaked expected marker '$leak_marker')"
    fail=1
  else
    echo "PASS: $desc (got $status, no content leak)"
  fi
  rm -f "$body"
}

echo "[5/7] path traversal (plain '..') must be rejected end-to-end"
# --path-as-is：禁止 curl 客户端在发送前折叠 URL 中的 ".." 片段，确保穿越 payload
# 真的原样发到服务端。-L：跟随服务端可能返回的重定向（net/http.ServeMux 会对含
# ".." 的路径先做一次 307 重定向到清理后的路径），验证端到端最终结果，而不是只看
# 第一跳的状态码。leak_marker 用 go.mod 里必然存在的 module 声明前缀做内容泄露检测。
reject_check "plain traversal ../../go.mod" \
  "http://127.0.0.1:${PORT}/materials/kimi-k3/v1/../../../go.mod" \
  "module github.com/leoobai/modeltestbed" \
  --path-as-is -L

echo "[6/7] path traversal via percent-encoded '..' (suiteID=%2e%2e) must be rejected"
# net/http.ServeMux 的 cleanPath 只处理明文路径段，不解码 %2e%2e；这类请求会带着
# suiteID == ".." 直接进入 handler，必须靠应用层 suiteIDPattern 校验挡住，
# 不能指望 ServeMux 帮忙清理。
reject_check "percent-encoded suiteID=.. traversal" \
  "http://127.0.0.1:${PORT}/materials/%2e%2e/v1/go.mod" \
  "module github.com/leoobai/modeltestbed"

echo "[7/7] unknown suite_id must be rejected"
reject_check "unknown suite_id" \
  "http://127.0.0.1:${PORT}/materials/does-not-exist/v1/x.png" \
  ""

if [ "$fail" -eq 0 ]; then
  echo "=== ALL CHECKS PASSED ==="
else
  echo "=== SOME CHECKS FAILED ==="
fi
exit "$fail"
