#!/usr/bin/env bash
# 截图助手：shot.sh <name>  —— 截图并拉到 android/ 目录
export PATH="$PATH:/c/Users/xzy/AppData/Local/Android/Sdk/platform-tools"
export MSYS_NO_PATHCONV=1
DEV=10.10.5.202:30900
OUT="C:/Users/xzy/Desktop/my/lxcode/android"
NAME="$1"
adb -s "$DEV" shell screencap -p "/sdcard/$NAME.png"
adb -s "$DEV" pull "/sdcard/$NAME.png" "$OUT/$NAME.png" >/dev/null 2>&1
adb -s "$DEV" shell rm "/sdcard/$NAME.png"
echo "saved $OUT/$NAME.png"
