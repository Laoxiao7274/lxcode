// hooks 顺序钉子（React #300 防再犯）。
//
// 事故：ToolBlock 的 todoItems useMemo 曾排在两个早退（share_file 带附件 /
// edit 参数齐）之后——同一 fiber 的 hook 数在 13↔14 间漂移，触发 React #300
// 渲染错乱（ErrorBoundary 接住降级，0.3.33 就埋的老雷）。
//
// hook 本体（React 渲染）在 node 单测里没有 DOM 跑不了，按本仓库惯例用**源码
// 文本钉子**（同 app-hooks-rewind.test.mjs 的做法）；lint 侧已有
// react-hooks/rules-of-hooks 全仓兜底（eslint.config.mjs），这里是双保险的
// 显式断言：ToolBlock.tsx 组件体里，第一条组件级早退 return 必须出现在
// 最后一个 hook 调用之后。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, '../src/components/thread/blocks/ToolBlock.tsx'), 'utf8');

const lines = src.split('\n');
const hookRe = /\buse[A-Z][A-Za-z]*\s*\(/;

// 组件体的顶层行 = `useXxx(` 或 `const ... = useXxx(` 开头（本文件 hook 全部
// 这样写，没有嵌套 if 里的 hook——那正是 rules-of-hooks 禁的）。
const hookLines = lines
  .map((l, i) => ({ line: l.replace(/\/\/.*$/, ''), n: i + 1 }))
  .filter(({ line }) => hookRe.test(line));

// 组件级早退 return：`  if (...) {` 形式的条件块内、缩进恰为 2 的 `    return`，
// 以及 2 缩进的裸 `  return (`/`  return`。JSX 内层 return 缩进更深，不匹配。
// `return [` / `return {` 等（useMemo 体内的值 return）缩进在 4+，不匹配。
const earlyReturnRe = /^ {2}(if \(.*\) \{|return[\s({[].*)$/;

const firstEarly = lines.findIndex((l, i) =>
  i > 0 && earlyReturnRe.test(l.replace(/\/\/.*$/, '')) && hookRe.test(l) === false && /^\s*(if|return)/.test(l),
);
// 第一条"if (...) {"（组件级早退分支的开头）与其后的组件级 return。
// 锚点从组件体（export function ToolBlock）开始——模块级 helper（toolArgs 的
// 缓存取数 if）体内的 2 缩进 if 不是组件级早退，全文件扫描会误锚。
const bodyStart = lines.findIndex((l) => l.startsWith("export function ToolBlock"));
const firstIf = bodyStart < 0
  ? -1
  : bodyStart + lines.slice(bodyStart).findIndex((l) => /^ {2}if \(.*\) \{$/.test(l.replace(/\/\/.*$/, "")));
assert.ok(firstIf > bodyStart && bodyStart >= 0, 'ToolBlock 应存在组件级早退分支（share_file / edit / todo）');

// 最后一个 hook 调用行。
const lastHook = hookLines.reduce((acc, { n, line }) => {
  // 只算组件体里的 hook（useMemo( 等直接调用行——useMemo 内部 return 不含 useXxx）
  return Math.max(acc, n);
}, 0);
assert.ok(lastHook > 0, 'ToolBlock 应存在 hook 调用');

test('ToolBlock：最后一个 hook 在第一条组件级早退之前（React #300 钉子）', () => {
  assert.ok(
    lastHook < firstIf,
    `hook 调用最晚在第 ${lastHook} 行，组件级早退分支从第 ${firstIf + 1} 行开始——hook 必须全部前置`,
  );
});

test('ToolBlock：todoItems useMemo 存在且在早退之前（上移后的锚点行）', () => {
  const todoMemo = lines.findIndex((l) => l.includes('const todoItems = useMemo('));
  assert.ok(todoMemo > 0, 'todoItems useMemo 应存在');
  assert.ok(todoMemo < firstIf, `todoItems useMemo（第 ${todoMemo + 1} 行）必须在组件级早退（第 ${firstIf + 1} 行）之前`);
  // 上移后原位置不应再有重复定义。
  assert.equal(lines.filter((l) => l.includes('const todoItems = useMemo(')).length, 1, 'todoItems useMemo 只能定义一次');
});

test('ToolBlock：share_file 带附件 / edit 参数齐两个早退分支都还在（修复没顺手删功能）', () => {
  assert.ok(src.includes('block.name === "share_file" && block.attachments?.length'), 'share_file 早退仍在');
  assert.ok(src.includes('if (editDiff && block.name === "edit")'), 'edit 早退仍在');
});
