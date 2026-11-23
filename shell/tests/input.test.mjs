// input.test.mjs —— 投屏键名表的协议形状钉子（纯数据，无 electron 依赖）。
// 修饰键锁定链路（2026-12 修复）：安卓端 toggleLock 发 type=down/up +
// key=ctrl/alt/shift/win，壳侧走键盘 VK down/up（不再误当鼠标事件）——
// 键名必须在表内，后端白名单（internal/server/screen.go isScreenKeyName）
// 也按这份集合对齐；这里钉住壳侧这份。
import { test } from "node:test";
import assert from "node:assert/strict";
import { VK_MAP, VK_MOD } from "../src/screen-keys.ts";

test("VK_MAP 覆盖修饰键锁定链路会发送的键名", () => {
  for (const key of ["ctrl", "alt", "shift", "win"]) {
    assert.ok(VK_MAP[key] !== undefined, `VK_MAP 缺 ${key}——锁定链路会发这个键名`);
  }
});

test("VK_MAP 覆盖单发键行的键名", () => {
  for (const key of ["tab", "up", "down", "left", "right", "enter", "backspace", "esc"]) {
    assert.ok(VK_MAP[key] !== undefined, `VK_MAP 缺 ${key}`);
  }
});

test("VK_MOD 是 VK_MAP 修饰键的子集（modifiers 组合注入复用同一份）", () => {
  for (const [name, vk] of Object.entries(VK_MOD)) {
    assert.equal(VK_MAP[name], vk, `VK_MOD.${name} 与 VK_MAP 不一致`);
  }
});
