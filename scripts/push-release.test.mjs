// push-release.mjs 的契约测试：判定（检查点/手工/发布）与发布说明组装是纯函数，
// 错判的代价是「检查点漏进公开历史」或「发布说明张冠李戴」，所以钉住。
//
// 运行: node --test scripts/push-release.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  classifyCommit, subjectType, buildReleaseMessage, releaseVersion, planCleanHistory, parseLog,
  parseMasterTrailer, hasNewContent, findCommitByTree, CHECKPOINT_MARKER,
} from './push-release.mjs';

const checkpoint = (subject, body = `${CHECKPOINT_MARKER} 第 1 轮\n\n用户原话：\n> 改点东西`) => ({ hash: 'h', author: 'a', date: '2026-10-19T10:00:00+08:00', subject, body });

test('classifyCommit：正文标记、老格式标题、发布提交、手工提交', () => {
  assert.equal(classifyCommit(checkpoint('feat(server): 加确认门代理')), 'checkpoint');
  // 老格式（没有正文标记）：标题以「第 N 轮」开头也算检查点
  assert.equal(classifyCommit({ subject: '第 12 轮：改点东西', body: '' }), 'checkpoint');
  assert.equal(classifyCommit({ subject: '第12轮', body: '' }), 'checkpoint');
  assert.equal(classifyCommit({ subject: 'release: 0.2.18', body: '' }), 'release');
  assert.equal(classifyCommit({ subject: 'fix(shell): 更新器修复', body: '' }), 'manual');
  // 正文里提到标记但不是检查点正文（例如手工提交引用了它）不该误判——
  // 这条是已知边界：标记出现在正文任意位置即判检查点，只在检查点提交里会出现。
  assert.equal(classifyCommit({ subject: 'docs: 说明', body: `提到 ${CHECKPOINT_MARKER}` }), 'checkpoint');
});

test('subjectType：conventional commits 解析，不合法回落 other', () => {
  assert.deepEqual(subjectType('feat(server): 子会话确认门'), { type: 'feat', scope: 'server', text: '子会话确认门' });
  assert.deepEqual(subjectType('fix: 修复'), { type: 'fix', scope: '', text: '修复' });
  assert.deepEqual(subjectType('refactor!: 破坏性'), { type: 'refactor', scope: '', text: '破坏性' });
  assert.deepEqual(subjectType('第 3 轮：中文标题'), { type: 'other', scope: '', text: '第 3 轮：中文标题' });
});

test('buildReleaseMessage：按类型分组、去重、附手工提交、含统计', () => {
  const commits = [
    checkpoint('feat(server): 确认门代理'),
    checkpoint('feat(server): 确认门代理'), // 同一件事改了两轮：只留一条
    checkpoint('fix(shell): 更新器暂存复制'),
    checkpoint('feat(frontend): 上下文环'),
    { hash: 'h', author: 'a', date: '', subject: 'docs: 补文档', body: '' },
    { hash: 'h', author: 'a', date: '', subject: 'release: 0.2.17', body: '' },
  ];
  const msg = buildReleaseMessage('0.2.18', commits);
  assert.match(msg, /^release: 0\.2\.18\n/);
  assert.match(msg, /4 个检查点 \+ 1 个手工提交/); // 统计按提交数（含重复），列条目才去重
  // feat 在 fix 之前（TYPE_ORDER）
  assert.ok(msg.indexOf('### feat') < msg.indexOf('### fix'));
  assert.equal((msg.match(/确认门代理/g) || []).length, 1, '重复的检查点只列一次');
  assert.match(msg, /- server: 确认门代理/);
  assert.match(msg, /### 手工提交\n- docs: 补文档/);
  // release 提交不进说明（它是基线，不是本轮改动）
  assert.doesNotMatch(msg, /0\.2\.17/);
});

test('buildReleaseMessage：大量检查点折叠', () => {
  const commits = Array.from({ length: 30 }, (_, i) => checkpoint(`feat(internal): 第 ${i} 项`));
  const msg = buildReleaseMessage('1.0.0', commits);
  assert.match(msg, /…另有 10 项/);
});

test('releaseVersion / planCleanHistory：版本与快照计划', () => {
  assert.equal(releaseVersion('release: 0.2.18'), '0.2.18');
  assert.equal(releaseVersion('release: android 0.2.11 (versionCode 11)'), 'android 0.2.11 (versionCode 11)');
  const plan = planCleanHistory([
    { subject: 'release: 0.2.3', tree: 't1', date: '2026-10-12T00:00:00+08:00' },
  ]);
  assert.deepEqual(plan, [{ version: '0.2.3', tree: 't1', date: '2026-10-12T00:00:00+08:00', subject: 'release: 0.2.3' }]);
});

test('parseLog：按 %x1e 分记录、正文里的换行原样保留', () => {
  const raw = 'abc\x1fT\x1f2026-10-19T10:00:00+08:00\x1ffeat: x\x1fbody line 1\nbody line 2\x1e\ndef\x1fT\x1f2026-10-19T11:00:00+08:00\x1ffix: y\x1f\x1e';
  const got = parseLog(raw);
  assert.equal(got.length, 2);
  assert.equal(got[0].subject, 'feat: x');
  assert.equal(got[0].body, 'body line 1\nbody line 2');
  assert.equal(got[1].subject, 'fix: y');
  assert.equal(got[1].body, '');
});

test('parseMasterTrailer：读发布提交的 master-commit 尾注', () => {
  assert.equal(parseMasterTrailer('body\n\nmaster-commit: 1f95af3bce9559b86dc0deaa2a87d6b66ff130d5'), '1f95af3bce9559b86dc0deaa2a87d6b66ff130d5');
  assert.equal(parseMasterTrailer('body\n\nmaster-commit: abcdef1'), 'abcdef1');
  assert.equal(parseMasterTrailer('没有尾注'), null);
  assert.equal(parseMasterTrailer(''), null);
});

test('buildReleaseMessage：带上 master-commit 尾注（下次发版的定位依据）', () => {
  const msg = buildReleaseMessage('0.2.19', [], { masterCommit: 'deadbeef' });
  assert.match(msg, /^release: 0\.2\.19\n/);
  assert.match(msg, /\nmaster-commit: deadbeef$/);
});

test('hasNewContent：树相同 = 无新内容', () => {
  assert.equal(hasNewContent(null, 't'), true, '首次发布恒有内容');
  assert.equal(hasNewContent('t', 't'), false);
  assert.equal(hasNewContent('t', 'u'), true);
});

test('findCommitByTree：按树在 master 历史里回溯定位', () => {
  const lines = ['aaa 111', 'bbb 222', 'ccc 333'];
  assert.equal(findCommitByTree(lines, '222'), 'bbb');
  assert.equal(findCommitByTree(lines, '999'), null);
});
