// 侧键接线钉子（2026-12 双重导航回归）：app-command 的探针注入必须是**存在性
// 分流**——探针存在才调用、不存在才回落 history。旧的 `?.() ?? history.back()`
// 写法在探针已处理（返回 undefined/true）时仍会触发 history.back——双重导航，
// 侧键把项目域历史栈耗尽后整个失灵（实测回归路径）。
//
// 为什么是源码文本断言而不是运行时单测：app-command 接线与 electron
// BrowserWindow 强耦合，node --test 环境没有 electron 运行时；断言对象是
// 「注入串的存在性分流」这条接线纪律——同款做法见 push-guard.test.mjs 头注。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const read = (p) => readFileSync(fileURLToPath(new URL(p, import.meta.url)), "utf8");

test("侧键注入：存在性分流（探针存在调用、不存在回落 history）", () => {
  const src = read("../src/main.ts");
  const m = src.match(/app-command[\s\S]*?\n    \}\);/);
  assert.ok(m, "main.ts 应有 app-command 侧键接线");
  assert.ok(
    m[0].includes('typeof window.${probe} === "function"'),
    "注入串必须按 typeof 分流（探针存在才调用）",
  );
  assert.ok(m[0].includes(": history."), "探针缺席回落 history（分流右支）");
  // 旧写法禁止回潮：代码里的 `?.() ??`（注释里的反例文案除外）
  const code = m[0].replace(/\/\/.*$/gm, "");
  assert.ok(!code.includes("?.() ??"), "禁用 `?.() ??` 可选链写法（探针返回 undefined 会双重导航）");
});
