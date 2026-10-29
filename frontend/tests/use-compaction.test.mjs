// use-compaction 的接线钉子（2026-12 用户报「点了没反应」的修复批）。
//
// hook 本体在 node 单测里没有 DOM 跑不了 async 交互（与本仓库其他 app-hooks 一致），
// 按 app-hooks-rewind.test.mjs 的惯例用**源码文本钉子**钉住接线；压缩事件的归约
// 语义（context 更新）在 store.test.mjs 钉、wire 形状在 ws.test.mjs 钉。
//
// 三条纪律：
// ① 进行中要有反馈：compacting state 存在且经 try/finally 复位——压缩同步等一次
//    摘要调用（10~30s），无反馈 = 用户以为按钮坏了；
// ② 成功后重拉历史落位：reloadHistory 必须在 compacted=true 时被调——
//    chat.compacted 事件只把检查点卡插在时间线**末尾**，落回语义位置（前缀处）
//    与清掉被压缩旧气泡只能靠重拉（历史应答的 checkpoints）；
// ③ 重入闸：compacting 不进 useCallback 依赖数组（否则入口身份每次翻转，
//    击穿 ContextIndicator 的 memo——2026-12 渲染性能 Top1）；
// ④ 「超时但成功」的尾巴（2026-12 用户报「压缩失败: 请求超时」）：超时只 reject
//    前端请求、不 abort 后端（context.Background() 无 deadline）——压缩超时路径
//    不得定死失败，保留 chat.compacted 监听 + 重入闸到兜底窗口，事件到达即认账
//    成功并重拉历史。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const hookSrc = readFileSync(join(here, '../src/app-hooks/use-compaction.ts'), 'utf8');
const indicatorSrc = readFileSync(join(here, '../src/components/context-indicator/ContextIndicator.tsx'), 'utf8');
const appSrc = readFileSync(join(here, '../src/App.tsx'), 'utf8');
const composerSrc = readFileSync(join(here, '../src/components/composer/Composer.tsx'), 'utf8');

test('① compacting state 存在且经 try/finally 复位（失败路径也摘除反馈）', () => {
  assert.match(hookSrc, /const \[compacting, setCompacting\] = useState\(false\)/);
  assert.match(hookSrc, /setCompacting\(true\)/);
  assert.match(hookSrc, /finally \{[\s\S]*?if \(!keepGate\) \{[\s\S]*?inFlight\.current = false;[\s\S]*?setCompacting\(false\)/);
});

test('② reloadHistory 在压缩成功时被调（compacted=false 不重拉）', () => {
  assert.match(hookSrc, /if \(!res\.compacted\) \{[\s\S]*?return;/, '没有收益时不重拉');
  assert.match(hookSrc, /if \(reloadHistory\) \{[\s\S]*?await reloadHistory\(currentId\)/);
});

test('③ 重入闸在 ref 上（compacting 不进依赖数组，保住 memo 身份）', () => {
  assert.match(hookSrc, /inFlight = useRef\(false\)/);
  assert.match(hookSrc, /if \(inFlight\.current\) return;/);
  // handleCompact 的依赖数组不含 compacting
  assert.match(hookSrc, /\[source, reportError, currentId, reloadHistory\]/);
});

test('③b 超时但成功的尾巴：超时路径保留事件监听与重入闸，chat.compacted 到达即认账成功', () => {
  // 超时判定（call 的计时器文案——请求确实发出去过；send 同步失败/未连接是别的文案）
  assert.match(hookSrc, /msg === "请求超时"/, '超时判定要带 unsubscribe（请求确实发出去过）');
  assert.match(hookSrc, /keepGate = true/);
  assert.match(hookSrc, /压缩仍在后台进行/);
  // 监听在请求之前注册、按会话 id 过滤
  assert.match(hookSrc, /source\.subscribe\(\(ev\) => \{[\s\S]*?ev\.type !== "compacted"[\s\S]*?ev\.sessionId !== currentId/);
  // 兜底 timer 到期：摘闸 + 诚实的后台进行提示
  assert.match(hookSrc, /setTimeout\(\(\) => \{[\s\S]*?inFlight\.current = false;[\s\S]*?reportError\(/);
  // 事件到达：摘闸 + 认账成功（重拉历史，与正常成功路径共用 finishSuccess）
  assert.match(hookSrc, /settled = true;[\s\S]*?cleanup\(\);[\s\S]*?inFlight\.current = false;[\s\S]*?void finishSuccess\(\)/);
  // 资源清理：组件卸载时撤监听与兜底 timer（防泄漏）
  assert.match(hookSrc, /useEffect\(\(\) => \(\) => \{ cleanupRef\.current\?\.\(\); \}, \[\]\)/);
});

test('④ 指示器按钮：压缩中禁用 + spinner + 文案变化', () => {
  assert.match(indicatorSrc, /disabled=\{busy \|\| compacting\}/);
  assert.match(indicatorSrc, /compacting \? "压缩中…" : "立即压缩历史"/);
  assert.match(indicatorSrc, /mset-spinner ctx-compact-spin/);
});

test('⑤ App：重拉口接进 hook，进行中状态条挂在 chat 视图（/compact 与按钮共用）', () => {
  assert.match(appSrc, /useCompaction\(\{ source, currentId, reportError, reloadHistory: reloadCurrentHistory \}\)/);
  assert.match(appSrc, /const compactingNotice = compacting && view === "chat"/);
  // Composer 收 compacting（ContextIndicator 的按钮态从这来）
  assert.match(composerSrc, /compacting=\{compacting\}/);
});
