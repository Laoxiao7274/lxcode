// 会话树面板的接线钉子（App.tsx / use-session-tree.ts / SessionTreePanel.tsx）。
//
// hook 与组件都是 React（node 单测里没有 DOM 跑不了交互），按本仓库惯例用**源码文本
// 钉子**钉住接线（同 app-hooks-rewind.test.mjs / project-entry.test.mjs 的做法）；
// 纯函数语义（flattenTree / 徽标 / 截断 / 确认文案）在 tree-model.test.mjs 与
// rewind-confirm.test.mjs 里钉。
//
// 这里钉五条**错了不会编译报错、只静默错**的纪律：
// ① 老后端没有 chat.tree → 回落 TurnPanel（不报错、不空屏）；
// ② 「回到最新」传树里最大的 seq，**绝不传 0**（0 = 空路径）；
// ③ files:true 走二次确认，冲突时弹清单（不静默覆盖）；
// ④ leafChanged 到达 → 重拉（leaf 是共享状态）；
// ⑤ 废弃分支单击 = 先切过去再跳（纯滚动跳转在别分支上是静默 no-op）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const read = (rel) => readFileSync(join(here, '..', rel), 'utf8');
const appSrc = read('src/App.tsx');
const hookSrc = read('src/app-hooks/use-session-tree.ts');
const panelSrc = read('src/components/panels/SessionTreePanel.tsx');
const outlineHookSrc = read('src/app-hooks/use-turn-outline.ts');

// ---------- ① 降级路径 ----------

