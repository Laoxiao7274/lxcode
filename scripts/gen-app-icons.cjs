// gen-app-icons.cjs —— LOGO 派生链：shell/build/icon.png → 各平台位图（全仓唯一入口）
//
// 母版：shell/build/icon.png（512×512 RGBA，浅灰圆角瓷砖 + 近黑 LX 笔画 + 「code」
// 字标）。没有 SVG 源——所有下游 LOGO 位图只能从这张 PNG 派生。**不要再手画/复刻
// LOGO**（历史上 topbar「L」、安卓矢量 foreground、site「LX」方块、favicon 字标都
// 是各画各的占位，已经全部替换为派生位图）；改动母版后重跑本脚本即可全链同步。
//
// 产物（全部幂等，重跑覆盖）：
//   frontend/public/favicon-32.png          32px   前端 favicon（index.html link rel=icon）
//   android/.../res/mipmap-*/ic_launcher(.round).png  48/72/96/144/192 启动图标
//   android/.../res/mipmap-*/ic_launcher_foreground.png 108dp 前景×3 密度（adaptive）
//   android/.../res/mipmap-*/ic_launcher_background.png 108dp 背景×3 密度（adaptive）
//   android/.../res/drawable-nodpi/ic_logo.png  192px 应用内 LOGO（painterResource）
//   site/public/logo.png                    256px  官网导航/页脚 LOGO
//   site/public/favicon-32.png              32px   官网 favicon（PNG，换掉旧 SVG 字标）
//   site/public/favicon.ico                 32px   /favicon.ico 兜底（32-bit DIB，改源图即可，
//                                                  与 gen-site-favicon.cjs 的 SVG 链共存但不再被引用）
//
// 设计决策（为什么这么派生）：
// - **自适应图标（anydpi-v26）走「PNG drawable 引用」而不是塞位图 mipmap**：Android
//   惯例是 `<adaptive-icon><foreground android:drawable="@mipmap/ic_launcher_foreground"/>
//   </adaptive-icon>`（AOSP 模板即如此，mipmap 与 drawable 都是合法 drawable 资源）；
//   位图前景比矢量灵活（我们的母版是照片级 PNG，矢量画不了），且密度分档放 mipmap
//   符合「launcher 图标资源进 mipmap」的惯例。
// - **前景/背景都派生**：母版自带浅灰瓷砖底色，直接把整图作为前景会与
//   `<background>` 叠色。所以把母版的「瓷砖底 + LX 笔画」整体居中缩进 66/108 安全区
//   （内容占 61%，四周留白让 launcher 遮罩裁剪），background 用母版瓷砖纯色铺满
//   108dp 画布（ic_launcher_background.png，颜色取自母版中心像素）——遮罩外露出的
//   部分与瓷砖同色，视觉上无缝。
// - **应用内 ic_logo 与桌面 favicon 保留「code」字标**（尺寸足够大时它是品牌的一
//   部分）；20px 级小图（favicon-32、site favicon）同 tray 链一样去掉字标（32px 下
//   字标不足 2px 高，糊成噪点）。去字标的几何与 tray 链共用 WORDMARK 常量——**改母
//   版版式时两份常量要一起改**（本脚本与 gen-tray-icon.cjs 各持一份，无 import）。
//
// 用法：node scripts/gen-app-icons.cjs
// 零依赖：只用 node 内置 zlib（PNG 解码/编码手写，缩放为面积平均降采样）。
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const zlib = require("node:zlib");

const ROOT = path.join(__dirname, "..");
const SRC = path.join(ROOT, "shell/build/icon.png");
const ANDROID_RES = path.join(ROOT, "android/app/src/main/res");
const FRONTEND_PUBLIC = path.join(ROOT, "frontend/public");
const SITE_PUBLIC = path.join(ROOT, "site/public");

// 母版几何实测（512×512 源图像素）——与 gen-tray-icon.cjs 保持一致
const TILE_RADIUS = 91; // 圆角瓷砖半径
const WORDMARK = { x0: 378, y0: 238, x1: 512, y1: 302 }; // 「code」字标区域

