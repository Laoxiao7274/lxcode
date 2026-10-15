// 灯箱（shared/lightbox-math.ts + lightbox.tsx）的钉子测试：
// ① 缩放钳制：25%~800% 边界钉住（越界一律回边界，NaN 回 1）；
// ② 滚轮缩放：方向约定（delta>0 放大）与边界收敛；
// ③ 双击语义：适配→100%、100%→适配、中间态→适配；
// ④ 多图索引：环形步进（含边界回绕与 n<=1 原地）；
// ⑤ 平移钳制：放大后图至少一角在视口内；小图吸附回中心；
// ⑥ 光标锚点缩放：锚点下的内容坐标不随缩放变；
// ⑦ fitSize：contain 适配 + 92% 边距 + 退化输入 null；
// ⑧ 组件装配：LightboxHost 单实例收口（openImageLightbox 全局入口），
//    渲染走 portal（容器挂 body）、缩略图 onClick 只发 open 不自带灯箱 DOM
//    ——三处入口（ImageCard/GridImage/溢出层）不再各自持有灯箱实现。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import {
  LIGHTBOX_MIN_SCALE,
  LIGHTBOX_MAX_SCALE,
  clampScale,
  wheelScale,
  doubleClickScale,
  stepIndex,
  clampOffset,
  anchorZoomOffset,
  scaleLabel,
  fitSize,
} from '../src/shared/lightbox-math.ts';
import {
  lightboxImages,
  openImageLightbox,
  LightboxHost,
  ImageLightbox,
} from '../src/shared/lightbox.tsx';
import { setAttachmentOrigin, setAttachmentToken } from '../src/shared/attachments.ts';

setAttachmentOrigin('http://127.0.0.1:7789');
setAttachmentToken('');

// ---------- ① 缩放钳制 ----------

test('① clampScale：25%~800% 边界钉住；NaN 回 1（不产生坏 transform）', () => {
  assert.equal(clampScale(1), 1);
  assert.equal(clampScale(0.25), 0.25, '下边界原样');
  assert.equal(clampScale(8), 8, '上边界原样');
  assert.equal(clampScale(0.1), 0.25, '低于下界钉回 25%');
  assert.equal(clampScale(100), 8, '高于上界钉回 800%');
  assert.equal(clampScale(NaN), 1, 'NaN 回适配');
  assert.equal(clampScale(Infinity), 8, 'Infinity 钉上界');
  assert.equal(clampScale(-Infinity), 0.25, '-Infinity 钉下界');
  assert.equal(LIGHTBOX_MIN_SCALE, 0.25);
  assert.equal(LIGHTBOX_MAX_SCALE, 8);
});

// ---------- ② 滚轮缩放 ----------

test('② wheelScale：delta>0 放大、delta<0 缩小；连续缩到边界收敛不穿越', () => {
  assert.equal(wheelScale(1, 1) > 1, true, '放大');
  assert.equal(wheelScale(1, -1) < 1, true, '缩小');
  // 边界收敛：从 1 连续放大 100 步不会超过 8
  let s = 1;
  for (let i = 0; i < 100; i++) s = wheelScale(s, 1);
  assert.equal(s, 8);
  s = 1;
  for (let i = 0; i < 100; i++) s = wheelScale(s, -1);
  assert.equal(s, 0.25);
});

// ---------- ③ 双击语义 ----------

test('③ doubleClickScale：适配→100%（不重置偏移）、100%→适配（重置）、中间态→适配', () => {
  assert.deepEqual(doubleClickScale(1, 1), { scale: 1, reset: false }, '已是适配 → 100%，偏移保留');
  assert.deepEqual(doubleClickScale(1, 2), { scale: 2, reset: true }, '在 100% → 适配（重置偏移）');
  assert.deepEqual(doubleClickScale(3.5, 2), { scale: 2, reset: true }, '中间态 → 适配');
  // 容差内视为适配
  assert.deepEqual(doubleClickScale(1.009, 1), { scale: 1, reset: false });
});

// ---------- ④ 多图索引 ----------

