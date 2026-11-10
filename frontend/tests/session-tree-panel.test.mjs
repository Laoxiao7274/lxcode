// 会话树面板的接线钉子（App.tsx / use-session-tree.ts / SessionTreePanel.tsx）——
// user-only 纯导航版。
//
// hook 与组件都是 React（node 单测里没有 DOM 跑不了交互），按本仓库惯例用**源码文本
// 钉子**钉住接线（同 app-hooks-rewind.test.mjs / project-entry.test.mjs 的做法）；
// 纯函数语义（flattenTree / 徽标 / 截断）在 tree-model.test.mjs 里钉。
//
// 这里钉五条**错了不会编译报错、只静默错**的纪律：
// ① 老后端没有 chat.tree → 回落 TurnPanel（不报错、不空屏）；
// ② leafChanged 到达 → 重拉（leaf 是共享状态）；
// ③ off_path 单击 = 先切过去再跳（纯滚动跳转在别分支上是静默 no-op）；
// ④ 面板是纯导航：没有过滤 chips、没有行尾按钮、没有「回到最新」（删了就是删了）；
// ⑤ 每会话树缓存：失效事件清条目并重拉、connectionChanged true 升代数。
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

// ---------- ② leafChanged 重拉 ----------

test('② leafChanged 到达 → 重拉本会话的树（leaf 是共享状态）', () => {
  assert.match(hookSrc, /ev\.type === "leafChanged"/, '处理 leafChanged');
  assert.match(hookSrc, /ev\.sessionId === sessionRef\.current\) void fetchTree\(\)/, '只处理本会话的 leafChanged 并重拉树');
  // 切会话未命中缓存（新会话/被失效事件清掉）要清掉上一次的树与 loadedAll
  //（否则新会话看到旧会话的分支）——命中缓存的那条分支不 setTree(null)。
  assert.match(hookSrc, /setLoadedAll\(false\);[\s\S]{0,200}setTree\(null\);/, '切会话清「加载全部」态与未命中时的树');
});

test('②b setLeaf 之后重拉树（分支结构变了，而树不在 chat.history 里）', () => {
  assert.match(hookSrc, /await fetchTree\(\{ loadAll: loadedAll \}\)/, 'setLeaf 之后重拉树，并保持「加载全部」档位');
});

// ---------- ③ off_path 单击 = 先切过去再跳 ----------

