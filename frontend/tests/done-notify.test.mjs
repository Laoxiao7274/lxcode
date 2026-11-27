// use-done-notify 的判定契约（对话完成系统通知，2026-12）。
//
// 判定核心 shouldNotify 是纯函数（焦点/开关由调用方传——node 单测不用 stub
// DOM），直接测它；hook 的接线（订阅 + 桥调用 + 标题解析）按本仓库惯例用
// 源码文本钉子（同 app-hooks-rewind.test.mjs 的做法——React hook 在 node
// 单测里没有 DOM 跑不了交互）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { shouldNotify, notifyDoneEnabled, setNotifyDoneEnabled, NOTIFY_DONE_KEY, NOTIFY_FALLBACK_TITLE_DONE } from '../src/app-hooks/use-done-notify.ts';

const here = dirname(fileURLToPath(import.meta.url));
const hookSrc = readFileSync(join(here, '../src/app-hooks/use-done-notify.ts'), 'utf8');
const appSrc = readFileSync(join(here, '../src/App.tsx'), 'utf8');
const eventsSrc = readFileSync(join(here, '../src/agent/ws/events.ts'), 'utf8');

const mainDone = { type: 'done', sessionId: 's1', usageTokens: 1, finishReason: 'stop', content: '改完了' };
const childDone = { type: 'done', sessionId: 's1', usageTokens: 1, finishReason: 'stop', dispatchId: 'd1' };

// ---------- shouldNotify 纯函数：触发规则四条 ----------

test('主轮 done + 失焦 + 开关开 → 弹', () => {
  assert.equal(shouldNotify(mainDone, false, true)?.kind, 'done');
});

test('焦点内不弹（正看着的会话完成是打扰）', () => {
  assert.equal(shouldNotify(mainDone, true, true), null);
});

test('子轮不弹（dispatchId 非空——dispatch 卡不是"你的对话完成了"）', () => {
  assert.equal(shouldNotify(childDone, false, true), null);
});

test('tool_calls 轮不弹（中间轮——主 Agent 发起派发的那轮没有 dispatchId，只滤 dispatchId 会误弹）', () => {
  const midTurn = { type: 'done', sessionId: 's1', usageTokens: 1, finishReason: 'tool_calls', content: '' };
  assert.equal(shouldNotify(midTurn, false, true), null);
});

test('白名单制：空串/unknown/aborted 都不弹（`?? "stop"` 兜不住空串——OpenAI 端点缺省 finish_reason 透传空串）', () => {
  for (const fr of ['', 'unknown', 'aborted', 'length']) {
    const ev = { type: 'done', sessionId: 's1', usageTokens: 1, finishReason: fr, content: '' };
    assert.equal(shouldNotify(ev, false, true), null, `finishReason=${JSON.stringify(fr)} 不应弹`);
  }
});

test('stop 轮照常弹（派发收尾后的最终轮——runTools 阻塞语义保证 stop 到达时派发已收尾）', () => {
  assert.equal(shouldNotify(mainDone, false, true)?.kind, 'done');
});

test('开关关不弹', () => {
  assert.equal(shouldNotify(mainDone, false, false), null);
});

// ---------- 失败通知（error 事件——A3 扩展） ----------

test('error + 失焦 + 开关开 → 弹 error 类通知', () => {
  const err = { type: 'error', sessionId: 's1', message: '生成失败: 连接中断', aborted: false };
  const hit = shouldNotify(err, false, true);
  assert.equal(hit?.kind, 'error');
});

test('error + aborted（用户主动取消）不弹——取消不是失败', () => {
  const err = { type: 'error', sessionId: 's1', message: '已取消', aborted: true };
  assert.equal(shouldNotify(err, false, true), null);
});

test('error + 子会话轮（dispatchId）不弹——错误在派发卡内呈现', () => {
  const err = { type: 'error', sessionId: 's1', message: '子任务失败', aborted: false, dispatchId: 'd1' };
  assert.equal(shouldNotify(err, false, true), null);
});

test('error 事件归一带 dispatchId（events.ts 源码钉子）', () => {
  assert.match(eventsSrc, /case "chat\.error":[\s\S]*?dispatchId,/, 'chat.error 归一必须带 dispatchId（通知滤子会话的依据）');
});

