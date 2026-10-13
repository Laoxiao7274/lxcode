// gen-site-favicon.cjs —— 从 site/public/favicon.svg 生成 site/public/favicon.ico
//
// 背景：浏览器默认请求 /favicon.ico，站点只有 favicon.svg 时网关返回 404。
// 本机没有 ImageMagick/sharp，SVG 又是纯几何图形（圆角方底 + 两条折线笔画），
// 于是按源码语义手绘位图（PNG 载荷）包进 ICO 容器——零依赖。
//
// 已知局限：SVG 解析只覆盖源文件实际用到的特性——rect(rx/fill)、
// path 的 M/L/H/V（含小写相对）+ stroke/width/linecap；源 SVG 改用
// 曲线/渐变/多子路径后此脚本需同步扩展，否则报错退出（不会静默出错图）。
//
// 用法：node scripts/gen-site-favicon.cjs
// 产物：site/public/favicon.ico（下次 vite build 会随 public/ 原样拷进 dist/）
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const zlib = require("node:zlib");

const ROOT = path.join(__dirname, "..");
const SVG_PATH = path.join(ROOT, "site/public/favicon.svg");
const OUT_PATH = path.join(ROOT, "site/public/favicon.ico");
const DRAW = 256;          // 超采样画布（viewBox 0..64 × 4）
const OUT = 32;            // 降采样后尺寸
const SCALE = DRAW / 64;

// --- 1. 解析 SVG（只覆盖源文件用到的特性） ---
const svg = fs.readFileSync(SVG_PATH, "utf8");
const rx = Number((svg.match(/rx="([\d.]+)"/) || [])[1] ?? 0);
const rectFill = (svg.match(/<rect[^>]*fill="([^"]+)"/) || [])[1] ?? "#0d0d0d";

function parsePolyline(d) {
  const tokens = d.match(/[MmLlHhVv]|-?\d+(?:\.\d+)?|-?\.\d+/g) || [];
  const pts = [];
  let cx = 0, cy = 0, i = 0;
  const num = () => Number(tokens[i++]);
  const isNum = (t) => t !== undefined && !isNaN(Number(t));
  let cmd = "";       // 当前命令（含隐式延续）
  let lastCmd = "";   // M/L 的隐式延续按此命令解释
  while (i < tokens.length) {
    if (isNum(tokens[i])) {
      // 数字：隐式延续上一 M/L（SVG 规范：M 后续坐标对 = 隐式 L）
      if (lastCmd === "M" || lastCmd === "m") cmd = "L";
      else if (lastCmd === "L" || lastCmd === "l") cmd = lastCmd;
      else throw new Error(`路径命令后出现意外数字: ${tokens[i]}`);
    } else {
      cmd = tokens[i++];
      lastCmd = cmd;
    }
    if (cmd === "M") { cx = num(); cy = num(); }
    else if (cmd === "m") { cx += num(); cy += num(); }
    else if (cmd === "L") { cx = num(); cy = num(); }
    else if (cmd === "l") { cx += num(); cy += num(); }
    else if (cmd === "H") { cx = num(); }
    else if (cmd === "h") { cx += num(); }
    else if (cmd === "V") { cy = num(); }
    else if (cmd === "v") { cy += num(); }
    else throw new Error(`不支持的 SVG 路径命令: ${cmd}（脚本只覆盖 M/L/H/V 及小写相对）`);
    pts.push([cx, cy]);
  }
  return pts;
}

const strokes = [];
for (const m of svg.matchAll(/<path\b[^>]*\/>/gs)) {
  const tag = m[0];
  const d = (tag.match(/d="([^"]+)"/) || [])[1] ?? "";
  const stroke = (tag.match(/stroke="([^"]+)"/) || [])[1] ?? "#000000";
  const width = Number((tag.match(/stroke-width="([\d.]+)"/) || [])[1] ?? 1);
  const caps = (tag.match(/stroke-linecap="([^"]+)"/) || [])[1] ?? "butt";
  strokes.push({ pts: parsePolyline(d).map(([x, y]) => [x * SCALE, y * SCALE]), color: stroke, width, caps });
}

function hexToRgb(hex) {
  const h = hex.replace("#", "");
  const f = h.length === 3 ? h.split("").map((c) => c + c).join("") : h;
  return [parseInt(f.slice(0, 2), 16), parseInt(f.slice(2, 4), 16), parseInt(f.slice(4, 6), 16)];
}

// --- 2. 绘制（覆盖度 + 颜色，后画覆盖先画；SDF 0.5px 过渡带抗锯齿） ---
const cov = new Float64Array(DRAW * DRAW);
const col = new Float64Array(DRAW * DRAW * 3);

function paint(x, y, a, c) {
  if (x < 0 || y < 0 || x >= DRAW || y >= DRAW || a <= 0) return;
  const i = y * DRAW + x;
  if (a >= cov[i] || cov[i] === 0) {
    col[i * 3] = c[0]; col[i * 3 + 1] = c[1]; col[i * 3 + 2] = c[2];
    cov[i] = Math.max(cov[i], a);
  }
}