test('③ 另一条分支单击 = 先 setLeaf 再跳（纯滚动跳转在别分支上是静默 no-op）', () => {
  assert.match(appSrc, /if \(row\.onPath\) \{/, '当前路径上直接跳');
  assert.match(appSrc, /pendingJumpSeqRef\.current = row\.userSeq;/, '别分支记下待跳 seq');
  assert.match(appSrc, /void sessionTree\.setLeaf\(row\.seq\)/, '先切过去');
  assert.match(appSrc, /const uid = seqToUid\.get\(seq\);/, '历史重载后兑现跳转（读最新映射，不靠定时器猜）');
});

test('③b 跳转锚点只来自 user 块（dispatch/assistant 块没有 seq）', () => {
  assert.match(outlineHookSrc, /userSeqUid\(blocks\)/, 'seqToUid 由 userSeqUid 建（只收 user 块）');
  assert.match(outlineHookSrc, /activeSeq/, '当前高亮行的 seq 由块反查（树面板按 seq 判高亮）');
});

// ---------- ④ 面板 = 纯导航 ----------

test('④ 面板上没有过滤 chips、没有行尾按钮、没有「回到最新」（删了就是删了）', () => {
  for (const gone of ['stree-chips', 'stree-chip', 'stree-latest', 'stree-actions', 'stree-action',
    '切到此', '回到最新', '从此分支', '只看用户消息', '只看有改动', 'onFilter', 'onSetLeaf',
    'onBranchFrom', 'onLatest', 'onToggle']) {
    assert.ok(!panelSrc.includes(gone), `面板不应再有「${gone}」`);
  }
  // App 侧不再接这些口（树的切叶只剩跳转与加载全部）
  assert.ok(!appSrc.includes('handleTreeSetLeaf'), 'App 不再有「切到此」的接线');
  assert.ok(!appSrc.includes('handleTreeBranchFrom'), 'App 不再有「从此分支」的接线');
  assert.ok(!appSrc.includes('handleTreeLatest'), 'App 不再有「回到最新」的接线');
  assert.ok(!appSrc.includes('RewindConfirmDialog'), '树不再触发文件还原确认弹窗');
  assert.ok(!appSrc.includes('RewindConflictDialog'), '树不再触发冲突弹窗');
});

test('④b off_path 行常显 + 灰化（可见、可点，不隐藏）', () => {
  // 减负后「在分支上」不再是行内徽标：灰化 + 行 title 承担提示（App 先切过去再跳）
  assert.ok(!panelSrc.includes('stree-onbranch'), '不再有「在分支上」文本徽标');
  assert.ok(!panelSrc.includes('stree-branch-badge'), '不再有「N 分支」徽标');
  assert.match(panelSrc, /data-off-path=\{row\.onPath \? "false" : "true"\}/, '灰化由 CSS 按 off-path 渲染');
  assert.ok(!panelSrc.includes('collapsib'), '没有折叠（树上全是 user 行，无需折叠）');
});

test('④c 平级列表：没有缩进/连接线/分支圆点/钳制（2026-12 用户拍板，线性时间线）', () => {
  for (const gone of ['stree-depth', 'stree-extra', 'visualDepth', 'clampedSteps', 'depthClamped',
    'data-depth', 'data-vdepth', 'data-clamped', 'data-branch', 'data-last', 'stree-indent']) {
    assert.ok(!panelSrc.includes(gone), `面板不应再有「${gone}」（缩进层级已删）`);
  }
  // 行不再带内联 style（缩进变量已删）
  assert.ok(!/style=\{/.test(panelSrc), '行上没有内联 style（无 --stree-* 缩进变量）');
});

test('④c 截断提示 + 加载全部（不静默截断）', () => {
  assert.match(panelSrc, /truncation\.text/, '截断提示文案来自纯函数');
  assert.match(panelSrc, /onLoadAll/, '「加载全部」按钮');
  assert.match(appSrc, /sessionTree\.refresh\(\{ loadAll: true \}\)/, '加载全部真的重新拉树（max_nodes=0）');
});

// ---------- ⑤ 每会话树缓存（2026-12 会话切换优化批 1） ----------
//
// 行为断言在 createTreeCache（纯对象，直测）；hook 的接线（缓存实例随 hook 挂、
// 切会话先查缓存、失效事件清条目并重拉、connectionChanged true 升代数）按仓库惯例
// 用源码文本钉住——React effect 在 node 单测里跑不了，钉错了不会编译报错、只静默错。
import { createTreeCache } from '../src/app-hooks/use-session-tree.ts';

test('⑤a createTreeCache：命中/未命中/覆盖/bust 幂等（纯对象，返回新引用）', () => {
  const cache = createTreeCache();
  assert.equal(cache.get('s1'), null, '未拉过 = 未命中');
  const t1 = { leafSeq: 3, nodes: [], total: 0 };
  const t2 = { leafSeq: 4, nodes: [], total: 0 };
  cache.set('s1', t1);
  assert.equal(cache.get('s1'), t1, '命中返回最近一次成功拉取的树');
  cache.set('s1', t2);
  assert.equal(cache.get('s1'), t2, '成功拉取覆盖');
  cache.set('s2', t1);
  cache.bust('s1');
  assert.equal(cache.get('s1'), null, '失效事件清掉该会话条目');
  assert.equal(cache.get('s2'), t1, '别的会话不受影响');
  assert.equal(cache.bust('s1'), cache, 'bust 幂等（没有条目返回同一份）');
  // 空 sessionId 是坏输入：不命中也不进键
  assert.equal(cache.get(''), null);
});

test('⑤b 连接代数：换后端（bumpGeneration）后旧代数条目整体落空，新条目独立', () => {
  const cache = createTreeCache();
  const t1 = { leafSeq: 1, nodes: [], total: 0 };
  cache.set('s1', t1);
  cache.bumpGeneration();
  assert.equal(cache.get('s1'), null, '旧后端的树对新后端没有意义（旧 id 甚至不存在）');
  cache.set('s1', { leafSeq: 2, nodes: [], total: 0 });
  assert.notEqual(cache.get('s1'), null, '新代数照常读写');
  cache.bust('s1');
  assert.equal(cache.get('s1'), null, 'bust 只清新代数');
});
