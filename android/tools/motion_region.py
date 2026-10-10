"""动效取证 · 区域指标

用法：python tools/motion_region.py <scene> <x1,y1,x2,y2> [stride]

逐帧打印指定区域的：
  ink   = 亮度 < 200 的像素数（墨迹量，打字机揭示 / 入场淡入时递增）
  bbox  = 墨迹包围盒（宽x高；打字机揭示时宽度递增）
  mean  = 区域平均亮度（呼吸圆点 / 遮罩淡入时变化）
  peakx = 「列均亮度的相对基线偏差」最大的列（扫光带的位置，随扫光左→右移动）
"""
import os
import sys

import numpy as np
from PIL import Image

OUT = r"C:\Users\xzy\Desktop\my\lxcode\android"


def main():
    scene = sys.argv[1]
    x1, y1, x2, y2 = [int(v) for v in sys.argv[2].split(",")]
    stride = int(sys.argv[3]) if len(sys.argv) > 3 else 1

    d = os.path.join(OUT, "frames-" + scene)
    frames = sorted(os.listdir(d))
    regs = [np.asarray(Image.open(os.path.join(d, f)).convert("L"), dtype=np.float32)[y1:y2, x1:x2]
            for f in frames]
    prof = np.stack(regs)
    print("场景=%s 区域=(%d,%d)-(%d,%d) 帧数=%d" % (scene, x1, y1, x2, y2, len(frames)))
    print(" idx   时间(s)      ink   bbox宽  区域均值   变化最大列x  该列峰值  相对上一帧差")
    prev = None
    for i in range(0, len(frames), stride):
        a = regs[i]
        ink = int((a < 200).sum())
        ys, xs = np.where(a < 200)
        w = int(xs.max() - xs.min() + 1) if len(xs) else 0
        if prev is None:
            dv, peakx, peakval = 0.0, -1, 0.0
        else:
            col = np.abs(a - prev).mean(axis=0)
            dv = float(col.mean())
            peakx = int(np.argmax(col))
            peakval = float(col.max())
        prev = a
        print("%4d %9.2f %8d %7d %9.2f %12d %9.2f %10.3f"
              % (i + 1, (i + 1) / 30.0, ink, w, a.mean(), peakx, peakval, dv))


if __name__ == "__main__":
    main()
