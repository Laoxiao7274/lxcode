// 助手域连接恢复自愈（2026-12「每次点开都显示后端未连接」修复）的行为钉子：
//   ① isConnectionError 纯函数：三类连接串命中、超时/业务错误不命中；
//   ② AssistantWorkspace 订阅 connectionChanged 的接线（源码文本钉——React
//      effect 在 node 单测里跑不了，钉错了不会编译报错、只静默错，按仓库惯例）；
//   ③ AssistantPage boot 连接类错误自动重试接线（同②惯例 + 纯逻辑直测覆盖
//      非连接类不重试的判定）。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const { isConnectionError } = await import('../src/components/assistant/AssistantPage.tsx');

// ---------- ① isConnectionError ----------

test('① isConnectionError：connection.ts 三类连接串命中（拒绝源同源）', () => {
  assert.equal(isConnectionError('后端未连接'), true, 'call 的 WS 未 open 同步 reject');
  assert.equal(isConnectionError('后端连接已断开'), true, '断连 rejectAllPending');
  assert.equal(isConnectionError('切换后端连接'), true, 'setBackend rejectAllPending');
});

test('① isConnectionError：非连接类错误不命中（不自动重试）', () => {
  assert.equal(isConnectionError('请求超时'), false, '超时留给手动重试');
  assert.equal(isConnectionError('助手会话不存在'), false, '业务错误盲目重试是无限循环');
  assert.equal(isConnectionError(''), false, '空串不命中');
  assert.equal(isConnectionError('后端连接断开，正在重连'), false, 'opError 的展示文案不是拒绝串（boot error 收的是 call 的 reject 消息）');
});

// ---------- ② AssistantWorkspace：connectionChanged 重拉接线 ----------

test('② AssistantWorkspace 订阅 connectionChanged：恢复且只有挂错误时才重拉', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.ok(src.includes('ev.type !== "connectionChanged"'), '订阅 connectionChanged 事件');
  assert.match(src, /if \(!ev\.connected\) \{\s*\n\s*connectedRef\.current = false;/, '断线翻转 connectedRef（不发请求）');
  assert.match(src, /if \(connectedRef\.current\) return;/, '同一次连接去重（onopen 连发不重复拉）');
  assert.match(src, /if \(!listErrorRef\.current\) return;/, '只在挂着 listError 时重拉（正常态不与 sessionChanged 撞车）');
  assert.match(src, /void refreshList\(\)\.catch\(\(\) => undefined\);/, '恢复重拉失败静默（弹层已有错误，不重复弹）');
});

test('② 切入助手模式兜底：mode 变为 assistant 且有 listError 时 refreshList', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.match(src, /if \(mode !== "assistant"\) return;/, '只在助手模式可见时兜底');
  assert.match(src, /listErrorRef\.current\) return;\s*\n\s*void refreshList\(\)/, '兜底同样只在挂错误时发请求');
});

// ---------- ③ AssistantPage：boot 连接类错误自动重试 ----------

test('③ AssistantPage boot 自动重试：状态门 + 连接类错误门 + 同错误只重试一次', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  // 订阅连接态
  assert.match(src, /ev\.type === "connectionChanged"\) setConnected\(ev\.connected\)/, '订阅 connectionChanged 翻转 connected');
  // 重试三道门：error 态、connected 为真、isConnectionError
  assert.match(src, /if \(status !== "error" \|\| !connected\) return;/, '只在 error 态且已连接时重试');
  assert.match(src, /if \(!isConnectionError\(error\)\) return;/, '非连接类错误不自动重试');
  // 同错误只一次：retryNonceRef 对齐 nonce，不随连接事件反复 reset
  assert.match(src, /if \(retryNonceRef\.current !== nonce\) return;/, '同一错误不反复重试');
  assert.match(src, /retryNonceRef\.current = nonce \+ 1;/, '重试后推进 nonce 记账');
  // 重试走既有 reset + nonce 通路（与手动重试按钮同一条）
  assert.match(src, /bootRef\.current\?\.reset\(\);\s*\n\s*setNonce\(\(n\) => n \+ 1\);\s*\n\s*setStatus\("loading"\);/, '自动重试与手动重试同一条通路');
});
