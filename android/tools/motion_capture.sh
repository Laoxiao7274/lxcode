#!/usr/bin/env bash
# 动效取证 · 录屏 + 抽帧
#
# 用法：motion_capture.sh <scene> <trigger-command-string>
#   <trigger-cmd> 是「触发动作」的命令（如 adb ... shell input tap x y 或 sh -c "..."），
#   它在录屏开始 1.5s 后执行；录屏再跑 2.0s 后停止。
#
# 产物：android/rec-<scene>.mp4 + android/frames-<scene>/f-<pts>.png（原生帧，文件名带 PTS）
set -u
export PATH="$PATH:/c/Users/xzy/AppData/Local/Android/Sdk/platform-tools"
export MSYS_NO_PATHCONV=1
DEV=10.10.5.202:30900
OUT="C:/Users/xzy/Desktop/my/lxcode/android"
SCENE="$1"; TRIGGER="$2"

adb -s "$DEV" shell rm -f "/sdcard/$SCENE.mp4" >/dev/null 2>&1
adb -s "$DEV" shell screenrecord --size 720x1280 --bit-rate 12M --time-limit 25 "/sdcard/$SCENE.mp4" >/dev/null 2>&1 &
REC=$!
sleep 1.5
eval "$TRIGGER" >/dev/null 2>&1
sleep 3.0
adb -s "$DEV" shell pkill -INT screenrecord >/dev/null 2>&1
wait "$REC" 2>/dev/null
adb -s "$DEV" pull "/sdcard/$SCENE.mp4" "$OUT/rec-$SCENE.mp4" 2>&1 | tail -1

rm -rf "$OUT/frames-$SCENE"
mkdir -p "$OUT/frames-$SCENE"
ffmpeg -loglevel error -i "$OUT/rec-$SCENE.mp4" -vf fps=30 "$OUT/frames-$SCENE/f-%06d.png"
echo "frames: $(ls "$OUT/frames-$SCENE" | wc -l)"
