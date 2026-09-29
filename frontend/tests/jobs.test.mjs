// 后台任务（jobs）前端契约：wire 映射、EndedBy 文案、时长/输出切片、
// 归约（同一任务一张卡）、WSAgent 的 job.* 三个方法与 DemoAgent 的演示链路。
//
// 这些是最容易「静默走偏」的一层：后端改一个键名不会让前端编译失败，只会
// 变成空字符串（超时文案不显示、结束原因显示成「正常结束」）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  JOB_NOTICE_PREFIX, activeJobCount, formatDuration, isJobActive, isJobNotice, isJobTimeout,
  jobElapsedMs, jobFromWire, jobNoticeBody, jobOutcomeText, jobTone, shortSessionId, sortJobs, tailOf, upsertJob,
} from '../src/shared/jobs.ts';
import { mapEvent } from '../src/agent/ws/events.ts';
import { reduce, reduceSessionStates } from '../src/shared/store.ts';
import { WSAgent } from '../src/agent/ws/index.ts';
import { DemoAgent } from '../src/agent/demo/index.ts';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: null, historyReady: false };

/** 后端 JobInfo 的 wire 形状（snake_case，见 docs/jobs.md §4）。 */
const WIRE = {
  id: 'j1', kind: 'bash', label: 'go test ./... -count=1',
  status: 'running', ended_by: '', detail: '', session_id: 's1', owner_session_id: 's1',
  started_at: '2026-09-29T10:00:00Z', finished_at: '',
  output_tail: 'ok  internal/agent\t4.106s', output_path: 'C:/sessions/jobs/j1.log',
};

// ---------- wire 映射 ----------

test('jobFromWire 逐字读契约字段（snake_case）', () => {
  const job = jobFromWire(WIRE);
  assert.deepEqual(job, {
    id: 'j1', kind: 'bash', label: 'go test ./... -count=1',
    status: 'running', ended_by: '', detail: '', session_id: 's1', owner_session_id: 's1',
    started_at: '2026-09-29T10:00:00Z', finished_at: '',
    output_tail: 'ok  internal/agent\t4.106s', output_path: 'C:/sessions/jobs/j1.log',
  });
});

test('jobFromWire 容忍 camelCase（并行实现期 tag 分叉不至于静默成空串）', () => {
  const job = jobFromWire({ id: 'j2', endedBy: 'user', sessionId: 's9', ownerSessionId: 's-root', startedAt: 'T', outputTail: 'x' });
  assert.equal(job.ended_by, 'user');
  assert.equal(job.session_id, 's9');
  assert.equal(job.owner_session_id, 's-root');
  assert.equal(job.started_at, 'T');
  assert.equal(job.output_tail, 'x');
});

test('jobFromWire 对缺字段/坏载荷不抛异常（空串是中性态，不是崩溃）', () => {
  assert.equal(jobFromWire(null).id, '');
  assert.equal(jobFromWire({ id: 42 }).id, '', '非字符串一律空串');
});

// ---------- 状态与文案 ----------

test('EndedBy 文案：用户停的 / 它挂了 / 超时 / 后端重启中断', () => {
  const done = { ...jobFromWire(WIRE), status: 'completed', ended_by: 'self', finished_at: 'T' };
  assert.equal(jobOutcomeText(done), '正常结束');

  // 用户停的：即使进程以非零码收尾也不该显示成「它挂了」（归属比现象权威）
  const byUser = { ...done, status: 'killed', ended_by: 'user', detail: 'signal: killed' };
  assert.equal(jobOutcomeText(byUser), '你停的');
  assert.equal(jobTone(byUser), 'off');

  const failed = { ...done, status: 'failed', detail: '退出码 1' };
  assert.equal(jobOutcomeText(failed), '它挂了');
  assert.equal(jobTone(failed), 'err');

  // 超时只有 Detail 里有线索（契约 §5：self + 超时），必须排在「它挂了」之前
  const timeout = { ...done, status: 'failed', detail: '超时（120s）' };
  assert.equal(jobOutcomeText(timeout), '超时');
  assert.equal(isJobTimeout(timeout), true);

  const backend = { ...done, status: 'killed', ended_by: 'backend', detail: '后端重启' };
  assert.equal(jobOutcomeText(backend), '后端重启中断');

  assert.equal(jobOutcomeText({ ...done, ended_by: 'agent' }), 'Agent 停的');
  assert.equal(jobOutcomeText(jobFromWire(WIRE)), '运行中');
  assert.equal(jobOutcomeText({ ...jobFromWire(WIRE), status: 'stopping' }), '正在结束…');
});

