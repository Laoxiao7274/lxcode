#!/usr/bin/env bash
# 截图助手：shot.sh <name>  —— 截图并拉到 android/ 目录
export PATH="$PATH:~/AppData/Local/Android/Sdk/platform-tools"
export MSYS_NO_PATHCONV=1
DEV=<adb设备地址>
OUT="~/Desktop/my/lxcode/android"
NAME="$1"
adb -s "$DEV" shell screencap -p "/sdcard/$NAME.png"
adb -s "$DEV" pull "/sdcard/$NAME.png" "$OUT/$NAME.png" >/dev/null 2>&1
adb -s "$DEV" shell rm "/sdcard/$NAME.png"
echo "saved $OUT/$NAME.png"