// 圆角矩形
{
  const c = hexToRgb(rectFill);
  const r = rx * SCALE;
  const half = DRAW / 2;
  for (let y = 0; y < DRAW; y++) {
    for (let x = 0; x < DRAW; x++) {
      const dx = Math.max(Math.abs(x + 0.5 - half) - (half - r), 0);
      const dy = Math.max(Math.abs(y + 0.5 - half) - (half - r), 0);
      const d = Math.sqrt(dx * dx + dy * dy) - r;
      paint(x, y, Math.min(1, Math.max(0, 0.5 - d)), c);
    }
  }
}

function segDist(px, py, a, b) {
  const vx = b[0] - a[0], vy = b[1] - a[1];
  const wx = px - a[0], wy = py - a[1];
  const t = Math.max(0, Math.min(1, (wx * vx + wy * vy) / (vx * vx + vy * vy || 1)));
  const dx = px - (a[0] + t * vx), dy = py - (a[1] + t * vy);
  return Math.sqrt(dx * dx + dy * dy);
}

// 笔画（square 端帽外延半宽）
for (const s of strokes) {
  const c = hexToRgb(s.color);
  const halfW = (s.width * SCALE) / 2;
  const ext = s.caps === "square" ? halfW : 0;
  const segs = [];
  for (let i = 0; i < s.pts.length - 1; i++) segs.push([s.pts[i], s.pts[i + 1]]);
  for (let y = 0; y < DRAW; y++) {
    for (let x = 0; x < DRAW; x++) {
      const px = x + 0.5, py = y + 0.5;
      let d = Infinity;
      for (const [a, b] of segs) d = Math.min(d, segDist(px, py, a, b));
      for (const p of [s.pts[0], s.pts[s.pts.length - 1]]) {
        const dx = px - p[0], dy = py - p[1];
        d = Math.min(d, Math.sqrt(dx * dx + dy * dy) - ext);
      }
      paint(x, y, Math.min(1, Math.max(0, 0.5 + halfW - d)), c);
    }
  }
}

// --- 3. 降采样 DRAW→32（4x4 求平均，alpha = 平均覆盖度） ---
const rgba = Buffer.alloc(OUT * OUT * 4);
const STEP = DRAW / OUT;
for (let y = 0; y < OUT; y++) {
  for (let x = 0; x < OUT; x++) {
    let r = 0, g = 0, b = 0, csum = 0, n = 0;
    for (let sy = Math.floor(y * STEP); sy < Math.floor((y + 1) * STEP); sy++) {
      for (let sx = Math.floor(x * STEP); sx < Math.floor((x + 1) * STEP); sx++) {
        const i = sy * DRAW + sx;
        const a = cov[i];
        r += col[i * 3] * a; g += col[i * 3 + 1] * a; b += col[i * 3 + 2] * a;
        csum += a; n++;
      }
    }
    const o = (y * OUT + x) * 4;
    if (csum > 0) {
      rgba[o] = Math.round(r / csum);
      rgba[o + 1] = Math.round(g / csum);
      rgba[o + 2] = Math.round(b / csum);
    }
    rgba[o + 3] = Math.round(Math.min(1, csum / n) * 255);
  }
}

// --- 4. 编码 PNG（RGBA8）并包进 ICO 容器（ICONDIR + 1 entry + PNG 载荷） ---
function crc32(buf) {
  let c = ~0;
  for (let i = 0; i < buf.length; i++) {
    c ^= buf[i];
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

const ihdr = Buffer.alloc(13);
ihdr.writeUInt32BE(OUT, 0);
ihdr.writeUInt32BE(OUT, 4);
ihdr[8] = 8;  // bit depth
ihdr[9] = 6;  // color type: RGBA
const raw = Buffer.alloc(OUT * (OUT * 4 + 1));
for (let y = 0; y < OUT; y++) {
  raw[y * (OUT * 4 + 1)] = 0; // filter: none
  rgba.copy(raw, y * (OUT * 4 + 1) + 1, y * OUT * 4, (y + 1) * OUT * 4);
}
const png = Buffer.concat([
  Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  chunk("IHDR", ihdr),
  chunk("IDAT", zlib.deflateSync(raw)),
  chunk("IEND", Buffer.alloc(0)),
]);

const dir = Buffer.alloc(22); // ICONDIR(6) + 1 × ICONDIRENTRY(16)
dir.writeUInt16LE(0, 0);  // reserved
dir.writeUInt16LE(1, 2);  // type: icon
dir.writeUInt16LE(1, 4);  // image count
dir[6] = OUT;             // width
dir[7] = OUT;             // height
dir[8] = 0;               // 调色板数（0 = 真彩色）
dir[9] = 0;               // reserved
dir.writeUInt16LE(1, 10); // planes
dir.writeUInt16LE(32, 12);// bpp
dir.writeUInt32LE(png.length, 14);
dir.writeUInt32LE(6 + 16, 18); // PNG 数据偏移

fs.writeFileSync(OUT_PATH, Buffer.concat([dir, png]));
console.log(`wrote ${OUT_PATH} (${22 + png.length} bytes, ${OUT}x${OUT} PNG-in-ICO)`);
