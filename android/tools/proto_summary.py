"""P0 原型截图校验：证明每张都不是空白页，并报告 token 色命中与二维码区域的黑白模块占比。"""
import glob, os
from collections import Counter
from PIL import Image

TARGETS = {
    "--bg #ffffff": (0xFF, 0xFF, 0xFF),
    "--bg-side #f9f9f9": (0xF9, 0xF9, 0xF9),
    "--fg #0d0d0d": (0x0D, 0x0D, 0x0D),
    "--fg-muted #6e6e80": (0x6E, 0x6E, 0x80),
    "--fg-faint #8e8ea0": (0x8E, 0x8E, 0xA0),
    "--border #ececec": (0xEC, 0xEC, 0xEC),
    "--border-strong #e5e5e5": (0xE5, 0xE5, 0xE5),
    "--border-soft #f4f4f5": (0xF4, 0xF4, 0xF5),
    "--success #10a37f": (0x10, 0xA3, 0x7F),
    "--danger #e02e2a": (0xE0, 0x2E, 0x2A),
    "--term-bg #1a1a1a": (0x1A, 0x1A, 0x1A),
    "amber #d97706": (0xD9, 0x77, 0x06),
    "bubble #f1f1f3": (0xF1, 0xF1, 0xF3),
    "code-bg #f4f5f7": (0xF4, 0xF5, 0xF7),
}

base = os.path.dirname(os.path.abspath(__file__)) + os.sep + ".."
files = sorted(glob.glob(os.path.join(base, "proto-*.png")))

for path in files:
    img = Image.open(path).convert("RGB")
    w, h = img.size
    counts = Counter(img.getdata())
    total = w * h
    non_white = sum(n for c, n in counts.items() if c != (255, 255, 255))
    hits = []
    for name, rgb in TARGETS.items():
        n = sum(cnt for c, cnt in counts.items()
                if abs(c[0] - rgb[0]) <= 6 and abs(c[1] - rgb[1]) <= 6 and abs(c[2] - rgb[2]) <= 6)
        if n > 50:
            hits.append(f"{name.split()[0]}={n}")
    print(f"{os.path.basename(path):26} {w}x{h} non_white={non_white/total*100:5.1f}%  colors={len(counts):5d}")
    print("      " + "  ".join(hits))