test('只有 running/stopping 算「在跑」（决定「结束」按钮与角标计数）', () => {
  assert.equal(isJobActive(jobFromWire(WIRE)), true);
  assert.equal(isJobActive({ ...jobFromWire(WIRE), status: 'stopping' }), true);
  for (const status of ['completed', 'killed', 'failed']) {
    assert.equal(isJobActive({ ...jobFromWire(WIRE), status }), false, status);
  }
  assert.equal(activeJobCount([jobFromWire(WIRE), { ...jobFromWire(WIRE), id: 'j2', status: 'completed' }]), 1);
});

test('时长：运行中按 now 计，结束后定格在 finished_at', () => {
  const job = jobFromWire({ ...WIRE, started_at: '2026-09-29T10:00:00Z' });
  assert.equal(jobElapsedMs(job, Date.parse('2026-09-29T10:00:12Z')), 12_000);
  assert.equal(jobElapsedMs({ ...job, finished_at: '2026-09-29T10:01:00Z' }, Date.parse('2026-09-29T10:09:00Z')), 60_000);
  // 坏时间戳不编数字
  assert.equal(jobElapsedMs(jobFromWire({ id: 'x' }), 1), 0);
  assert.equal(formatDuration(0), '0s');
  assert.equal(formatDuration(45_000), '45s');
  assert.equal(formatDuration(125_000), '2m 05s');
  assert.equal(formatDuration(3_600_000), '1h 00m');
});

test('tailOf 取末尾 n 行并给出总行数', () => {
  assert.deepEqual(tailOf('', 4), { lines: [], clipped: false, total: 0 });
  assert.deepEqual(tailOf('a\nb\n', 4), { lines: ['a', 'b'], clipped: false, total: 2 });
  const t = tailOf('1\n2\n3\n4\n5', 2);
  assert.deepEqual(t.lines, ['4', '5']);
  assert.equal(t.clipped, true);
  assert.equal(t.total, 5);
});

test('upsertJob 保位置、sortJobs 在跑的在前', () => {
  const a = jobFromWire({ ...WIRE, id: 'a', started_at: '2026-09-29T10:00:00Z' });
  const b = jobFromWire({ ...WIRE, id: 'b', started_at: '2026-09-29T11:00:00Z' });
  let list = upsertJob([], a);
  list = upsertJob(list, b);
  list = upsertJob(list, { ...a, label: 'updated' });
  assert.deepEqual(list.map((j) => j.id), ['a', 'b'], '更新不该让任务跳到最前');
  assert.equal(list[0].label, 'updated');
  // 结束的任务排到在跑的任务之后（不管它多新）
  const doneNew = { ...b, status: 'completed' };
  assert.deepEqual(sortJobs([doneNew, a]).map((j) => j.id), ['a', 'b']);
  assert.equal(shortSessionId(''), '—');
  assert.equal(shortSessionId('20260929-abcdef'), '20260929');
});

// ---------- 唤醒通告（user 角色消息 + 文本前缀识别） ----------

test('JOB_NOTICE_PREFIX 与后端 protocol.JobNoticePrefix 逐字一致（含尾空格）', () => {
  // 尾空格是契约的一部分：后端用它做前缀匹配，前端少一个空格就永远认不出来
  // （通告会静默变回用户气泡——用户以为是自己说的话）
  assert.equal(JOB_NOTICE_PREFIX, '[后台任务通告] ');
  assert.equal(JOB_NOTICE_PREFIX.length, '[后台任务通告] '.length);
  assert.equal(JOB_NOTICE_PREFIX.endsWith(' '), true, '尾空格必须保留');
});

test('通告识别只看文本前缀，不看角色', () => {
  assert.equal(isJobNotice('[后台任务通告] 后台任务 go test 结束（退出码 0）。'), true);
  assert.equal(isJobNotice('用户自己打的一句话'), false);
  // 前缀在中间出现不算（只有开头才算——否则用户引用通告文本会被误判）
  assert.equal(isJobNotice('看看这个 [后台任务通告] 是什么'), false);
  assert.equal(jobNoticeBody('[后台任务通告] 正文在此 '), '正文在此');
  assert.equal(jobNoticeBody('普通消息'), '普通消息');
});

