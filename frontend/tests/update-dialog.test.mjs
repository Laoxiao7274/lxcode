// update-dialog.test.mjs —— 统一更新弹窗的钉子。
//
// 2026-12 用户拍板：发现新版本的两条路径（① 启动/手动检查，② 壳主进程周期
// 检查推送 update:available）都引导进**同一个弹窗**，不再有 Toast 与设置页
// 内联面板两套形态。本文件钉两件事：
//   1. 纯展示口径（文案/进度/可关性）由 update-dialog-model.ts 单点给出——
//      总量未知不编分母、校验阶段不说「卡住」、安装中不可关；
//   2. 两条路径确实调用同一个状态（source 级断言：两处都 setDialogOpen(true)），
//      且旧的 Toast 组件已不存在（不许复活成第二套提示形态）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import {
  bytesText, canClose, closeHint, dialogTitle, percentOf, retryLabel, stageText,
} from '../src/components/update/update-dialog-model.ts';

const read = (rel) => readFileSync(join(fileURLToPath(new URL('../', import.meta.url)), rel), 'utf8');

test('dialogTitle：每个阶段一句，不共用模糊文案', () => {
  assert.equal(dialogTitle('checking'), '正在检查更新');
  assert.equal(dialogTitle('latest'), '已是最新版本');
  assert.equal(dialogTitle('available'), '发现新版本');
  assert.equal(dialogTitle('downloading'), '正在下载更新');
  assert.equal(dialogTitle('ready'), '更新已就绪');
  assert.equal(dialogTitle('installing'), '正在安装更新');
});

test('stageText：校验阶段明确说在忙（进度 100% 不能看起来像卡死）', () => {
  assert.match(stageText('downloading', 42), /正在下载/);
  assert.match(stageText('verifying', 100), /校验/);
  assert.match(stageText('downloading', 100), /校验/, '进度满格但还没完成下载时也要说明在校验');
});

test('bytesText：总量未知不编分母', () => {
  assert.equal(bytesText(0, 0), '');
  assert.equal(bytesText(2048, 0), '2.0 KB', '只给已下载，不编总量');
  assert.equal(bytesText(512, 1024), '512 B / 1.0 KB');
  assert.equal(bytesText(4096, 1024), '1.0 KB / 1.0 KB', '已下载超过总量时钳到总量');
});

test('percentOf：有总量按字节算，无总量用上报值；都钳到 0-100', () => {
  assert.equal(percentOf(50, 100, 0), 50);
  assert.equal(percentOf(0, 0, 37), 37);
  assert.equal(percentOf(0, 0, 0), 0);
  assert.equal(percentOf(200, 100, 0), 100);
});

test('canClose：安装排程后不可关（应用即将重启）；下载中可关（后台继续）', () => {
  assert.equal(canClose('installing'), false);
  assert.equal(canClose('downloading'), true);
  assert.equal(canClose('ready'), true);
  assert.equal(canClose('available'), true);
});

test('closeHint：下载中关闭 ≠ 取消，要写明后台继续', () => {
  assert.match(closeHint('downloading'), /不会中断下载/);
  assert.match(closeHint('ready'), /稍后安装/);
  assert.match(closeHint('available'), /稍后更新/);
});

test('retryLabel：按失败步骤给对应重试文案', () => {
  assert.equal(retryLabel('download'), '重新下载');
  assert.equal(retryLabel('install'), '重新安装');
  assert.equal(retryLabel('check'), '重试');
  assert.equal(retryLabel(null), '重试');
});

test('两条路径收敛到同一弹窗：都置 dialogOpen（source 级断言）', () => {
  const src = read('src/shared/update.tsx');
  // ① 启动/手动检查：check() 发现 available 后开弹窗
  const checkBlock = src.slice(src.indexOf('const check = useCallback'), src.indexOf('const download = useCallback'));
  assert.ok(checkBlock.includes('setDialogOpen(true)'), '路径①（检查发现新版本）必须打开同一弹窗');
  // ② 周期检查推送：onAvailable 回调里开弹窗
  const availBlock = src.slice(src.indexOf('bridge.onAvailable'), src.indexOf('const value = useMemo'));
  assert.ok(availBlock.includes('setDialogOpen(true)'), '路径②（update:available 推送）必须打开同一弹窗');
  // 只有一个弹窗宿主
  assert.ok(src.includes('const [dialogOpen, setDialogOpen] = useState(false)'), '弹窗开合是单点状态');
});

test('旧的右下角 Toast 已删除（不允许两套提示形态）', () => {
  assert.equal(existsSync(join(fileURLToPath(new URL('../', import.meta.url)), 'src/components/update/UpdateToast.tsx')), false);
  const app = read('src/App.tsx');
  assert.ok(app.includes('UpdateDialog'), 'App 必须挂载统一更新弹窗');
  assert.ok(!app.includes('UpdateToast'), 'App 不得再挂载 Toast');
});
