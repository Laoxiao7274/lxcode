// 连接域的纯函数测试——token 遮罩（凭证展示的安全形态）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { maskToken } from '../src/shared/connections.tsx';

test('遮罩：保头尾各 4 位，中间打点', () => {
  assert.equal(maskToken('a3f8b2c1-9d4e-4f2a-8c6b-7e1d5a9f0b3c'), 'a3f8••••0b3c');
});

test('短 token 不遮（<=8 位原样），9 位起遮', () => {
  assert.equal(maskToken('abcd1234'), 'abcd1234');
  assert.equal(maskToken('12345678'), '12345678');
  assert.equal(maskToken('123456789'), '1234••••6789');
});