// --- 1. PNG 解码（8-bit，全部行滤波；与 gen-tray-icon.cjs 同实现） ---
function decodePNG(buf) {
  if (buf.readUInt32BE(0) !== 0x89504e47) throw new Error("不是 PNG");
  let off = 8;
  let width = 0, height = 0, bitDepth = 0, colorType = 0;
  const idat = [];
  while (off < buf.length) {
    const len = buf.readUInt32BE(off);
    const type = buf.toString("ascii", off + 4, off + 8);
    const data = buf.subarray(off + 8, off + 8 + len);
    if (type === "IHDR") {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      bitDepth = data[8];
      colorType = data[9];
      if (data[12] !== 0) throw new Error("不支持隔行 PNG");
    } else if (type === "IDAT") {
      idat.push(data);
    } else if (type === "IEND") {
      break;
    }
    off += 12 + len;
  }
  if (bitDepth !== 8) throw new Error(`只支持 8-bit PNG（当前 ${bitDepth}）`);
  const channels = { 0: 1, 2: 3, 4: 2, 6: 4 }[colorType];
  if (!channels) throw new Error(`不支持的 PNG colorType ${colorType}`);

  const raw = zlib.inflateSync(Buffer.concat(idat));
  const stride = width * channels;
  const out = Buffer.alloc(width * height * 4);
  let prev = Buffer.alloc(stride);
  let p = 0;
  for (let y = 0; y < height; y++) {
    const filter = raw[p++];
    const line = Buffer.from(raw.subarray(p, p + stride));
    p += stride;
    for (let i = 0; i < stride; i++) {
      const a = i >= channels ? line[i - channels] : 0;
      const b = prev[i];
      const c = i >= channels ? prev[i - channels] : 0;
      switch (filter) {
        case 0: break;
        case 1: line[i] = (line[i] + a) & 0xff; break;
        case 2: line[i] = (line[i] + b) & 0xff; break;
        case 3: line[i] = (line[i] + ((a + b) >> 1)) & 0xff; break;
        case 4: {
          const pa = Math.abs(b - c), pb = Math.abs(a - c), pc = Math.abs(a + b - 2 * c);
          const pred = pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
          line[i] = (line[i] + pred) & 0xff;
          break;
        }
        default: throw new Error(`未知行滤波 ${filter}`);
      }
    }
    for (let x = 0; x < width; x++) {
      const s = x * channels, d = (y * width + x) * 4;
      if (channels === 1) { out[d] = out[d + 1] = out[d + 2] = line[s]; out[d + 3] = 255; }
      else if (channels === 2) { out[d] = out[d + 1] = out[d + 2] = line[s]; out[d + 3] = line[s + 1]; }
      else if (channels === 3) { out[d] = line[s]; out[d + 1] = line[s + 1]; out[d + 2] = line[s + 2]; out[d + 3] = 255; }
      else { out[d] = line[s]; out[d + 1] = line[s + 1]; out[d + 2] = line[s + 2]; out[d + 3] = line[s + 3]; }
    }
    prev = line;
  }
  return { width, height, data: out };
}

// --- 2. PNG 编码（colorType 6 RGBA，filter 0，zlib level 9） ---
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
  len.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([len, body, crc]);
}
function encodePNG(rgba, width, height) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;  // bit depth
  ihdr[9] = 6;  // colorType RGBA
  const raw = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0; // filter 0
    rgba.copy(raw, y * (width * 4 + 1) + 1, y * width * 4, (y + 1) * width * 4);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

