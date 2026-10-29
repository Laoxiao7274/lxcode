import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  THEME_KEY, applyTheme, isThemePreference, loadTheme, persistTheme,
  resolveTheme, setTheme, systemScheme, themeAttr,
} from '../src/shared/theme.ts';

const here = dirname(fileURLToPath(import.meta.url));

// ---- 测试环境桩：theme.ts 的 DOM/localStorage 面在 Node 里手工补齐 ----
function installStubs({ stored = null, prefersDark = false } = {}) {
  const attrs = new Map();
  const style = {};
  const store = new Map(stored ? [[THEME_KEY, stored]] : []);
  const root = {
    setAttribute: (k, v) => attrs.set(k, v),
    getAttribute: (k) => attrs.get(k) ?? null,
  };
  globalThis.document = { documentElement: root };
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
  };
  globalThis.window = {
    matchMedia: (q) => ({ matches: q.includes('dark') && prefersDark, addEventListener() {}, removeEventListener() {} }),
  };
  return {
    attrs, style,
    dataTheme: () => attrs.get('data-theme') ?? null,
    storedValue: () => store.get(THEME_KEY) ?? null,
  };
}

test('resolveTheme：跟随系统取系统亮暗，显式偏好直取', () => {
  assert.equal(resolveTheme('system', 'dark'), 'dark');
  assert.equal(resolveTheme('system', 'light'), 'light');
  assert.equal(resolveTheme('dark', 'light'), 'dark');
  assert.equal(resolveTheme('light', 'dark'), 'light');
});

test('themeAttr：system 映射为 auto（CSS 媒体查询块认它），dark/light 原样', () => {
  assert.equal(themeAttr('system'), 'auto');
  assert.equal(themeAttr('dark'), 'dark');
  assert.equal(themeAttr('light'), 'light');
});

test('systemScheme：无 matchMedia 回落 light（与 :root 默认 token 一致）', () => {
  const saved = globalThis.window;
  globalThis.window = undefined;
  assert.equal(systemScheme(), 'light');
  globalThis.window = saved;
  assert.equal(systemScheme(), 'light');
  const s = installStubs({ prefersDark: true });
  assert.equal(systemScheme(), 'dark');
  assert.equal(s.dataTheme(), null); // 只读不落属性
});

test('isThemePreference 只认三态值', () => {
  for (const v of ['system', 'dark', 'light']) assert.equal(isThemePreference(v), true);
  for (const v of ['auto', 'SYSTEM', '', null, undefined, 1]) assert.equal(isThemePreference(v), false);
});

test('applyTheme 落 html data-theme；setTheme 同时持久化到 THEME_KEY', () => {
  const s = installStubs({ prefersDark: true });
  assert.equal(applyTheme('dark'), 'dark');
  assert.equal(s.dataTheme(), 'dark');
  assert.equal(applyTheme('light'), 'light');
  assert.equal(s.dataTheme(), 'light');
  // system：暗色系统 → 实际 dark
  assert.equal(applyTheme('system'), 'dark');
  assert.equal(s.dataTheme(), 'auto');

  assert.equal(setTheme('light'), 'light');
  assert.equal(s.dataTheme(), 'light');
  assert.equal(s.storedValue(), 'light');
  assert.equal(setTheme('system'), 'dark');
  assert.equal(s.storedValue(), 'system');
});

test('loadTheme：持久化值可读回，值不认识/无记录返回 null', () => {
  installStubs({ stored: 'dark' });
  assert.equal(loadTheme(), 'dark');
  installStubs({ stored: 'auto' }); // 旧值域（attr 名）不是合法偏好
  assert.equal(loadTheme(), null);
  installStubs({});
  assert.equal(loadTheme(), null);
});

test('persistTheme 按固定键写 localStorage（index.html 内联脚本读同一个键）', () => {
  const s = installStubs({});
  persistTheme('dark');
  assert.equal(s.storedValue(), 'dark');
});

// ---- index.html 防闪内联脚本：首帧前定 data-theme 与背景兜底 ----
const indexHtml = readFileSync(join(here, '..', 'index.html'), 'utf8');

test('index.html 内联脚本在首帧前应用主题（键名/属性/系统判定/背景兜底俱全）', () => {
  const script = indexHtml.slice(indexHtml.indexOf('<script>'), indexHtml.indexOf('</script>') + 9);
  assert.ok(script.includes(THEME_KEY), '读同一个持久化键');
  assert.ok(script.includes('data-theme'), '落 html 根属性');
  assert.ok(script.includes('prefers-color-scheme'), '跟随系统走 matchMedia');
  assert.ok(script.includes('"#18181c"'), '暗色兜底背景与暗 token --bg 同值');
  assert.ok(script.includes('"#ffffff"'), '亮色兜底背景与亮 token --bg 同值');
  assert.ok(script.includes('try'), 'localStorage 异常静默回落');
  // 内联脚本必须在模块入口之前（首帧前执行）
  assert.ok(indexHtml.indexOf('<script>') < indexHtml.indexOf('<script type="module"'), '内联脚本先于应用入口');
});

