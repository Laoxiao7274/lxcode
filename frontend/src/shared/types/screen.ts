// 投屏域（对齐 internal/protocol/screen.go）。
//
// 桌面前端**没有 screen UI**（投屏的消费端是安卓 App；桌面壳的 ffmpeg 管理在
// shell/src/screen.ts，不走这层类型）——这个文件存在的原因是协议契约护栏：
// golden.json 的 screen 域样本（_type 指到这里）经
// frontend/tests/protocol-golden.test.mjs 做键声明校验，后端给 screen 域加
// 字段时改 protocol/screen.go + 这里的镜像（前端测试强制两侧同步），漏改在
// npm test 爆掉而不是等跨端联调时炸。

/** 单个显示器（screen.monitors 的 monitors[] 项；对齐 protocol.ScreenMonitor）。 */
export interface ScreenMonitor {
  id: number;
  name: string;
  primary: boolean;
  width: number;
  height: number;
  attached: boolean;
}

/** 显示器枚举（screen.monitors 应答；对齐 protocol.ScreenMonitorsResult）。 */
export interface ScreenMonitorsResult {
  monitors: ScreenMonitor[];
  shell_online: boolean;
}

/** 投屏状态（screen.start/screen.state 应答与 screen.changed 事件载荷；
 *  对齐 protocol.ScreenState）。除 active 外全部 omitempty——缺席 = 未知/未设，
 *  不发 0 冒充。 */
export interface ScreenState {
  active: boolean;
  monitor?: number;
  quality?: string;
  /** 实际执行帧率（壳上报；0/缺席 = 老壳或未知——按 30 对待）。 */
  fps?: number;
  encoder?: string;
  width?: number;
  height?: number;
  /** 壳侧兼容模式捕获中（主捕获路径失败后的回落；缺席 = 老壳未上报）。 */
  fallback_active?: boolean;
  /** 最近一次失败的人话原因（active=false 时可能非空）。 */
  error?: string;
  receiver?: string;
}

/** 一次输入事件（screen.input 参数；对齐 protocol.ScreenInputParams——
 *  触屏/键鼠语义并集，按 type 取用相应字段）。 */
export interface ScreenInputParams {
  type: string;
  x?: number;
  y?: number;
  button?: string;
  dx?: number;
  dy?: number;
  key?: string;
  modifiers?: string[];
  text?: string;
  monitor?: number;
}