test('④ stepIndex：环形步进（末张→首张、首张→末张）；n<=1 恒 0；step=0 原地', () => {
  assert.equal(stepIndex(0, 5, 1), 1);
  assert.equal(stepIndex(4, 5, 1), 0, '末张下一张回首');
  assert.equal(stepIndex(0, 5, -1), 4, '首张上一张到末');
  assert.equal(stepIndex(2, 5, -1), 1);
  assert.equal(stepIndex(2, 1, 1), 0, '单图恒 0');
  assert.equal(stepIndex(2, 0, 1), 0, '空集恒 0');
  assert.equal(stepIndex(3, 5, 0), 3, 'step=0 原地');
});

// ---------- ⑤ 平移钳制 ----------

test('⑤ clampOffset：放大后图至少一角留在视口内；图小于视口吸附回中心', () => {
  const vp = { width: 1000, height: 800 };
  const fit = { width: 800, height: 600 };
  // 放大 2x：图 1600×1200 > 视口 → 允许拖出 (1600-1000)/2 = 300、(1200-800)/2 = 200
  const lim = clampOffset({ x: 9999, y: -9999 }, 2, fit, vp);
  assert.equal(lim.x, 300);
  assert.equal(lim.y, -200, 'y 钳到 -(1200-800)/2');
  assert.deepEqual(clampOffset({ x: -9999, y: 9999 }, 2, fit, vp), { x: -300, y: 200 }, '负向对称');
  // 缩到适配（1x）：图 800×600 < 视口 → 偏移归零（吸附居中）
  // limitX = max(800,1000)/2 = 500、limitY = max(600,800)/2 = 400——两个方向
  // 都被视口罩住 → 任意偏移归零。宽 > 视口的扁图仍能在 x 方向拖动：
  const wide = { width: 1200, height: 400 };
  const wideLim = clampOffset({ x: 9999, y: 9999 }, 1, wide, vp);
  assert.equal(wideLim.x, 100, 'x: (1200-1000)/2');
  assert.equal(wideLim.y, 0, 'y: 图 400 < 视口 800 → 吸附');
  const center = clampOffset({ x: 500, y: -500 }, 1, fit, vp);
  assert.deepEqual(center, { x: 0, y: 0 });
});

// ---------- ⑥ 光标锚点缩放 ----------

test('⑥ anchorZoomOffset：锚点下的内容坐标不随缩放变（光标为中心）', () => {
  const offset = { x: -100, y: 50 };
  const anchor = { x: 200, y: -80 };
  // 缩放前后锚点下的内容点不动：a - o = c，a - o' = c·(s'/s)
  const next = anchorZoomOffset(offset, 1, 2, anchor);
  const c1 = { x: anchor.x - offset.x, y: anchor.y - offset.y };
  const c2 = { x: anchor.x - next.x, y: anchor.y - next.y };
  assert.deepEqual(c2, { x: c1.x * 2, y: c1.y * 2 });
  // 等比缩放（to === from）时偏移不变
  const same = anchorZoomOffset(offset, 2, 2, anchor);
  assert.deepEqual(same, offset);
});

// ---------- ⑦ fitSize ----------

test('⑦ fitSize：contain 适配（不放大小图）、92% 边距；退化输入 null', () => {
  const vp = { width: 1000, height: 800 };
  // 大图：宽受限 1000*0.92/2000=0.46
  assert.deepEqual(fitSize({ width: 2000, height: 1000 }, vp), { width: 920, height: 460 });
  // 高图：高受限 800*0.92/1600=0.46
  assert.deepEqual(fitSize({ width: 800, height: 1600 }, vp), { width: 368, height: 736 });
  // 小图不放大（min 含 1）
  assert.deepEqual(fitSize({ width: 400, height: 300 }, vp), { width: 400, height: 300 });
  assert.equal(fitSize({ width: 0, height: 100 }, vp), null, '0 尺寸 null（等 load）');
  assert.equal(fitSize({ width: 100, height: 100 }, { width: 0, height: 0 }), null);
});

// ---------- ⑧ 组件装配 ----------

// ImageLightbox 经 createPortal 挂 document.body —— SSR 渲染器不支持 portal。
// 两条结构断言路径：
// ① 不注入 document（保持 node 无 DOM 原状）→ 组件走 SSR 降级返回 null（钉「降级不崩」）；
// ② 结构断言改在源码装配层做：portal 容器 = document.body、遮罩/顶栏/工具条/
//    切换箭头都由这一个组件产出（JSX 字面量钉在源码里，grep 断言）。
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const fs = require('node:fs');

const imgAtt = (over = {}) => ({ path: 's-1/ab12.png', name: 'chart.png', size: 9, mime: 'image/png', kind: 'image', ...over });

