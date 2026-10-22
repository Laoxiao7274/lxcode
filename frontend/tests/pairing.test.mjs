// 配对二维码载荷的纯函数测试——桌面端两张卡（局域网 / 樱花frp）共用同一份
// 生成逻辑，安卓 parsePairPayload 是对端契约：addr/token 必填、addr 过
// host:port 校验、name 可选。这里钉住载荷形态与 frp 入口挑选规则。
import test from 'node:test';
import assert from 'node:assert/strict';
import { pairPayload, tunnelPairEndpoints } from '../src/shared/pairing.ts';

test('局域网载荷：addr/token/name，name 取地址主机名（与旧版逐字节一致）', () => {
  assert.equal(
    pairPayload('192.168.1.105:7789', 'tok-abc'),
    'lxcode://pair?addr=192.168.1.105%3A7789&token=tok-abc&name=192.168.1.105',
  );
});

test('载荷对特殊字符 URL encode（name 含空格/中文不破坏 URI）', () => {
  assert.equal(
    pairPayload('idea-leaper-1.natfrp.io:20000', 't/1', '樱花frp · idea'),
    'lxcode://pair?addr=idea-leaper-1.natfrp.io%3A20000&token=t%2F1&name=%E6%A8%B1%E8%8A%B1frp+%C2%B7+idea',
  );
});

test('hostFromAddr 回落原样时（name === addr）不带 name 参数（安卓会自动按地址带出）', () => {
  // 地址解析不出主机段时 hostFromAddr 原样返回 → name 与 addr 相同 → 省略
  const payload = pairPayload('weird-addr', 'tok');
  assert.ok(!payload.includes('name='), payload);
  assert.equal(payload, 'lxcode://pair?addr=weird-addr&token=tok');
});

test('frp 入口：只收在线隧道（离线地址扫了也连不上，不放进码里）', () => {
  const eps = tunnelPairEndpoints([
    { addr: 'idea-leaper-1.natfrp.io:20000', online: true },
    { addr: 'hk-1.natfrp.io:20001', online: false },
  ]);
  assert.equal(eps.length, 1);
  assert.equal(eps[0].addr, 'idea-leaper-1.natfrp.io:20000');
  assert.equal(eps[0].label, '樱花frp');
});

test('frp 入口：按 addr 去重、空地址剔除、顺序保持', () => {
  const eps = tunnelPairEndpoints([
    { addr: 'cn-sh-1.natfrp.io:20002', online: true },
    { addr: 'cn-sh-1.natfrp.io:20002', online: true },
    { addr: '  ', online: true },
    { addr: '待分配', online: true },
  ]);
  assert.deepEqual(eps.map((e) => e.addr), ['cn-sh-1.natfrp.io:20002']);
});

test('frp 入口的预填连接名可区分来源（带「樱花frp」标签 + 节点主机名）', () => {
  const [ep] = tunnelPairEndpoints([{ addr: 'idea-leaper-1.natfrp.io:20000', online: true }]);
  assert.ok(ep.name.startsWith('樱花frp · '), ep.name);
  assert.ok(ep.name.includes('idea-leaper-1.natfrp.io'), ep.name);
});

test('frp 公网地址过安卓 addrValid 同款正则（host:port 形态可被对端消费）', () => {
  const ADDR_RE = /^(https?:\/\/)?[a-zA-Z0-9.-]+(:\d{1,5})(\/.*)?$/;
  const [ep] = tunnelPairEndpoints([{ addr: 'idea-leaper-1.natfrp.io:20000', online: true }]);
  assert.ok(ADDR_RE.test(ep.addr), ep.addr);
});

// online 语义收紧（2026-12 三态拆分）：online = 节点端确认在线（服务端可达），
// 不再包含「本机 frpc 在跑但节点未确认」（starting）——starting 隧道的公网地址
// 扫了还连不上，不能进二维码。
test('frp 入口：starting 隧道（running 未 online）不进二维码——online 必须是节点确认态', () => {
  const eps = tunnelPairEndpoints([
    { addr: 'idea-leaper-1.natfrp.io:20000', online: false, running: true, starting: true },
    { addr: 'hk-1.natfrp.io:20001', online: true, running: true, starting: false },
  ]);
  assert.deepEqual(eps.map((e) => e.addr), ['hk-1.natfrp.io:20001']);
});
