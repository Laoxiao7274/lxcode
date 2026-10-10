"""动效取证 · 帧选取

用法：
  python tools/motion_pick.py <scene>                  # 打印帧间 diff 时间轴（定位动作起点）
  python tools/motion_pick.py <scene> pick <base-ms>    # 以 base-ms 为动作起点，落盘 motion-<scene>-<n>.png

帧序列 = motion_capture.sh 抽出的原生帧（-vsync 0，与 ffprobe 的 pts_time 一一对应）。
落盘的四张：动作起点 +0 / +120 / +400 / +1000ms（取时间上最近的原生帧，并打印偏差）。
"""
import os
import shutil
import subprocess
import sys

import numpy as np
from PIL import Image

OUT = r"C:\Users\xzy\Desktop\my\lxcode\android"
THRESHOLD = 0.30  # 帧间平均像素差（0~255）超过它 = 画面在变


FPS = 30.0


def pts_times(scene):
    """抽帧是 ffmpeg 的 30fps 网格：第 i 帧（1 起）= i/30 秒。"""
    d = os.path.join(OUT, "frames-" + scene)
    return [i / FPS for i in range(1, len(os.listdir(d)) + 1)]


def main():
    scene = sys.argv[1]
    mode = sys.argv[2] if len(sys.argv) > 2 else "show"
    base_ms = float(sys.argv[3]) if len(sys.argv) > 3 else 0.0

    d = os.path.join(OUT, "frames-" + scene)
    frames = sorted(os.listdir(d))
    times = pts_times(scene)
    arrs = [np.asarray(Image.open(os.path.join(d, f)).convert("RGB"), dtype=np.int16) for f in frames]
    diffs = [float(np.abs(arrs[i] - arrs[i - 1]).mean()) for i in range(1, len(frames))]

    print("场景=%s  原生帧数=%d  时长=%.2fs  帧间隔≈%.0fms"
          % (scene, len(frames), times[-1], (times[-1] - times[0]) / max(1, len(times) - 1) * 1000))
    runs = []
    for i, dv in enumerate(diffs, start=1):
        changed = dv > THRESHOLD
        if changed:
            if runs and times[i - 1] - runs[-1][1] < 0.15:
                runs[-1][1] = times[i]
                runs[-1][2] = max(runs[-1][2], dv)
            else:
                runs.append([times[i - 1], times[i], dv])
    print("画面在变的时间段（帧间 diff > %.2f）：" % THRESHOLD)
    for s, e, mx in runs:
        print("  %.3fs ~ %.3fs   峰值 diff %.2f" % (s, e, mx))
    if mode == "show":
        print(" idx   时间(s)  帧间隔(ms)  帧间 diff")
        for i, dv in enumerate(diffs, start=1):
            print("%4d %9.3f %10.0f %10.3f%s"
                  % (i, times[i], (times[i] - times[i - 1]) * 1000, dv,
                     "  <== 起点" if base_ms and times[i - 1] < base_ms / 1000.0 <= times[i] else ""))
        return

    base = base_ms / 1000.0
    picked = []
    for n, off in enumerate([0, 120, 400, 1000], start=1):
        target = base + off / 1000.0
        best = min(range(len(frames)), key=lambda i: abs(times[i] - target))
        dst = os.path.join(OUT, "motion-%s-%d.png" % (scene, n))
        shutil.copyfile(os.path.join(d, frames[best]), dst)
        picked.append(best)
        print("motion-%s-%d.png <- %s (t=%.3fs, 目标 +%dms, 实际 +%dms)"
              % (scene, n, frames[best], times[best], off,
                 round((times[best] - base) * 1000)))

    # 四张落盘帧的互相差异 + 墨迹像素数（暗像素 = 文字/图形），证明画面在变
    print("")
    print("落盘帧两两对比（平均像素差 0~255；墨迹 = 亮度<200 的像素数）：")
    for n in range(1, len(picked) + 1):
        a = arrs[picked[n - 1]]
        ink = int((a.mean(axis=2) < 200).sum())
        line = "  motion-%s-%d  t=%.3fs  墨迹=%6d" % (scene, n, times[picked[n - 1]], ink)
        if n > 1:
            b = arrs[picked[0]]
            line += "  与第 1 张差=%.3f" % float(np.abs(a - b).mean())
        print(line)


if __name__ == "__main__":
    main()
