// 内置工具目录里 workspace_sync 的文案契约：2026-10 直连架构下它的行为是
// 「提交当前分支 +（可选）直接推送 origin」——**没有**合并进程、没有集成分支。
// 这三处过时文案（desc / push 参数 / doc）曾同时出现在后端种子与前端目录里，
// 会直接误导模型与用户（模型照它说「起了合并进程」，用户在编辑器里也看到）。
//
// 同时钉住推送闸门的说明：lxcode 本体仓库的推送被拒绝、发版走 push-release.mjs
// ——这条必须出现在工具说明里，模型读到工具结果时才不会把闸门当故障去绕过。
import test from 'node:test';
import assert from 'node:assert/strict';
import { BUILTIN_TOOLS, seedAgents } from '../src/shared/agent-seeds.ts';

const toolOf = (id) => BUILTIN_TOOLS.find((t) => t.id === id);

test('workspace_sync 的目录文案说的是实际行为（提交当前分支 + 直接推送）', () => {
  const sync = toolOf('workspace_sync');
  assert.ok(sync, '目录里必须有 workspace_sync（编辑器 chip 只读目录）');
  assert.match(sync.desc, /提交当前分支/);
  assert.match(sync.desc, /推送/);
  const pushParam = sync.params.find((p) => p.name === 'push');
  assert.ok(pushParam, 'push 参数必须在');
  assert.match(pushParam.desc, /直接推送到远程 origin/);
  assert.doesNotMatch(pushParam.desc, /合并成功后/);
  assert.match(sync.doc, /不 force/);
  assert.match(sync.doc, /push-release\.mjs/, '说明里必须点出发版通道（闸门指引）');
});

test('workspace_sync 的文案不再提退役的合并流程', () => {
  const sync = toolOf('workspace_sync');
  const text = [sync.desc, sync.doc, ...sync.params.map((p) => p.desc ?? '')].join('\n');
  for (const banned of ['起合并进程', '合并进程', '合并成功后', '集成分支', '后台任务']) {
    assert.ok(!text.includes(banned), `workspace_sync 文案不该再出现「${banned}」: ${text}`);
  }
});

test('代码/测试 Agent 的种子提示词含推送纪律（不得直接 git push）', () => {
  const agents = seedAgents();
  for (const id of ['coder', 'tester']) {
    const a = agents.find((x) => x.id === id);
    assert.ok(a, `种子里必须有 ${id}`);
    assert.match(a.prompt, /git push/, `${id} 的提示词应含推送纪律`);
    assert.match(a.prompt, /workspace_sync/, `${id} 的提示词应指明唯一推送通道`);
  }
});