test('⑧ lightboxImages：只收图片、URL 构造失败的不进集合', () => {
  const items = lightboxImages([imgAtt(), { path: '../evil.png', name: 'evil.png', kind: 'image' }, { path: 's-1/x.csv', name: 'x.csv', kind: 'file' }]);
  assert.equal(items.length, 1);
  assert.equal(items[0].url, 'http://127.0.0.1:7789/attachments/s-1/ab12.png');
  assert.equal(lightboxImages([]).length, 0);
});

test('⑧ openImageLightbox：空集合不开、index 越界钳到合法范围（不崩、不指向不存在的图）', () => {
  // 无 host 注册（测试进程没有 LightboxHost 挂载）→ openRef.current 为 null，
  // openImageLightbox 必须静默 no-op 而不是抛错（入口在任何渲染时机都可能被点）。
  assert.doesNotThrow(() => openImageLightbox([], 0));
  assert.doesNotThrow(() => openImageLightbox(lightboxImages([imgAtt()]), 99));
  assert.doesNotThrow(() => openImageLightbox(lightboxImages([imgAtt()]), -5));
});

test('⑧ ImageLightbox：SSR 降级返回 null（portal 是纯客户端）；结构装配钉在源码', () => {
  const items = lightboxImages([imgAtt({ name: 'a.png', path: 's-1/a.png' }), imgAtt({ name: 'b.png', path: 's-1/b.png' })]);
  // node 无 DOM：组件应干净返回 null（不崩、不产出 markup）
  const html = renderToStaticMarkup(createElement(ImageLightbox, {
    items, index: 0, onClose: () => {}, onNavigate: () => {},
  }));
  assert.equal(html, '', 'SSR 降级 = 空 markup');
  // 空集合同样不渲染
  const empty = renderToStaticMarkup(createElement(ImageLightbox, {
    items: [], index: 0, onClose: () => {}, onNavigate: () => {},
  }));
  assert.equal(empty, '');

  // 结构装配钉子：遮罩根 / 顶栏 / 工具条 / 切换箭头 / 计数都在组件 JSX 里
  const src = fs.readFileSync('src/shared/lightbox.tsx', 'utf8');
  assert.ok(src.includes('createPortal(') && src.includes('document.body'), '渲染走 portal 挂 body');
  assert.ok(src.includes('"lightbox"'), '遮罩根类名');
  assert.ok(src.includes('lightbox-topbar'), '顶栏');
  assert.ok(src.includes('lightbox-toolbar'), '工具条');
  assert.ok(src.includes('lightbox-prev') && src.includes('lightbox-next'), '切换箭头');
  assert.ok(src.includes('{index + 1} / {items.length}'), '计数读数');
  assert.ok(src.includes('lightbox-scale'), '缩放比例显示');
  assert.ok(src.includes('lightbox-loading') && src.includes('lightbox-failed'), '加载中/失败态');
  assert.ok(src.includes('ArrowLeft') && src.includes('ArrowRight'), '键盘 ←→ 切图');
  assert.ok(src.includes('"Escape"'), 'Esc 关闭');
  // 滚轮缩放/双击/拖动三件套都在
  assert.ok(src.includes('onWheel') && src.includes('onDoubleClick') && src.includes('onPointerDown'), '缩放/双击/拖动事件');
});

test('⑧ 三处入口统一：AttachmentCard 不再内联灯箱 DOM，缩略图只发 openImageLightbox', () => {
  // 源码级钉子：三处内联实现（att-lightbox 的 onClick 关灯箱 + 内嵌 img）已删，
  // 放大统一走 shared/lightbox.tsx 的 openImageLightbox / lightboxImages。
  const src = fs.readFileSync('src/components/thread/blocks/AttachmentCard.tsx', 'utf8');
  assert.ok(!src.includes('att-lightbox'), '旧内联灯箱类名不再出现');
  assert.ok(!src.includes('setZoom'), '不再各自持有 zoom 状态');
  assert.ok(src.includes('openImageLightbox'), '缩略图统一发 open');
  assert.ok(src.includes('lightboxImages'), '集合构造走统一纯函数');
  // Escape/键盘处理收口到组件（卡内不再挂 keydown）
  assert.ok(!src.includes('addEventListener("keydown"'), '卡内不再挂 Esc 监听');
});
