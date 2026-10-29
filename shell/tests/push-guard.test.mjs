// push-guard.test.mjs —— 「退出路径碰已销毁窗口」回归的源码级守卫断言。
//
// 背景（0.3.3 实测）：will-quit 里 shutdownSakura → stopFrpc → setState →
// notify → `win?.webContents.send(...)`，窗口已销毁时 send 抛
// 「Object has been destroyed」变成未捕获异常弹窗。
//
// 为什么是源码文本断言而不是运行时单测：推送出口（main.ts pushToRenderer）
// 与 electron BrowserWindow 强耦合，node --test 环境没有 electron 运行时；
// 且本断言的对象就是「调用点必须走守卫出口」这条**接线纪律**——同款做法
// 见 ts-loader.mjs 头注（接线断言在源码文本上做）。真销毁路径的行为由
// pushToRenderer 实现里的三重守卫保证，tsc --noEmit 保证签名不漂。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const read = (p) => readFileSync(fileURLToPath(new URL(p, import.meta.url)), "utf8");

test("pushToRenderer：三重存活守卫（null / 窗口销毁 / webContents 销毁）", () => {
  const src = read("../src/main.ts");
  const m = src.match(/function pushToRenderer\([\s\S]*?\n\}/);
  assert.ok(m, "main.ts 应有 pushToRenderer 统一推送出口");
  assert.ok(m[0].includes("!w ||"), "守卫应含 win 判空");
  assert.ok(m[0].includes("w.isDestroyed()"), "守卫应含窗口销毁检查");
  assert.ok(m[0].includes("w.webContents.isDestroyed()"), "守卫应含 webContents 销毁检查");
});

test("main.ts 不再直接对 win.webContents.send（推送必须走 pushToRenderer）", () => {
  const src = read("../src/main.ts");
  assert.ok(!/win\??\.webContents\.send\(/.test(src),
    "main.ts 里出现裸 webContents.send——退出路径会抛 Object has been destroyed，改走 pushToRenderer");
});

test("updater.ts 的窗口广播循环必须带销毁守卫", () => {
  const src = read("../src/updater.ts");
  // broadcast 与 onAvailable 两处 BrowserWindow.getAllWindows() 循环
  const loops = src.match(/for \(const win of BrowserWindow\.getAllWindows\(\)\)[\s\S]*?\n  \}/g) ?? [];
  assert.equal(loops.length, 2, "updater.ts 应恰有两处窗口广播循环");
  for (const loop of loops) {
    assert.ok(loop.includes("isDestroyed()"), "每处广播循环都必须先查窗口/webContents 是否已销毁");
  }
});