// --- 3. 通用缩放：面积平均降采样（每个目标像素 = 源图对应矩形的加权平均，RGBA） ---
function downscaleRGBA(src, size) {
  const { width, height, data } = src;
  if (width === height && width === size) return Buffer.from(data);
  const out = Buffer.alloc(size * size * 4);
  const sx = width / size, sy = height / size;
  for (let dy = 0; dy < size; dy++) {
    const y0 = dy * sy, y1 = y0 + sy;
    for (let dx = 0; dx < size; dx++) {
      const x0 = dx * sx, x1 = x0 + sx;
      let r = 0, g = 0, b = 0, a = 0, wSum = 0;
      for (let y = Math.floor(y0); y < Math.ceil(y1); y++) {
        const wy = Math.min(y + 1, y1) - Math.max(y, y0);
        if (wy <= 0) continue;
        for (let x = Math.floor(x0); x < Math.ceil(x1); x++) {
          const wx = Math.min(x + 1, x1) - Math.max(x, x0);
          if (wx <= 0) continue;
          const w = wx * wy, o = (y * width + x) * 4;
          const al = data[o + 3] / 255;
          r += data[o] * al * w; g += data[o + 1] * al * w; b += data[o + 2] * al * w;
          a += al * w;
          wSum += w;
        }
      }
      const o2 = (dy * size + dx) * 4;
      const al = wSum > 0 ? a / wSum : 0;
      if (al > 0) {
        out[o2] = Math.round(r / (a || 1));
        out[o2 + 1] = Math.round(g / (a || 1));
        out[o2 + 2] = Math.round(b / (a || 1));
      }
      out[o2 + 3] = Math.round(al * 255);
    }
  }
  return out;
}

// 直通上采样/缩放换画布：把 src（RGBA）居中贴到 size×size 透明画布（可选缩放比例）
function centeredPaste(src, size, scale) {
  const inner = downscaleRGBA(src, Math.round(size * scale));
  const out = Buffer.alloc(size * size * 4);
  const off = Math.floor((size - Math.round(size * scale)) / 2);
  const n = Math.round(size * scale);
  for (let y = 0; y < n; y++) {
    inner.copy(out, ((y + off) * size + off) * 4, y * n * 4, (y + 1) * n * 4);
  }
  return out;
}

// 去字标：把 WORDMARK 区域置为瓷砖底色（大尺寸下区域用底色补，不是透明——透明会
// 在瓷砖上开天窗）
function withoutWordmark(img) {
  const { width, data } = img;
  const out = Buffer.from(data);
  const cx = (width >> 1) * 4 + (width >> 1) * 4 + ((width >> 1) * 0); // 中心像素索引取下面更直接的写法
  const c = ((width >> 1) * width + (width >> 1)) * 4;
  for (let y = WORDMARK.y0; y < Math.min(WORDMARK.y1, img.height); y++) {
    for (let x = WORDMARK.x0; x < Math.min(WORDMARK.x1, width); x++) {
      const o = (y * width + x) * 4;
      out[o] = data[c]; out[o + 1] = data[c + 1]; out[o + 2] = data[c + 2]; out[o + 3] = data[c + 3];
    }
  }
  return out;
}

// --- 4. 各产物 ---
function writePNG(file, rgba, size) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, encodePNG(rgba, size, size));
}

// 启动图标（含瓷砖透明角）：直接缩放母版
const LEGACY = [
  ["mipmap-mdpi", 48], ["mipmap-hdpi", 72], ["mipmap-xhdpi", 96],
  ["mipmap-xxhdpi", 144], ["mipmap-xxxhdpi", 192],
];

// adaptive 前景/背景：108dp 画布，内容缩进安全区 66/108 ≈ 61%
const ADAPTIVE = [
  ["mipmap-mdpi", 108], ["mipmap-hdpi", 162], ["mipmap-xhdpi", 216],
  ["mipmap-xxhdpi", 324], ["mipmap-xxxhdpi", 432],
];
const ADAPTIVE_SAFE = 66 / 108; // 内容占画布比例（Android 安全区）