test('通告渲染成通告条而不是用户气泡（实时路径）', () => {
  const s = reduce(base, { type: 'userMessage', sessionId: 's1', text: '[后台任务通告] 用户主动结束了后台任务 X。不要重启它。' });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'notice', 'user 角色的通告不能变成用户气泡');
  assert.equal(s.blocks[0].text, '用户主动结束了后台任务 X。不要重启它。', '前缀由标签承担，正文不带前缀');
  // 普通用户消息照旧是气泡
  const s2 = reduce(s, { type: 'userMessage', sessionId: 's1', text: '帮我看下构建' });
  assert.equal(s2.blocks.at(-1).kind, 'user');
});

test('通告在历史回放里同样是通告条（两条路径必须一致）', () => {
  let s = reduce(base, {
    type: 'historyLoaded', sessionId: 's1',
    history: {
      sessionId: 's1', busy: false, pending: null, todos: [],
      messages: [
        { role: 'user', content: '帮我看下构建' },
        { role: 'assistant', content: '好' },
        { role: 'user', content: '[后台任务通告] 后台任务 go test 结束（退出码 0）。' },
      ],
    },
  });
  assert.equal(s.blocks[0].kind, 'user');
  assert.equal(s.blocks.at(-1).kind, 'notice', '刷新后同一句话不能换张脸');
  assert.equal(s.blocks.at(-1).text, '后台任务 go test 结束（退出码 0）。');
});

// ---------- 纯映射（events.ts） ----------

test('job.started / job.settled 映射成前端事件（载荷就是 JobInfo）', () => {
  const started = mapEvent('job.started', WIRE);
  assert.equal(started.type, 'jobStarted');
  assert.equal(started.sessionId, 's1');
  assert.equal(started.job.id, 'j1');
  assert.equal(started.job.output_tail, 'ok  internal/agent\t4.106s');

  const settled = mapEvent('job.settled', { ...WIRE, status: 'completed', ended_by: 'self', finished_at: 'T' });
  assert.equal(settled.type, 'jobSettled');
  assert.equal(settled.job.ended_by, 'self');
});

test('无归属会话的任务事件 sessionId 为空（只进全局面板，不进任何时间线）', () => {
  const ev = mapEvent('job.started', { ...WIRE, session_id: '', owner_session_id: '' });
  assert.equal(ev.sessionId, '');
});

test('子 Agent 起的任务按 owner_session_id 上**父会话**的时间线', () => {
  // 子会话不进侧栏：按执行会话（session_id）上卡等于用户在主对话里什么都看不到，
  // 所以归属必须取 owner_session_id。
  const child = { ...WIRE, session_id: 's-child', owner_session_id: 's-parent' };
  const started = mapEvent('job.started', child);
  assert.equal(started.sessionId, 's-parent', '任务卡应挂在父会话上');
  assert.equal(started.job.session_id, 's-child', '执行会话仍要如实保留');
  const settled = mapEvent('job.settled', { ...child, status: 'killed', ended_by: 'user' });
  assert.equal(settled.sessionId, 's-parent');
});

test('owner_session_id 缺席时回落 session_id（老后端/顶层会话起的任务）', () => {
  const ev = mapEvent('job.started', { ...WIRE, session_id: 's1', owner_session_id: '' });
  assert.equal(ev.sessionId, 's1');
});

// ---------- 归约（reduce.ts） ----------

test('jobStarted 建卡；jobSettled 就地更新同一张卡（不追加第二张）', () => {
  let s = reduce(base, { type: 'jobStarted', sessionId: 's1', job: jobFromWire(WIRE) });
  assert.equal(s.blocks.length, 1);
  assert.equal(s.blocks[0].kind, 'job');
  const uid = s.blocks[0].uid;

  s = reduce(s, {
    type: 'jobSettled', sessionId: 's1',
    job: jobFromWire({ ...WIRE, status: 'completed', ended_by: 'self', detail: '退出码 0', finished_at: 'T' }),
  });
  assert.equal(s.blocks.length, 1, '同一个任务只该有一张卡');
  assert.equal(s.blocks[0].uid, uid, 'uid 稳定（React key 不重挂）');
  assert.equal(s.blocks[0].job.status, 'completed');
  assert.equal(s.blocks[0].job.detail, '退出码 0');
});

