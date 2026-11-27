// A2 斜杠命令补齐 + 助手域斜杠面板：
//  ① 命令集源码钉子（主域 App：新命令 + 信号机制；/help 复用 op-notice）；
//  ② 选择器 openSignal 的纯逻辑钉子（ModelPicker/PermPicker/JobsBadge 监听）；
//  ③ 助手域 AssistantInput 的斜杠面板接线（SlashPalette 复用）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const here = new URL('.', import.meta.url);
const read = (p) => readFileSync(new URL(p, here), 'utf8');

const appSrc = read('../src/App.tsx');
const composerSrc = read('../src/components/composer/Composer.tsx');
const modelPickerSrc = read('../src/components/model-picker/ModelPicker.tsx');
const permPickerSrc = read('../src/components/perm-picker/PermPicker.tsx');
const jobsBadgeSrc = read('../src/components/jobs/JobsBadge.tsx');
const topbarSrc = read('../src/components/topbar/Topbar.tsx');
const asstInputSrc = read('../src/components/assistant/AssistantInput.tsx');
const asstPageSrc = read('../src/components/assistant/AssistantPage.tsx');
const slashHelpSrc = read('../src/shared/slash-help.ts');

// ---------- ① 主域命令集 ----------

test('App：命令集补 /model /effort /approval /jobs /help', () => {
  for (const name of ['model', 'effort', 'approval', 'jobs', 'help']) {
    assert.match(appSrc, new RegExp(`name: "${name}"`), `/${name} 应在命令集里`);
  }
  // 实现 = 打开对应 picker（信号驱动），不是直接 RPC——会话级状态走 UI 入口
  assert.match(appSrc, /bumpSignal\("model"\)/);
  assert.match(appSrc, /bumpSignal\("jobs"\)/);
});

test('App：/help 清单走 op-notice 形态 + 自动消失', () => {
  assert.match(appSrc, /SLASH_HELP_TEXT/);
  assert.match(appSrc, /op-notice slash-help/);
  assert.match(appSrc, /setTimeout\(\(\) => setHelpText\(""\), 12000\)/);
});

test('信号链路：App → Topbar(jobsSignal) → JobsBadge；App → Composer(model/effort/approvalSignal)', () => {
  assert.match(topbarSrc, /openSignal=\{jobsSignal\}/);
  assert.match(jobsBadgeSrc, /openSignal > 0 && openSignal !== prevSignal\.current/);
  assert.match(composerSrc, /openSignal=\{approvalSignal\}/);
  assert.match(composerSrc, /openSignal=\{modelSignal \|\| effortSignal\}/);
});

test('ModelPicker：信号打开落 initialPanel（/effort 直落强度面板）', () => {
  assert.match(modelPickerSrc, /initialPanel = "model"/);
  assert.match(modelPickerSrc, /setPanel\(initialPanel\)/);
  assert.match(modelPickerSrc, /openSignal > 0 && openSignal !== prevSignal\.current/);
});

test('slash-help：两条域清单都存在且命令齐全', () => {
  assert.match(slashHelpSrc, /SLASH_HELP_TEXT/);
  assert.match(slashHelpSrc, /SLASH_HELP_TEXT_ASSISTANT/);
  assert.match(slashHelpSrc, /\/model/);
});

// ---------- ② 助手域 ----------

test('AssistantInput：复用 SlashPalette（/ 开头单行开面板 + 键盘接管）', () => {
  assert.match(asstInputSrc, /<SlashPalette/);
  assert.match(asstInputSrc, /text\.startsWith\("\/"\)/);
  assert.match(asstInputSrc, /if \(slashOpen\) return;/, '面板开着时 onKey 不吃 Enter');
});

test('AssistantPage：注入助手域命令集（/compact /model /effort /approval /help）', () => {
  for (const name of ['compact', 'model', 'effort', 'approval', 'help']) {
    assert.match(asstPageSrc, new RegExp(`name: "${name}"`), `/${name} 应在助手命令集里`);
  }
  // 域隔离：助手命令集没有页面导航命令（agents/catalog/settings/new 不进）
  assert.doesNotMatch(asstPageSrc, /name: "agents"/);
  // /compact 走 handleCompact（assistant.compact 经 useCompaction 的 compact 注入）
  assert.match(asstPageSrc, /run: \(\) => \{ void handleCompact\(\); \}/);
  // 信号透传给 AssistantInput 的 pickers
  assert.match(asstPageSrc, /modelSignal=\{pickerSignals\.model\}/);
});
