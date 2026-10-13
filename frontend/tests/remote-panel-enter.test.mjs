// 远程访问页面板入场动画的纪律锁——「局域网直连」卡片闪烁的回归钉子。
// 根因（2026-12）：staggerIn 写在内联 ref 回调里，而 React 对 identity 变化的
// ref 回调每次渲染都会重调（旧 ref 传 null、新 ref 传 el）——父级（App 聊天流、
// 连接域推送）每重渲染一次，面板入场动画就重播一次，卡片反复淡入即「一直闪」。
// 纪律：入场动画只能由 effect 驱动（挂载时播一次）；ref 回调恒定且不触发动画。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, '../src/components/remote/LanAccessBlock.tsx'), 'utf8');

test('staggerIn 全文件只调用一次，且在 useEffect 体内（不在 ref 回调里）', () => {
  const calls = [...src.matchAll(/staggerIn\(/g)];
  assert.equal(calls.length, 1, 'staggerIn 应只有一处调用');
  const at = calls[0].index;
  const before = src.slice(0, at);
  const lastEffect = before.lastIndexOf('useEffect(');
  const lastRef = before.lastIndexOf('ref=');
  assert.ok(lastEffect !== -1, 'staggerIn 应由 useEffect 驱动');
  assert.ok(lastEffect > lastRef, 'staggerIn 不得落在 ref 回调里（每次渲染都会重播）');
});

test('面板 ref 是恒定回调（useCallback 空依赖），回调体不触发动画', () => {
  const m = src.match(/const setPanelRef = useCallback\(([\s\S]*?)\}, \[\]\);/);
  assert.ok(m, 'setPanelRef 必须是 useCallback 且依赖为空（identity 恒定，React 只在挂载/卸载时调用）');
  assert.ok(!/staggerIn\(/.test(m[1]), 'ref 回调里不得重播交错动画');
  assert.ok(/playEnter\(el\)/.test(m[1]), '挂载入场仍由恒定 ref 回调承担（与 useEnterRef 同语义）');
});

test('ref 挂载点引用恒定回调，不再用内联箭头', () => {
  assert.ok(/className="conn-ra-panel" ref=\{setPanelRef\}/.test(src), '面板 ref 应直接绑定 setPanelRef');
});
