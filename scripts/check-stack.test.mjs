// check-stack.mjs 自身的契约测试（同 check-boundaries.test.mjs 的定位：
// 守卫/诊断工具本身也要被测——它给出的是「后端二进制新不新」的判断，
// 判断错了会把人引向错误的排查方向，比没有更糟）。
//
// 运行: node --test scripts/check-stack.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { sameCommit, readBuildInfo, readHead, checkStack } from './check-stack.mjs';

const ROOT = fileURLToPath(new URL('../', import.meta.url));

test('sameCommit：短/长 hash 前缀兼容，任一为空则不判定', () => {
  assert.equal(sameCommit('88a1b94', '88a1b946abcdef0123456789abcdef0123456789'), true);
  assert.equal(sameCommit('88a1b946abcdef0123456789abcdef0123456789', '88a1b94'), true);
  assert.equal(sameCommit('88a1b94', 'bad1bd72'), false);
  // 无法判定：不返回 true 也不返回 false（调用方据此走 unknown 分支）
  assert.equal(sameCommit(null, '88a1b94'), null);
  assert.equal(sameCommit('88a1b94', null), null);
  assert.equal(sameCommit('', ''), null);
});

test('readBuildInfo：非 Go 产物读不出内嵌构建信息（返回 null，不抛异常）', () => {
  // 用仓库里必然存在的非二进制文件——go version -m 会失败，函数须吞掉错误
  assert.equal(readBuildInfo(resolve(ROOT, 'frontend/package.json')), null);
  assert.equal(readBuildInfo(resolve(ROOT, 'no-such-file-anywhere.exe')), null);
});

test('readHead：仓库内取到 40 位 hash，非仓库路径取不到（返回 null）', () => {
  const head = readHead();
  assert.match(head ?? '', /^[0-9a-f]{40}$/);
  // 子目录仍属同一仓库——git rev-parse 会向上找 .git，结果必须一致
  assert.equal(readHead(resolve(ROOT, 'frontend')), head);
  // 不存在的路径 → git 报错 → 吞掉返回 null（不能抛，否则诊断工具本身崩）
  assert.equal(readHead(resolve(ROOT, 'no-such-dir-xyz')), null);
});

test('checkStack：状态取值合法，且带状态自洽的字段', () => {
  const r = checkStack();
  assert.ok(
    ['no-binary', 'unknown', 'ok', 'stale'].includes(r.status),
    `未知状态: ${r.status}`,
  );
  if (r.status === 'no-binary') {
    assert.equal(r.exe, null);
    return;
  }
  // 有二进制：exe 必给；ok/stale 必须给出双方 hash 供人比对
  assert.ok(r.exe, '应给出被检查的二进制路径');
  if (r.status === 'ok' || r.status === 'stale') {
    assert.match(r.info.revision, /^[0-9a-f]{7,40}$/);
    assert.match(r.head, /^[0-9a-f]{40}$/);
    // 状态必须与 hash 比对一致（防止判定分支写反）
    assert.equal(sameCommit(r.info.revision, r.head), r.status === 'ok');
  }
});