test('hook：error 走 bridge.error（与 done 同管道不同方法；老壳回落 done）', () => {
  assert.match(hookSrc, /bridge\.error\(\{/);
  // 回落实现在 host.ts 的桥构造处（老壳没有 error 通道时复用 done 通道）
  const hostSrc = readFileSync(join(here, '../src/agent/host.ts'), 'utf8');
  assert.match(hostSrc, /error: s\.error \? \(n\) => s\.error!\(n\) : \(n\) => s\.done!\(n\)/);
});

// ---------- 开关读写（localStorage 纪律） ----------

test('开关键名 lx.notify.done，默认开（无记录/读写失败都回落开）', () => {
  assert.equal(NOTIFY_DONE_KEY, 'lx.notify.done');
  // 读写失败静默（函数体里 try/catch 包住，不向上抛）
  assert.match(hookSrc, /try\s*\{\s*\n\s*return localStorage\.getItem\(NOTIFY_DONE_KEY\) !== "off";\s*\n\s*\} catch \{\s*\n\s*return true;/);
  assert.match(hookSrc, /localStorage\.setItem\(NOTIFY_DONE_KEY, on \? "on" : "off"\)/);
  // 两条读写都被 try/catch 包住（失败静默）
  assert.match(hookSrc, /\} catch \{ \/\* 隐私模式等——不持久化而已 \*\/ \}/);
});

test('开关写入 round-trip（off 不等于默认，读取才关闭）', () => {
  const store = new Map();
  const g = globalThis;
  g.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, v),
  };
  assert.equal(notifyDoneEnabled(), true, '无记录 = 默认开');
  setNotifyDoneEnabled(false);
  assert.equal(notifyDoneEnabled(), false, '写入 off 后读回关');
  setNotifyDoneEnabled(true);
  assert.equal(notifyDoneEnabled(), true, '写入 on 后读回开');
  delete g.localStorage;
});

// ---------- 兜底标题 ----------

test('会话标题查不到时回落「任务完成」', () => {
  assert.equal(NOTIFY_FALLBACK_TITLE_DONE, '任务完成');
  // 判定通过后 body 兜底「本轮已完成」（content 缺席时——工具轮/老后端）
  assert.match(hookSrc, /body: hit\.ev\.content \|\| "本轮已完成"/);
});

// ---------- hook 接线（源码钉子） ----------

test('hook：无桥静默跳过（浏览器/demo 模式挂载不炸）', () => {
  assert.match(hookSrc, /if \(!bridge\) return;/, '无桥必须在订阅前退出');
});

test('hook：焦点与开关在事件到达那一刻现读（不读挂载时刻的快照）', () => {
  assert.match(hookSrc, /shouldNotify\(ev, document\.hasFocus\(\), notifyDoneEnabled\(\)\)/);
});

test('hook：标题从 source.sessions() 查，归属按事件 sessionId', () => {
  assert.match(hookSrc, /source\.sessions\(\)\.find\(\(s\) => s\.id === hit\.ev\.sessionId\)/);
});

test('App.tsx 挂载 useDoneNotify', () => {
  assert.match(appSrc, /useDoneNotify\(\{ source \}\)/);
});

// ---------- events.ts 的 finish_reason 归一化（空串兜 stop，与后端归一对齐） ----------

test('events.ts：finish_reason 空串兜 stop（?? 不兜 ""，需显式判空串）', () => {
  assert.match(eventsSrc, /p\.finish_reason === "" \? "stop" : String\(p\.finish_reason \?\? "stop"\)/);
});

// ---------- events.ts 的 content 归一化（通知正文的来源） ----------

test('chat.done 归一化补 content（message.content 截 200 字符；缺席整键缺席）', () => {
  const long = 'x'.repeat(300);
  // 截断：通知是预览不是全文
  assert.match(eventsSrc, /slice\(0, 200\)/);
  // 缺席时键不写（键缺席纪律——deepStrictEqual 按键比对）
  assert.match(eventsSrc, /\.\.\.\(p\.message && typeof \(p\.message as \{ content\?: unknown \}\)\.content === "string"/);
});