test('只收到 settled（重连后）也要补一张已结束的卡——不能静默丢弃', () => {
  const s = reduce(base, {
    type: 'jobSettled', sessionId: 's1',
    job: jobFromWire({ ...WIRE, status: 'killed', ended_by: 'user' }),
  });
  assert.equal(s.blocks.length, 1);
  assert.equal(jobOutcomeText(s.blocks[0].job), '你停的');
});

test('任务卡按归属会话路由（不串到别的会话时间线）', () => {
  let all = {};
  all = reduceSessionStates(all, { type: 'jobStarted', sessionId: 's1', job: jobFromWire(WIRE) });
  assert.equal(all.s1.blocks.length, 1);
  assert.equal(all.s2, undefined, '别的会话不该凭空多出状态');
  // 无归属（session_id 为空）的任务不进任何会话时间线
  all = reduceSessionStates(all, { type: 'jobStarted', sessionId: '', job: jobFromWire({ ...WIRE, id: 'j9' }) });
  assert.equal(all.s1.blocks.length, 1);
  assert.equal(Object.keys(all).length, 1);
});

test('冷回放（切走再切回）不丢任务卡——历史里没有 jobs', () => {
  let s = reduce(base, { type: 'jobStarted', sessionId: 's1', job: jobFromWire(WIRE) });
  s = reduce(s, { type: 'userMessage', sessionId: 's1', text: '问题' });
  s = reduce(s, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: '问题' }], busy: false, pending: null, todos: [] },
  });
  assert.equal(s.blocks.filter((b) => b.kind === 'job').length, 1, '重建历史不该丢掉任务卡');
  assert.equal(s.blocks.at(-1).kind, 'job');
});

// ---------- WSAgent（live） ----------

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor(url) { this.url = url; FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result, error) { this.receive({ id: this.sent.at(-1).id, result, error }); }
}

function setup(t) {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const events = [], agent = new WSAgent('localhost:1234');
  const off = agent.subscribe((e) => events.push(e));
  t.after(off);
  return { agent, events, ws: FakeSocket.latest };
}

test('job.list 不带会话过滤时刷新全局缓存；带过滤时不覆盖它', async (t) => {
  const { agent, ws } = setup(t);
  const all = agent.jobAdmin.listJobs();
  assert.equal(ws.sent.at(-1).method, 'job.list');
  assert.equal(ws.sent.at(-1).params, undefined, '不传会话 = 全部');
  ws.reply({ jobs: [WIRE] });
  const list = await all;
  assert.equal(list[0].id, 'j1');
  assert.equal(agent.jobAdmin.jobs()[0].label, WIRE.label);

  const scoped = agent.jobAdmin.listJobs('s2');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's2' });
  ws.reply({ jobs: [{ ...WIRE, id: 'j2', session_id: 's2' }] });
  await scoped;
  assert.deepEqual(agent.jobAdmin.jobs().map((j) => j.id), ['j1'], '按会话过滤不该覆盖全局缓存');
});

test('job.started / job.settled 事件增量进面板缓存并通知订阅者', async (t) => {
  const { agent, ws } = setup(t);
  let notified = 0;
  const off = agent.jobAdmin.onJobsChanged(() => { notified++; });
  t.after(off);
  ws.receive({ method: 'job.started', params: WIRE });
  assert.equal(notified, 1);
  assert.equal(agent.jobAdmin.jobs()[0].id, 'j1');
  ws.receive({ method: 'job.settled', params: { ...WIRE, status: 'completed', ended_by: 'self' } });
  assert.equal(notified, 2);
  assert.equal(agent.jobAdmin.jobs().length, 1, 'settled 是更新不是追加');
  assert.equal(agent.jobAdmin.jobs()[0].status, 'completed');
});