function main() {
  const src = decodePNG(fs.readFileSync(SRC));
  if (src.width !== src.height) throw new Error(`源图必须正方形（当前 ${src.width}x${src.height}）`);
  const noWM = withoutWordmark(src); // 去字标后的 512 RGBA

  // ① 前端 favicon（32px，去字标）
  writePNG(path.join(FRONTEND_PUBLIC, "favicon-32.png"), downscaleRGBA({ ...src, data: noWM }, 32), 32);

  // ② 安卓 legacy 启动图标（48..192，含透明圆角）
  for (const [dir, size] of LEGACY) {
    const img = downscaleRGBA(src, size);
    writePNG(path.join(ANDROID_RES, dir, "ic_launcher.png"), img, size);
    writePNG(path.join(ANDROID_RES, dir, "ic_launcher_round.png"), img, size);
  }

  // ③ adaptive 前景（整图居中缩进安全区）+ 背景（瓷砖纯色铺满）
  const tile = src.data[((src.width >> 1) * src.width + (src.width >> 1)) * 4];
  const bgRGBA = Buffer.alloc(432 * 432 * 4);
  for (let i = 0; i < 432 * 432; i++) {
    bgRGBA[i * 4] = bgRGBA[i * 4 + 1] = bgRGBA[i * 4 + 2] = tile;
    bgRGBA[i * 4 + 3] = 255;
  }
  for (const [dir, size] of ADAPTIVE) {
    writePNG(path.join(ANDROID_RES, dir, "ic_launcher_foreground.png"), centeredPaste(src, size, ADAPTIVE_SAFE), size);
    writePNG(path.join(ANDROID_RES, dir, "ic_launcher_background.png"), downscaleRGBA({ width: 432, height: 432, data: bgRGBA }, size), size);
  }

  // ④ 安卓应用内 LOGO（192px，含字标）
  writePNG(path.join(ANDROID_RES, "drawable-nodpi", "ic_logo.png"), downscaleRGBA(src, 192), 192);

  // ⑤ 官网 logo（256px，含字标）+ favicon（32px 去字标 PNG + ICO 兜底）
  writePNG(path.join(SITE_PUBLIC, "logo.png"), downscaleRGBA(src, 256), 256);
  const fav32 = downscaleRGBA({ ...src, data: noWM }, 32);
  writePNG(path.join(SITE_PUBLIC, "favicon-32.png"), fav32, 32);
  fs.writeFileSync(path.join(SITE_PUBLIC, "favicon.ico"), packICO32(fav32));

  console.log(
    `gen-app-icons: 前端 favicon-32 + 安卓 mipmap ${LEGACY.map(([, s]) => s).join("/")}` +
    ` + adaptive ${ADAPTIVE.map(([, s]) => s).join("/")} + ic_logo 192 + site logo 256/favicon（源 ${src.width}x${src.height}）`,
  );
}

// 32-bit DIB-in-ICO（与 gen-tray-icon.cjs 同款容器，仅单条目）
function packICO32(rgba) {
  const size = 32;
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);
  header.writeInt32LE(size, 4);
  header.writeInt32LE(size * 2, 8);
  header.writeUInt16LE(1, 12);
  header.writeUInt16LE(32, 14);
  header.writeUInt32LE(0, 16);
  header.writeUInt32LE(size * size * 4, 20);
  const xor = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    const srcRow = (size - 1 - y) * size * 4;
    rgba.copy(xor, y * size * 4, srcRow, srcRow + size * 4);
  }
  const maskStride = Math.ceil(size / 32) * 4;
  const mask = Buffer.alloc(maskStride * size, 0);
  const dib = Buffer.concat([header, xor, mask]);
  const dir = Buffer.alloc(6 + 16);
  dir.writeUInt16LE(1, 2); // type icon
  dir.writeUInt16LE(1, 4); // count
  dir[6] = size; dir[7] = size;
  dir.writeUInt16LE(1, 10);
  dir.writeUInt16LE(32, 12);
  dir.writeUInt32LE(dib.length, 14);
  dir.writeUInt32LE(22, 18);
  return Buffer.concat([dir, dib]);
}

main();
