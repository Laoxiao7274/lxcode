// update-bridge.test.mjs —— 更新桥对新 IPC 契约的映射钉子：
//  shell 侧手动检查已改为三态结局（shell/src/update-poll.ts 的
//  manualCheckOutcome：latest / available / error），IPC update:check 直接返回它。
//  host.ts 的 check() 负责还原成 UpdateProvider 状态机吃的旧形状
//  {available, manifest}——error 必须抛出（手动检查不许静默），available 带
//  normalize 过的 manifest，latest 给 {available:false, manifest:null}。
//  顺带钉 onAvailable 推送通道的归一（非对象/无版本号的推送丢弃——不进状态机）。
import test from 'node:test';
import assert from 'node:assert/strict';

const withBridge = (raw) => {
  globalThis.window = { __LX__: { update: raw } };
  return import('../src/agent/host.ts');
};

test('check：三态 available → {available:true, manifest}（大小/notes 保留）', async () => {
  const { getUpdateBridge } = await withBridge({
    check: async () => ({ kind: 'available', version: '0.3.0', manifest: { version: '0.3.0', size: 123, notes: ['a', 5, 'b'] } }),
  });
  const r = await getUpdateBridge().check();
  assert.equal(r.available, true);
  assert.deepEqual(r.manifest, { version: '0.3.0', size: 123, notes: ['a', 'b'] });
});

test('check：三态 latest → {available:false, manifest:null}', async () => {
  const { getUpdateBridge } = await withBridge({ check: async () => ({ kind: 'latest' }) });
  const r = await getUpdateBridge().check();
  assert.deepEqual(r, { available: false, manifest: null });
});

test('check：三态 error → 抛出人话原因（手动检查失败不许静默）', async () => {
  const { getUpdateBridge } = await withBridge({
    check: async () => ({ kind: 'error', message: 'manifest 拉取失败：HTTP 503' }),
  });
  await assert.rejects(getUpdateBridge().check(), /manifest 拉取失败：HTTP 503/);
});

test('check：非法结局（未知 kind）按无新版处理，不炸状态机', async () => {
  const { getUpdateBridge } = await withBridge({ check: async () => ({}) });
  const r = await getUpdateBridge().check();
  assert.deepEqual(r, { available: false, manifest: null });
});

test('onAvailable：归一后的 manifest 才进回调；坏推送（无版本号）丢弃', async () => {
  let sink;
  const { getUpdateBridge } = await withBridge({
    onAvailable: (cb) => { sink = cb; return () => {}; },
  });
  const got = [];
  const off = getUpdateBridge().onAvailable?.((m) => got.push(m));
  assert.equal(typeof off, 'function', '桥上有 onAvailable 时必须返回退订函数');
  sink({ version: '0.3.0', size: 1, notes: ['x'] });
  sink('垃圾');
  sink({ size: 1 });
  sink(null);
  assert.deepEqual(got, [{ version: '0.3.0', size: 1, notes: ['x'] }]);
});