// ---- CSS 侧：token 双套与三态选择器 ----
const baseCss = readFileSync(join(here, '..', 'src', 'styles', 'base.css'), 'utf8');

test('base.css：暗色 token 双套（dark 直取 + auto 媒体查询），:root 亮色值未动', () => {
  assert.ok(baseCss.includes('html[data-theme="dark"]'), '深色直取覆盖块');
  assert.ok(/@media \(prefers-color-scheme: dark\)\s*\{\s*html\[data-theme="auto"\]/.test(baseCss), 'auto 走 prefers-color-scheme');
  assert.ok(baseCss.includes('color-scheme: dark'), '暗色下 color-scheme 同步');
  assert.ok(baseCss.includes('color-scheme: light'), '亮色默认 color-scheme');
  // 亮暗两套的同一 token 必须给出不同值（覆盖真实存在）
  const lightBg = baseCss.match(/--bg: (#\w+);/)[1];
  assert.equal(lightBg, '#ffffff');
  assert.ok(baseCss.includes('--bg: #18181c;'), '暗色 --bg 成套给出');
  // 新增派生 token 在亮色 :root 里有定义（归并点）
  for (const t of ['--code-bg', '--on-accent', '--ok-text', '--window-outer', '--bg-inset', '--hover-veil']) {
    assert.ok(baseCss.includes(`${t}:`), `token ${t} 已定义`);
  }
});

// ---- aicss：AI 内容卡跟随三态（白字看不清的根因修复点） ----
for (const mod of ['TextResponse', 'TodoList', 'ThinkingReasoning']) {
  test(`aicss/${mod}：媒体查询暗色分支覆盖 data-theme="auto"`, () => {
    const css = readFileSync(join(here, '..', 'src', 'aicss', `${mod}.module.css`), 'utf8');
    assert.ok(/@media \(prefers-color-scheme: dark\)\s*\{[\s\S]*?\[data-theme="auto"\]/.test(css));
  });
}

test('aicss/ApprovalCard：OS 暗色翻暗时排除强制浅色（不出现暗卡贴亮 UI）', () => {
  const css = readFileSync(join(here, '..', 'src', 'aicss', 'ApprovalCard.module.css'), 'utf8');
  assert.ok(css.includes(':global(html:not([data-theme="light"])) .card'));
});

// ---- 设置面板：三态选项接线（切档即时落 DOM + 持久化） ----
const settingsSrc = readFileSync(join(here, '..', 'src', 'shared', 'settings.tsx'), 'utf8');
const panelSrc = readFileSync(join(here, '..', 'src', 'components', 'settings', 'SettingsPanel.tsx'), 'utf8');

test('settings：默认跟随系统；set({theme}) 走 setTheme（DOM+持久化+壳同步一个口）', () => {
  assert.ok(/theme:\s*"system"/.test(settingsSrc), 'DEFAULTS.theme = system');
  assert.ok(settingsSrc.includes('setTheme(supported.theme)'), 'theme 变更调 setTheme');
  assert.ok(settingsSrc.includes('initTheme()'), '挂载时恢复持久化偏好');
  assert.ok(panelSrc.includes('value: "dark"'), '三态选项含深色');
  assert.ok(panelSrc.includes('value: "light"'), '三态选项含浅色');
  assert.ok(panelSrc.includes('"system"'), '三态选项含跟随系统');
});

// ---- Electron 壳：窗口底色与 nativeTheme 同步 ----
const mainSrc = readFileSync(join(here, '..', '..', 'shell', 'src', 'main.ts'), 'utf8');
const preloadSrc = readFileSync(join(here, '..', '..', 'shell', 'src', 'preload.ts'), 'utf8');

test('shell：backgroundColor 按系统亮暗取基色；theme:prefer 同步 nativeTheme', () => {
  assert.ok(mainSrc.includes('nativeTheme.shouldUseDarkColors'), '窗口底色跟随当前系统亮暗');
  assert.ok(mainSrc.includes('nativeTheme.themeSource'), '偏好同步到 nativeTheme');
  assert.ok(mainSrc.includes('"theme:prefer"'), 'IPC 通道注册');
  assert.ok(preloadSrc.includes('setThemePreference'), 'preload 桥暴露主题偏好入口');
});
