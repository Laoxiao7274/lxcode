#!/usr/bin/env bash
# 动效取证 · 连拍（adb exec-out screencap 快速循环）
#
# 用法：motion_burst.sh <scene> <trigger-command> [触发后等待毫秒]
#   触发动作后（默认立刻，可给等待毫秒）连拍 4 张，落盘 motion-<scene>-1..4.png，
#   并打印每张相对「触发命令返回」的时刻（ms）。
#   这台云机上单次 screencap 约 360ms，所以 4 张覆盖约 1.5s。
set -u
export PATH="$PATH:/c/Users/xzy/AppData/Local/Android/Sdk/platform-tools"
export MSYS_NO_PATHCONV=1
DEV=10.10.5.202:30900
OUT="C:/Users/xzy/Desktop/my/lxcode/android"
SCENE="$1"; TRIGGER="$2"; WAIT_MS="${3:-0}"

eval "$TRIGGER" >/dev/null 2>&1
START=$(date +%s%3N)
if [ "$WAIT_MS" != "0" ]; then sleep "$(echo "scale=3; $WAIT_MS/1000" | bc)"; fi
for n in 1 2 3 4; do
  adb -s "$DEV" exec-out screencap -p > "$OUT/motion-$SCENE-$n.png"
  NOW=$(date +%s%3N)
  echo "motion-$SCENE-$n.png  触发后 +$((NOW - START))ms"
done
