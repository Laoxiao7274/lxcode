// use-rewind 的会话归属契约（问题 1 跨会话泄漏 + 问题 2 回填延迟的接线钉子）。
//
// hook 本体（React state + ref）在 node 单测里没有 DOM 跑不了交互，按本仓库
// 惯例用**源码文本钉子**钉住接线（同 project-entry.test.mjs / child-tab-view
// 的做法）；纯函数语义（rewindThenRestore / session-drafts）在
// rewind-drafts.test.mjs 里钉。这里钉三条纪律：
//
// ① 归属显式传递：injectDraft 的第一个参是归属会话 sid——「当前会话」在异步
//    回调里不可信，撤回 A 的回填/回滚必须落 A（用户实测：撤回 A 的消息，
//    文本出现在 B 的输入框/时间线里）。
// ② 乐观回填时序：rewindThenRestore 的 inject 在 rewind() 调用**之前**发生
//    （点击那一帧输入框就有内容）——回填等后端应答就是问题 2 的 1~2s 空窗。
// ③ 失败回滚：rewind 被拒时 rollback(sid) 必须被调（把注入的文本撤出输入框），
//    时间线由 ws 的重放历史复原——两条路径一起回到撤回前。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const hookSrc = readFileSync(join(here, '../src/app-hooks/use-rewind.ts'), 'utf8');
const blocksSrc = readFileSync(join(here, '../src/shared/blocks.ts'), 'utf8');
const composerSrc = readFileSync(join(here, '../src/components/composer/Composer.tsx'), 'utf8');

// ---------- ① 归属显式传递（跨会话隔离的接线） ----------

test('①a injectDraft 的第一个参是归属会话 sid（不再读回调时刻的「当前会话」）', () => {
  assert.match(
    hookSrc,
    /const injectDraft = useCallback\(\(sid: string, text: string, atts\?: SendAttachments\)/,
    'injectDraft 签名必须显式收 sid',
  );
  // 记账键恒落 sid（不落 currentIdRef.current）
  assert.match(hookSrc, /writeDraftAtts\(writeDraftText\(cur, sid, text\), sid, atts\)/);
});

test('①b 挂载 Composer 的注入只在 sid 正是当前会话时发生（切走不串屏）', () => {
  assert.match(
    hookSrc,
    /if \(sid === currentIdRef\.current\) \{\s*\n\s*draftIdRef\.current \+= 1;\s*\n\s*setDraft\(\{ id: draftIdRef\.current, text,/,
    '注入挂载中的 Composer 前必须校验 sid === currentIdRef.current',
  );
});

test('①c 撤回/回填的归属 sid 在点击那一刻捕获（handleRewind/handleSend 的闭包）', () => {
  assert.match(hookSrc, /const sid = currentId;/, '撤回与编辑重发都在点击那一刻捕获归属会话');
  assert.match(hookSrc, /rewind: \(\) => source\.rewind\(sid, block\.seq\)/, 'rewind 请求发往捕获的那个会话');
  assert.match(hookSrc, /sid,\s*\n\s*inject: injectDraft,\s*\n\s*rollback: rollbackDraft,/);
});

// ---------- ② 乐观回填时序（点击 → 下一帧输入框有内容） ----------

test('② rewindThenRestore：inject 在 rewind() 之前发生（乐观回填），失败走 rollback', () => {
  // 提取 rewindThenRestore 函数体检查顺序
  const fn = blocksSrc.slice(blocksSrc.indexOf('export function rewindThenRestore'));
  const injectPos = fn.indexOf('opts.inject(opts.sid, opts.text);');
  const rewindPos = fn.indexOf('void opts.rewind()');
  assert.ok(injectPos >= 0, '乐观注入必须存在');
  assert.ok(rewindPos > injectPos, '注入必须先于 rewind 请求（点击即回填，不等后端应答）');
  // 失败路径：rollback(sid) 在报错之前（回滚先落，错误提示随后——两步都要发生）
  const catchBody = fn.slice(fn.indexOf('(e: unknown) =>'));
  assert.match(catchBody, /opts\.rollback\(opts\.sid\)/, '失败必须回滚注入的草稿');
  assert.match(catchBody, /opts\.report\(`撤回失败/);
});

// ---------- ③ 失败回滚把输入框清干净（回滚路径的完整语义） ----------

test('③ rollbackDraft：清 sid 的记账键文本；当前会话再清挂载 Composer 的注入态', () => {
  assert.match(hookSrc, /const rollbackDraft = useCallback\(\(sid: string\)/, '回滚按 sid 落');
  assert.match(hookSrc, /setDrafts\(\(cur\) => writeDraftText\(cur, sid, ""\)\)/, '记账键文本清零（附件暂存保留）');
  assert.match(hookSrc, /setDraft\(\{ id: draftIdRef\.current, text: "" \}\)/, '当前会话用新 id 注入空文本（Composer 只在 id 变化时应用）');
});

// ---------- ④ Composer 的注入消费契约（回滚能清掉输入框的前提） ----------

test('④ Composer 按 draft.id 判变化（同 id no-op、新 id 应用）——回滚的空文本注入因此生效', () => {
  assert.match(
    composerSrc,
    /if \(!draft \|\| draft\.id === draftIdRef\.current\) return;/,
    'Composer 必须按 id 判注入变化，否则回滚注入的空文本会被文本比较吃掉',
  );
});
