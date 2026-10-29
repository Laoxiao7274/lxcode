// memo 包裹（2026-12 渲染性能 Top1/Top3）的结构断言：
//
// memo 的收益依赖「组件真的被 memo 包住」——回退成普通函数不会有任何报错，
// 只会悄悄退回每帧全量重渲染。这里把六个组件钉成 React.memo 对象：
// $$typeof === REACT_MEMO_TYPE。props 稳定性由 App 的 useCallback / 签名 memo
// 保证（App 侧改动有自己的审查），这里只钉结构。
import test from 'node:test';
import assert from 'node:assert/strict';
import { Sidebar } from '../src/components/sidebar/Sidebar.tsx';
import { Topbar } from '../src/components/topbar/Topbar.tsx';
import { ContextIndicator } from '../src/components/context-indicator/ContextIndicator.tsx';
import { StatsPills } from '../src/components/stats-pills/StatsPills.tsx';
import { OutlinePanel } from '../src/components/panels/OutlinePanel.tsx';
import { ThinkingReasoning } from '../src/aicss/ThinkingReasoning.tsx';

const REACT_MEMO_TYPE = Symbol.for('react.memo');

test('流式热路径上的组件都包 memo（结构断言）', () => {
  for (const [name, comp] of [
    ['Sidebar', Sidebar],
    ['Topbar', Topbar],
    ['ContextIndicator', ContextIndicator],
    ['StatsPills', StatsPills],
    ['OutlinePanel', OutlinePanel],
    ['ThinkingReasoning', ThinkingReasoning],
  ]) {
    assert.equal(comp?.$$typeof, REACT_MEMO_TYPE, `${name} 必须是 memo 组件（退回普通函数 = 每帧全量重渲染）`);
    assert.equal(typeof comp.type, 'function', `${name} 的内层必须是函数组件`);
  }
});
