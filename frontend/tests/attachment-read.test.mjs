// 附件读入通道（shared/attachment-read.ts）的测试：
// 钉住「读取文件失败: A requested file or directory could not be found…」的
// 修复契约——blob 快照失效（Chromium NotFoundError）时回落 Electron 真实路径
// 重读；浏览器模式与双重失败都给带文件名的人话错误（不带裸英文系统错误）。
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  isNotFoundMessage,
  readBase64With,
  readError,
} from '../src/shared/attachment-read.ts';

const fakeFile = (name = 'a.png') => ({ name });

const deps = (over) => ({
  blob: async () => 'BLOB',
  shell: async () => 'SHELL',
  ...over,
});

test('blob 成功：直接返回，不碰壳回退', async () => {
  let shellCalled = false;
  const r = await readBase64With(fakeFile(), deps({
    blob: async () => 'QUJD',
    shell: async () => { shellCalled = true; return 'SHELL'; },
  }));
  assert.equal(r, 'QUJD');
  assert.equal(shellCalled, false);
});

test('blob 报 NotFoundError、壳回退成功：返回真实路径读到的数据', async () => {
  const r = await readBase64With(fakeFile('截图.png'), deps({
    blob: async () => { throw new Error('A requested file or directory could not be found at the time an operation was processed.'); },
    shell: async () => 'U0hFTEw=',
  }));
  assert.equal(r, 'U0hFTEw=');
});

test('浏览器模式（无壳回退）+ NotFound：错误带文件名与人话指引，不带裸英文', async () => {
  await assert.rejects(
    readBase64With(fakeFile('报告.pdf'), deps({
      blob: async () => { throw new Error('A requested file or directory could not be found at the time an operation was processed.'); },
      shell: null,
    })),
    (e) => {
      assert.ok(e.message.includes('报告.pdf'), `应带文件名: ${e.message}`);
      assert.ok(e.message.includes('保存到本地'), `应带指引: ${e.message}`);
      assert.ok(!e.message.includes('could not be found'), `不应暴露裸英文: ${e.message}`);
      return true;
    },
  );
});

test('双重失败（快照与真实路径都读不到）：错误带上主进程的带路径消息', async () => {
  await assert.rejects(
    readBase64With(fakeFile('a.png'), deps({
      blob: async () => { throw new Error('A requested file or directory could not be found at the time an operation was processed.'); },
      shell: async () => { throw new Error('无法读取 C:\\temp\\a.png：文件不存在或无权访问'); },
    })),
    (e) => {
      assert.ok(e.message.includes('a.png'), `应带文件名: ${e.message}`);
      assert.ok(e.message.includes('C:\\temp\\a.png'), `应带真实路径: ${e.message}`);
      assert.ok(e.message.includes('保存到本地'), `应带指引: ${e.message}`);
      return true;
    },
  );
});

test('非 NotFound 的普通错误：透传原始消息（带文件名）', async () => {
  await assert.rejects(
    readBase64With(fakeFile('a.png'), deps({
      blob: async () => { throw new Error('disk quota exceeded'); },
      shell: null,
    })),
    (e) => e.message === 'a.png：disk quota exceeded',
  );
});

test('readError：NotFound 判定归类正确', () => {
  assert.ok(
    readError(fakeFile('x'), new Error('NotFoundError: file not found'), null).message.includes('保存到本地'),
  );
  assert.ok(
    !readError(fakeFile('x'), new Error('permission denied'), null).message.includes('保存到本地'),
  );
});

test('isNotFoundMessage：中英特征串', () => {
  assert.equal(isNotFoundMessage('A requested file or directory could not be found at the time an operation was processed.'), true);
  assert.equal(isNotFoundMessage('ENOENT: no such file or directory, open ...'), true);
  assert.equal(isNotFoundMessage('系统找不到指定的文件。'), true);
  assert.equal(isNotFoundMessage('permission denied'), false);
});