test('① 老后端（tree === null）回落 TurnPanel：不报错、不空屏', () => {
  assert.match(appSrc, /sessionTree\.tree !== null && \(/, '有树才渲染 SessionTreePanel');
  assert.match(appSrc, /sessionTree\.tree === null && \(\s*<TurnPanel/, '没有树时回落 TurnPanel（线性轮次视图）');
  // 错误不吞：hook 把失败原因留在 error 里给面板显示（而不是静默 tree=null）
  assert.match(hookSrc, /setError\(e instanceof Error \? e\.message : String\(e\)\)/, '拉树失败要记下原因（面板显示，不静默）');
});

test('①b chat.tree 的失败不清空时间线：只把 tree 置 null 并回落', () => {
  // 失败路径里只动 tree/error（不碰 blocks / 历史）——树是面板的视图数据
  const catchBlock = hookSrc.slice(hookSrc.indexOf('} catch (e) {'), hookSrc.indexOf('} finally {'));
  assert.ok(!/loadHistory|historyLoaded/.test(catchBlock), '拉树失败不该去重放历史（那是 rewind/setLeaf 的对齐路径）');
});

// ---------- ② 「回到最新」 ----------

test('② 「回到最新」传树里最大的 seq，绝不传 0', () => {
  // App 侧：落点来自 latestSeq(tree)，且有 <= 0 的守卫（空树不动作）
  assert.match(appSrc, /const latestSeqInTree = useMemo\(\(\) => latestSeq\(sessionTree\.tree\)/, '落点 = 树里最大 seq');
  assert.match(appSrc, /if \(latestSeqInTree <= 0\) return;/, '空树不传 0 去 setLeaf（0 是空路径）');
  assert.match(appSrc, /sessionTree\.setLeaf\(latestSeqInTree\)/, '真正切到最新');
  // 面板侧：按钮禁用判据是「已经在最新处」（不是「树为空」）
  assert.match(panelSrc, /disabled=\{!canLatest \|\| busy \|\| loading\}/, '已在最新处时按钮禁用');
});

// ---------- ③ 二次确认与冲突清单 ----------

test('③ files:true 先二次确认，数字来自「这一行之后」的回滚量', () => {
  assert.match(appSrc, /if \(files\) \{/, '带文件走确认分支');
  assert.match(appSrc, /setRewindPlan\(\{/, '确认框状态被设置');
  assert.match(appSrc, /files: row\.rollbackFiles/, '数字用 rollbackFiles（不含本行——严格大于的切段口径）');
  assert.match(appSrc, /<RewindConfirmDialog/, '确认框真的被渲染');
  // 不带文件的「切到此」不弹确认（它是可逆的视觉动作）
  assert.match(appSrc, /void sessionTree\.setLeaf\(row\.seq\)\.catch/, '不带文件时直接切（无二次确认）');
});

test('③b 冲突时弹清单、不静默覆盖（fail-closed 的展示）', () => {
  assert.match(appSrc, /const conflicts = r\.files\?\.conflicts \?\? \[\];/, '从应答里取冲突清单');
  assert.match(appSrc, /if \(conflicts\.length > 0\) setRewindConflicts/, '非空就弹清单');
  assert.match(appSrc, /<RewindConflictDialog/, '冲突弹窗被渲染');
  assert.match(appSrc, /只切历史，不动文件|onHistoryOnly=/, '失败时给「只切历史」的退路（不是默认动作）');
});

test('③c 二次确认的 facts 在点击那一刻定格（树刷新不让数字跳）', () => {
  // setRewindPlan 里直接带 facts（而不是渲染时再算一次）
  assert.match(appSrc, /setRewindPlan\(\{\s*row,\s*facts: \{/, 'facts 与 row 一起定格');
  assert.match(appSrc, /facts=\{rewindPlan\.facts\}/, '渲染时读定格值（不重算）');
});

// ---------- ④ leafChanged 重拉 ----------

test('④ leafChanged 到达 → 重拉本会话的树（leaf 是共享状态）', () => {
  assert.match(hookSrc, /ev\.type === "leafChanged"/, '处理 leafChanged');
  assert.match(hookSrc, /ev\.sessionId === sessionRef\.current\) void fetchTree\(\)/, '只处理本会话的 leafChanged 并重拉树');
  // 切会话要清掉上一次的树与 loadedAll（否则新会话看到旧会话的分支）
  assert.match(hookSrc, /setLoadedAll\(false\);[\s\S]{0,200}setTree\(null\);/, '切会话清树与「加载全部」态');
});

test('④b setLeaf 之后重拉树（分支结构变了，而树不在 chat.history 里）', () => {
  assert.match(hookSrc, /await fetchTree\(\{ loadAll: loadedAll \}\)/, 'setLeaf 之后重拉树，并保持「加载全部」档位');
});

// ---------- ⑤ 跳转与切分支 ----------

test('⑤ 废弃分支单击 = 先 setLeaf 再跳（纯滚动跳转在别分支上是静默 no-op）', () => {
  assert.match(appSrc, /if \(row\.onPath\) \{/, '当前路径上直接跳');
  assert.match(appSrc, /pendingJumpSeqRef\.current = row\.userSeq;/, '别分支记下待跳 seq');
  assert.match(appSrc, /void sessionTree\.setLeaf\(row\.seq\)/, '先切过去');
  assert.match(appSrc, /const uid = seqToUid\.get\(seq\);/, '历史重载后兑现跳转（读最新映射，不靠定时器猜）');
});

test('⑤b 「从此分支」走 rewind（setLeaf 会让重发出现两条一模一样的用户消息）', () => {
  assert.match(appSrc, /const handleTreeBranchFrom = useCallback/, '有「从此分支」的接线');
  assert.match(appSrc, /handleRewind\(block\)/, '复用 handleRewind（rewind = setLeaf(parent)，含原文回填）');
  assert.match(panelSrc, /onBranchFrom\(row\)/, '面板调用它');
  assert.match(panelSrc, /row\.kind === "user" && row\.onPath/, '只对当前路径上的 user 行给（别分支上 rewind 是 no-op）');
});

test('⑤c 跳转锚点只来自 user 块（dispatch/assistant 块没有 seq）', () => {
  assert.match(outlineHookSrc, /userSeqUid\(blocks\)/, 'seqToUid 由 userSeqUid 建（只收 user 块）');
  assert.match(outlineHookSrc, /activeSeq/, '当前高亮行的 seq 由块反查（树面板按 seq 判高亮）');
});

// ---------- ⑥ 面板交互纪律 ----------

test('⑥ 否决双击：切分支是显式行尾按钮（可 Tab、可触摸）', () => {
  assert.ok(!/onDoubleClick/.test(panelSrc), '不用双击（不可发现、触摸端冲突、键盘不可达）');
  assert.match(panelSrc, /⟲ 切到此/, '显式按钮「切到此」');
  assert.match(panelSrc, /disabled=\{atLeaf \|\| busy\}/, '已在叶上/生成中时禁用（不做成点了没反应）');
});

test('⑥b 徽标 omitempty：0 不渲染（不显示「0 文件」）', () => {
  assert.match(panelSrc, /const files = filesBadge\(row\);/, '文件徽标走纯函数（0 → null）');
  assert.match(panelSrc, /\{files && <span className="stree-files"/, 'null 不渲染');
  assert.match(panelSrc, /\{external && \(/, '外部操作徽标同理');
  assert.match(panelSrc, /\{branch && <span className="stree-branch-badge"/, '分支徽标同理');
});

test('⑥c 过滤三档 + 截断提示 + 加载全部', () => {
  for (const label of ['全部', '只看用户消息', '只看有改动']) {
    assert.ok(panelSrc.includes(label), `过滤档「${label}」存在`);
  }
  assert.match(panelSrc, /truncation\.text/, '截断提示文案来自纯函数');
  assert.match(panelSrc, /onLoadAll/, '「加载全部」按钮');
  assert.match(appSrc, /sessionTree\.refresh\(\{ loadAll: true \}\)/, '加载全部真的重新拉树（max_nodes=0）');
});
