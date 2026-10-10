#!/usr/bin/env bash
# 动效取证 · 场景驱动（把页面准备好 + 录屏触发 + 抽帧）
#
# 用法：motion_run.sh <scene>
#   sessions / conn / thread / confirm / pairing / loops    动效开启
#   off-*                                                 原型「动效」开关关掉（静态对照）
#   sys-off-sessions                                      系统 animator_duration_scale=0 的静态对照
#
# 场景定义里 PREP = 录屏前把页面准备到「动作发生前」的状态；
# TRIG = 录屏中执行的「触发动作」（空串 = 不触发，录连续循环动画）。
set -u
DIR="$(cd "$(dirname "$0")" && pwd -W)"
export PATH="$PATH:/c/Users/xzy/AppData/Local/Android/Sdk/platform-tools"
export MSYS_NO_PATHCONV=1
DEV=10.10.5.202:30900
SCENE="$1"

RESET() {  # 重启应用 = 回到「会话页 + 动效开关默认开」的已知状态（内存态清零）
  adb -s "$DEV" shell am force-stop com.moyunteng.lxcode.remote >/dev/null 2>&1
  adb -s "$DEV" shell am start -n com.moyunteng.lxcode.remote/.MainActivity >/dev/null 2>&1
  sleep 3.5
}
T() { adb -s "$DEV" shell "input tap $1 $2" >/dev/null 2>&1; sleep 0.9; }
SWIPE() { adb -s "$DEV" shell "input swipe $1 $2 $3 $4 400" >/dev/null 2>&1; sleep 1.2; }
GATE_OFF() { adb -s "$DEV" shell "input tap 580 92" >/dev/null 2>&1; sleep 0.8; }

NAV_SESSIONS=90
NAV_CONN=270
NAV_PAIR=450
SESSION_ROW_X=216
SESSION_ROW_Y=1022
NAV_Y=1253
CONFIRM_X=676
CONFIRM_Y=92
BACK_X=40
BACK_Y=92

prep_base() { T $BACK_X $BACK_Y; T $NAV_SESSIONS $NAV_Y; }   # 回会话列表（线程页/连接页的返回箭头都在 40,92）
prep_conn() { prep_base; T $NAV_CONN $NAV_Y; }               # 停到连接页
prep_thread() { prep_base; T $SESSION_ROW_X $SESSION_ROW_Y; }  # 停到线程页（顶部）
prep_confirm() { prep_thread; }                                 # 线程页 + 触发确认门
prep_loops() { prep_thread; SWIPE 360 750 360 250; }          # 线程页滚到「执行中…」工具行
prep_off_conn() { prep_base; T $NAV_CONN $NAV_Y; GATE_OFF; }  # 连接页 + 关「动效」
prep_off_base() { prep_off_conn; T $NAV_SESSIONS $NAV_Y; }  # 会话页 + 关「动效」
prep_off_thread() { prep_off_base; T $SESSION_ROW_X $SESSION_ROW_Y; }                  # 线程页 + 关「动效」
prep_off_loops() { prep_off_thread; SWIPE 360 750 360 250; }                             # 滚到「执行中…」工具行

case "$SCENE" in
  sessions)   PREP=prep_conn;     TRIG="adb -s $DEV shell 'input tap $NAV_SESSIONS $NAV_Y'" ;;
  conn)       PREP=prep_base;   TRIG="adb -s $DEV shell 'input tap $NAV_CONN $NAV_Y'" ;;
  thread)     PREP=prep_base;     TRIG="adb -s $DEV shell 'input tap $SESSION_ROW_X $SESSION_ROW_Y'" ;;
  confirm)    PREP=prep_confirm;  TRIG="adb -s $DEV shell 'input tap $CONFIRM_X $CONFIRM_Y'" ;;
  offline)    PREP=prep_conn; TRIG="adb -s $DEV shell 'input tap 485 92'" ;;
  pairing)    PREP=prep_base;   TRIG="adb -s $DEV shell 'input tap $NAV_PAIR $NAV_Y'" ;;
  loops)      PREP=prep_loops;    TRIG="sleep 0.2" ;;
  off-sessions) PREP=prep_off_conn; TRIG="adb -s $DEV shell 'input tap $NAV_SESSIONS $NAV_Y'" ;;
  off-thread)   PREP=prep_off_base; TRIG="adb -s $DEV shell 'input tap $SESSION_ROW_X $SESSION_ROW_Y'" ;;
  off-confirm)  PREP=prep_off_thread; TRIG="adb -s $DEV shell 'input tap $CONFIRM_X $CONFIRM_Y'" ;;
  off-loops)    PREP=prep_off_loops; TRIG="sleep 0.2" ;;
  sys-off-sessions) PREP=prep_conn; TRIG="adb -s $DEV shell 'input tap $NAV_SESSIONS $NAV_Y'" ;;
  *) echo "未知场景: $SCENE"; exit 2 ;;
esac

RESET
$PREP
sleep 1.0
bash "$DIR/motion_capture.sh" "$SCENE" "$TRIG"
python "$DIR/motion_pick.py" "$SCENE" 2>&1 | head -4