test('job.kill 走协议方法并回填即时快照；job.log 读全量输出', async (t) => {
  const { agent, ws } = setup(t);
  const killing = agent.jobAdmin.killJob('j1');
  assert.equal(ws.sent.at(-1).method, 'job.kill');
  assert.deepEqual(ws.sent.at(-1).params, { id: 'j1' });
  ws.reply({ job: { ...WIRE, status: 'stopping', ended_by: 'user' } });
  const job = await killing;
  assert.equal(job.status, 'stopping');
  assert.equal(agent.jobAdmin.jobs()[0].status, 'stopping');

  const logging = agent.jobAdmin.readJobLog('j1');
  assert.equal(ws.sent.at(-1).method, 'job.log');
  assert.deepEqual(ws.sent.at(-1).params, { id: 'j1' });
  ws.reply({ data: 'full output', truncated: true });
  assert.deepEqual(await logging, { data: 'full output', truncated: true });
});

// ---------- DemoAgent（无后端也要全量跑 UI） ----------

test('演示模式：起后台任务 → 读输出 → 用户点「结束」→ 定时收尾不改写它', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const agent = new DemoAgent();
  const events = [];
  agent.subscribe((e) => events.push(e));
  const session = agent.currentId;

  agent.send(session, '跑一下全量测试');
  // 分两步推进：node 的 mock timers 不会在同一次 tick 里执行「tick 期间新排的
  // 定时器」——起任务（t+2000）与它随后逐行追加的输出（起任务后 +400ms 起）
  // 是两层定时器，一次 tick 只会跑到第一层（输出会是空的，测试假红）。
  t.mock.timers.tick(6000);
  t.mock.timers.tick(30_000);

  const started = events.find((e) => e.type === 'jobStarted');
  assert.ok(started, '演示轮次应起一个后台任务（否则 UI 无卡可看）');
  const id = started.job.id;
  assert.equal(started.sessionId, session, '任务归属当前会话');
  assert.equal(agent.jobAdmin.jobs()[0].status, 'running');
  assert.equal(agent.jobAdmin.jobs()[0].ended_by, '');

  return agent.jobAdmin.readJobLog(id).then((log) => {
    assert.ok(log.data.includes('ok  '), '运行中也能拉到此刻的全量输出');
    return agent.jobAdmin.killJob(id).then((killed) => {
      assert.equal(killed.status, 'killed');
      assert.equal(killed.ended_by, 'user', '用户点「结束」= EndedUser（与 agent 的 job_kill 同路径、by 不同）');
      assert.equal(jobOutcomeText(killed), '你停的');
      const settled = events.filter((e) => e.type === 'jobSettled');
      assert.equal(settled.length, 1);
      // 用户先停掉之后，脚本的定时收尾必须是 no-op——否则「你停的」会自己变成「正常结束」
      return agent.jobAdmin.killJob(id).then((again) => {
        assert.equal(again.ended_by, 'user');
        assert.equal(agent.jobAdmin.jobs()[0].ended_by, 'user');
      });
    });
  });
});

test('演示模式：任务 settle 后投一条带前缀的 user 角色通告（唤醒投递的前端形态）', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const agent = new DemoAgent();
  const events = [];
  agent.subscribe((e) => events.push(e));
  const session = agent.currentId;
  agent.send(session, '跑一下全量测试');
  // 多步推进（mock timers 不会在同一次 tick 里执行 tick 期间新排的定时器）
  for (let i = 0; i < 10; i++) t.mock.timers.tick(2000);
  await agent.confirm(session, 'd-c3', true);
  for (let i = 0; i < 10; i++) t.mock.timers.tick(2000);
  const notice = events.find((e) => e.type === 'userMessage' && isJobNotice(e.text));
  assert.ok(notice, '任务 settle 后应投一条通告（模型要当作用户回合才能回应）');
  assert.equal(notice.sessionId, session, '通告投进归属会话');
  assert.equal(jobOutcomeText(agent.jobs()[0]), '正常结束');
  // 通告走 reduce 之后是通告条，不是用户气泡
  const s = reduce(base, notice);
  assert.equal(s.blocks.at(-1).kind, 'notice');
  assert.equal(s.blocks.at(-1).text, '后台任务 go test ./... -count=1 结束（退出码 0）。用 job_output 读输出。');
});

test('演示模式：未知任务 id 不静默成功（抛错给 UI 提示）', async () => {
  const agent = new DemoAgent();
  await assert.rejects(agent.jobAdmin.killJob('nope'), /任务不存在/);
  await assert.rejects(agent.jobAdmin.readJobLog('nope'), /任务不存在/);
  assert.deepEqual(agent.jobAdmin.jobs(), []);
});
