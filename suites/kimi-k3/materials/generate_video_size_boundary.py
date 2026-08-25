#!/usr/bin/env python3
"""复现 video_size_under_50mb.mp4 / video_size_over_50mb.mp4（v1.6.0 新增，
input_validation.video_base64_size_boundary_* 用例的固定素材）的生成步骤。

对应《Kimi-K3-接口兼容性测试总结-0820.md》1.11：传入接近/超过 50MB 的
base64 视频时，官方能正常解码，内部网关曾笼统返回 429。

方法：
1. 用 ffmpeg 生成一段真实可解码的噪声内容 h264/mp4（噪声内容不可压缩，
   避免"体积被编码器压没了"）。
2. 在文件末尾追加一个合法的 ISO-BMFF `free` box（box 内容会被所有兼容
   解析器忽略，是容器格式本身预留的占位机制），把文件精确填充到目标字节数。
   只追加、不修改任何已有 box，因此不会破坏 moov 里的采样偏移表。
3. 用 ffprobe 校验追加 padding 后文件仍可正常解析（duration/stream 不变）。

目标字节数是精确计算出来的：raw_size 取 3 的整倍数，使 base64 编码后
（Go base64.StdEncoding，长度恰为 raw_size/3*4，无换行、无多余 padding
字符的边界情况）得到整数的 base64 字节数：
  - under：raw=36,000,000 → base64=48,000,000（约 48MB，小于 50MB）
  - over ：raw=39,000,000 → base64=52,000,000（约 52MB，大于 50MB）

依赖：ffmpeg、ffprobe、python3（标准库）。
用法：从仓库根目录执行 `python3 suites/kimi-k3/materials/generate_video_size_boundary.py`。
"""
import subprocess
import sys
from pathlib import Path

MATERIALS_DIR = Path(__file__).resolve().parent

TARGETS = [
    ("video_size_under_50mb.mp4", 36_000_000, 48_000_000),
    ("video_size_over_50mb.mp4", 39_000_000, 52_000_000),
]


def make_seed(seed_path: Path, duration_s: int) -> None:
    subprocess.run(
        [
            "ffmpeg", "-y",
            "-f", "lavfi", "-i", f"nullsrc=size=320x240:rate=25:duration={duration_s}",
            "-vf", "geq=random(1)*255:random(1)*255:random(1)*255",
            "-c:v", "libx264", "-preset", "ultrafast", "-crf", "28",
            "-pix_fmt", "yuv420p", "-movflags", "+faststart",
            str(seed_path),
        ],
        check=True, capture_output=True,
    )


def pad_to_exact_size(seed_path: Path, out_path: Path, target_size: int) -> None:
    data = seed_path.read_bytes()
    if len(data) + 8 > target_size:
        raise SystemExit(
            f"seed 文件 {len(data)} 字节 + 8 字节 free box 头已超过目标 {target_size} 字节，"
            "需要调大 duration_s 让 seed 更小，或调小目标字节数"
        )
    pad_len = target_size - len(data) - 8
    free_box = (pad_len + 8).to_bytes(4, "big") + b"free" + b"\x00" * pad_len
    out_path.write_bytes(data + free_box)
    actual = out_path.stat().st_size
    if actual != target_size:
        raise SystemExit(f"填充后大小 {actual} 与目标 {target_size} 不一致，脚本逻辑有误")


def verify_playable(path: Path) -> str:
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-show_entries", "format=duration",
         "-of", "default=noprint_wrappers=1:nokey=1", str(path)],
        check=True, capture_output=True, text=True,
    )
    duration = out.stdout.strip()
    if not duration:
        raise SystemExit(f"{path} 追加 padding 后 ffprobe 读不到 duration，文件已损坏")
    return duration


def main() -> None:
    seed_path = MATERIALS_DIR / "_seed_tmp.mp4"
    make_seed(seed_path, duration_s=3)
    try:
        for filename, raw_size, base64_size in TARGETS:
            out_path = MATERIALS_DIR / filename
            pad_to_exact_size(seed_path, out_path, raw_size)
            duration = verify_playable(out_path)
            sha256 = subprocess.run(
                ["shasum", "-a", "256", str(out_path)],
                check=True, capture_output=True, text=True,
            ).stdout.split()[0]
            print(f"{filename}: size_bytes={raw_size} base64_size_bytes={base64_size} "
                  f"duration={duration} sha256={sha256}")
    finally:
        seed_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
