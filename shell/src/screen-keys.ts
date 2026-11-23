// screen-keys.ts —— 投屏键名 → Win32 VK 码表（**唯一事实源**）。
//
// 从 input.ts 抽出的原因：input.ts 依赖 electron（screen API 坐标换算），
// node:test 无法直接加载；键名表本身是纯数据，独立成无依赖模块后壳侧单测
// 与安卓/后端白名单可对齐到同一份。使用方：input.ts（注入执行）、
// tests/input.test.mjs（协议形状钉子）。
//
// 单字符键名（"a"-"z"/"0"-"9"）不在表内：注入时走 Unicode 注入，不查布局。

/** 命名键 → VK；winuser.h 常量。 */
export const VK_MAP: Record<string, number> = {
  backspace: 0x08, tab: 0x09, enter: 0x0d, return: 0x0d, esc: 0x1b, escape: 0x1b,
  space: 0x20, pgup: 0x21, pgdn: 0x22, end: 0x23, home: 0x24,
  left: 0x25, up: 0x26, right: 0x27, down: 0x28, insert: 0x2d, delete: 0x2e,
  win: 0x5b, lwin: 0x5b, rwin: 0x5c, ctrl: 0x11, control: 0x11, alt: 0x12, shift: 0x10,
  volumeup: 0xaf, volumedown: 0xae, volumemute: 0xad,
  medianext: 0xb0, mediaprev: 0xb1, mediaplay: 0xb3,
};
for (let i = 1; i <= 24; i++) VK_MAP[`f${i}`] = 0x6f + i; // F1=0x70

/** 修饰键名 → VK（key 事件的 modifiers 组合；锁定链路的 down/up 也用这份）。 */
export const VK_MOD: Record<string, number> = { ctrl: 0x11, control: 0x11, alt: 0x12, shift: 0x10, win: 0x5b };
