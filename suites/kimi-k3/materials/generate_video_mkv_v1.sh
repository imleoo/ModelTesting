#!/usr/bin/env bash
# 复现 video_mkv_v1.mkv（v1.4.0 新增，multimodal.video_mkv_format 用例的固定
# 素材）的生成步骤。对应《Kimi-K3-接口兼容性测试总结-0820.md》2.3：内部网关曾
# 对 MKV（matroska）视频容器返回笼统 400，本素材用来验证该容器格式是否被
# 正确接受并解析。
#
# 画面内容沿用与 image_qa_v1 / video_qa_v1 相同的『固定印刷体数字』方案
# （见 manifest.json answer_match_definitions.digits_exact），保证答案可
# 程序化比对，不依赖模型开放式描述。
#
# 依赖：python3 + Pillow（生成数字画面 PNG），ffmpeg（编码为 matroska 容器）。
# 用法：从仓库根目录执行 `bash suites/kimi-k3/materials/generate_video_mkv_v1.sh`。
# 注意：ffmpeg/libx264 的输出不保证跨版本逐字节确定性，重新生成后请重新计算
# sha256/size 并同步更新 manifest.json 里 video_mkv_v1 条目的对应字段。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRAME_PNG="$(mktemp -t video_mkv_v1_frame).png"
OUT_MKV="$SCRIPT_DIR/video_mkv_v1.mkv"
DIGITS="3849"

trap 'rm -f "$FRAME_PNG"' EXIT

python3 - "$FRAME_PNG" "$DIGITS" <<'PYEOF'
import sys
from PIL import Image, ImageDraw, ImageFont

out_path, digits = sys.argv[1], sys.argv[2]
W, H = 800, 600
bg = (245, 245, 245)
fg = (10, 10, 10)

im = Image.new("RGB", (W, H), bg)
draw = ImageDraw.Draw(im)
font = ImageFont.truetype("/System/Library/Fonts/SFNSMono.ttf", 220)
bbox = draw.textbbox((0, 0), digits, font=font)
tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
x = (W - tw) / 2 - bbox[0]
y = (H - th) / 2 - bbox[1]
draw.text((x, y), digits, font=font, fill=fg)
im.save(out_path)
PYEOF

ffmpeg -y -loop 1 -i "$FRAME_PNG" -t 3 -r 25 -c:v libx264 -pix_fmt yuv420p -crf 30 -f matroska "$OUT_MKV"

echo "生成完成: $OUT_MKV"
echo "sha256: $(shasum -a 256 "$OUT_MKV" | awk '{print $1}')"
echo "size_bytes: $(wc -c < "$OUT_MKV" | tr -d ' ')"
echo "base64_size_bytes: $(base64 < "$OUT_MKV" | wc -c | tr -d ' ')"
echo "expected_answer（人工核对画面后确认与 DIGITS 一致）: $DIGITS"
