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

// 职责分工（2026-12 用户拍板）：代码 Agent 只写代码、不跑测试；测试与验证归
// 测试 Agent。前端种子是演示名单与 Agent 编辑器的默认值，与后端种子同款文案。
test('代码 Agent 的种子文案不把测试写成自己的活（分工：测试归测试 Agent）', () => {
  const agents = seedAgents();
  const coder = agents.find((a) => a.id === 'coder');
  assert.ok(coder);
  for (const banned of ['跑构建测试', '构建测试通过才算完成']) {
    assert.ok(!coder.desc.includes(banned), `coder.desc 不该含「${banned}」: ${coder.desc}`);
    assert.ok(!coder.prompt.includes(banned), `coder.prompt 不该含「${banned}」`);
  }
  assert.match(coder.prompt, /不跑测试/, 'coder 的提示词要明说不跑测试');
  assert.match(coder.prompt, /编译/, 'coder 保留编译自检底线');
  assert.match(coder.desc, /测试交给测试 Agent/, 'desc 是主 Agent 的选人信号，要把测试指向测试 Agent');

  const tester = agents.find((a) => a.id === 'tester');
  assert.ok(tester);
  assert.match(tester.desc, /验收/, 'tester 的 desc 应点明验收职责');
  assert.match(tester.prompt, /代码 Agent/, 'tester 的提示词应写明接手 coder 交回的改动');
});

// 调研 Agent 的执行面（2026-12 用户反馈）：只读 bash 在目录里、在白名单里、
// 描述里点明；完整 bash 不在白名单里（写操作只能靠命令级判据拦，不是白名单）。
test('调研 Agent 有只读执行面，没有完整 bash', () => {
  const tools = BUILTIN_TOOLS.map((t) => t.id);
  assert.ok(tools.includes('bash_readonly'), '目录里必须有 bash_readonly（编辑器 chip 只读目录）');
  const ro = toolOf('bash_readonly');
  assert.equal(ro.risk, 'low', '只读执行面必须是低危（不触发确认门）');
  const researcher = seedAgents().find((a) => a.id === 'researcher');
  assert.ok(researcher, '种子里必须有 researcher');
  assert.ok(researcher.tools.includes('bash_readonly'), '调研 Agent 的白名单应含 bash_readonly');
  assert.ok(!researcher.tools.includes('bash'), '调研 Agent 不该有完整 bash');
  assert.match(researcher.desc, /只读/, '描述是主 Agent 的选人信号，要点明只读');
  assert.match(researcher.desc, /查数据库/, '描述要点明能查数据库（sqlite3 等）');
  assert.equal(researcher.approval, 'auto', '全线 auto（2026-12 用户拍板）');
});

// repo-manager（2026-12 新增内置种子 Agent，与后端 agents_seeds.go 对齐）：
// 仓库管理专属执行面——提交/推送/状态/回滚。白名单只有工作区三件套（无写文件
// 工具，「不改业务代码」是结构保证）、approval=auto（2026-12 全线 auto，高危面
// 靠工具层 ConfirmForced 硬闸兜底）、main 的委派名单含它（desc 是主 Agent 的选人信号）。
test('仓库管理 Agent 的种子契约：工作区三件套 + auto + 进委派名单', () => {
  const rm = seedAgents().find((a) => a.id === 'repo-manager');
  assert.ok(rm, '种子里必须有 repo-manager');
  assert.deepEqual(
    [...rm.tools].sort(),
    ['workspace_rollback', 'workspace_status', 'workspace_sync'],
    '白名单应恰为工作区三件套（不写业务代码是结构保证）',
  );
  assert.equal(rm.approval, 'auto', '全线 auto（2026-12 用户拍板）');
  for (const need of ['提交', '推送', 'workspace_sync', 'workspace_status', 'workspace_rollback']) {
    assert.ok(rm.desc.includes(need), `desc（主 Agent 的选人信号）缺「${need}」: ${rm.desc}`);
  }
  assert.match(rm.prompt, /git push/, '提示词应含推送纪律');
  assert.match(rm.prompt, /workspace_sync/, '提示词应指明唯一推送通道');
  assert.match(rm.prompt, /首行/, '提示词应写明提交信息默认取最近一条用户消息首行');
  assert.match(rm.prompt, /丢用户的工作/, '回滚纪律要落到结尾（曾被发现被截断成「都可能」）');
  const main = seedAgents().find((a) => a.id === 'main');
  assert.ok(main.delegates.includes('repo-manager'), 'main 的委派名单应含 repo-manager');
});
