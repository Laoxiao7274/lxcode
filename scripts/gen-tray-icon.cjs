// gen-tray-icon.cjs —— 从 shell/build/icon.png 生成多尺寸托盘图标 shell/build/tray.ico
//
// 为什么需要它（用户报「托盘图标撕裂/切碎」的根因）：
//   shell/src/tray.ts 之前直接把 512x512 的 icon.png 交给 Tray。Windows 上
//   Electron 的 Tray::SetImage 走 NativeImage::GetHICON(GetSystemMetrics(SM_CXSMICON))
//   ——对非 .ico 路径，GetHICON 把**整张位图**（512x512）做成 HICON 交给
//   Shell_NotifyIcon，尺寸参数被忽略；真正的 16/20/24px 缩放由任务栏用 GDI 完成。
//   源图笔画只有 11/512 ≈ 2.1% 宽，缩到 16px 后不足 0.35px —— GDI 的低质量缩放
//   把它打成散点，看起来就是「撕裂/切碎」。
//   而 .ico 路径走 ReadICOFromPath → LoadImage(IMAGE_ICON, size, size, LR_LOADFROMFILE)，
//   系统会**挑尺寸最接近的条目**（不缩放），所以多尺寸 ico 才是托盘图标的正确形态。
//
// 本脚本做的事：按目标尺寸做面积平均降采样（而非 GDI 缩放），小尺寸先对笔画做
// 形态学加粗（细于 1px 的笔画必然丢失，加粗到可辨认）并去掉不可读的「code」字标。
//
// 用法：node scripts/gen-tray-icon.cjs
// 产物：shell/build/tray.ico（16/20/24/32/40/48 —— 覆盖 100%~300% 缩放）
// 零依赖：只用 node 内置 zlib（PNG 解码/编码 + ICO 容器自己拼）。
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const zlib = require("node:zlib");

const ROOT = path.join(__dirname, "..");
const SRC = path.join(ROOT, "shell/build/icon.png");
const OUT = path.join(ROOT, "shell/build/tray.ico");

// 托盘实际用到的尺寸：SM_CXSMICON = 16 × (DPI/96)，覆盖 100/125/150/200/250/300%
const SIZES = [16, 20, 24, 32, 40, 48];

// 每档尺寸的配方：dilate = 笔画加粗半径（源图像素）。
// 源图笔画宽 11/512，16px 下只剩 0.34px，不加粗必然断成散点；尺寸越大需要的
// 加粗越少（48px 时原样即可）。这些值是对着真实任务栏截图逐档调出来的。
const RECIPE = {
  16: { dilate: 10 },
  20: { dilate: 8 },
  24: { dilate: 6 },
  32: { dilate: 3 },
  40: { dilate: 1 },
  48: { dilate: 0 },
};

// 源图 512x512 的实测几何（像素坐标）
const TILE_RADIUS = 91;                                   // 圆角瓷砖半径
const WORDMARK = { x0: 378, y0: 238, x1: 512, y1: 302 };  // 「code」字标区域

// --- 1. 解码 PNG（8-bit；实现全部 5 种行滤波，不假设源图只用 filter 0） ---
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

// --- 2. 取「墨量」场与瓷砖透明度（底色是浅灰圆角瓷砖，笔画近黑） ---
// 直接对 RGB 做面积平均会让笔画偏灰，所以按「离底色多远」归一成 0..1 的墨量，
// 再乘 alpha（瓷砖外的透明区不计墨）。
function inkField(img) {
  const { width, height, data } = img;
  const n = width * height;
  const lum = new Float32Array(n);
  const alpha = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    lum[i] = (data[o] + data[o + 1] + data[o + 2]) / 3;
    alpha[i] = data[o + 3] / 255;
  }
  const tileLum = lum[(height >> 1) * width + (width >> 1)]; // 中心像素 = 瓷砖纯色
  const ink = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    ink[i] = Math.min(1, Math.max(0, (tileLum - lum[i]) / tileLum)) * alpha[i];
  }
  return { width, height, ink, tileLum };
}

// 抹掉字标区域（托盘尺寸下不可读）
function withoutWordmark(field) {
  const { width, height, ink } = field;
  const copy = new Float32Array(ink);
  const x0 = Math.max(0, WORDMARK.x0), x1 = Math.min(width, WORDMARK.x1);
  const y0 = Math.max(0, WORDMARK.y0), y1 = Math.min(height, WORDMARK.y1);
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) copy[y * width + x] = 0;
  }
  return { ...field, ink: copy };
}

// 形态学加粗（3x3 十字膨胀 r 次）——把细于 1px 的笔画撑到能落在像素上
function dilate(field, radius) {
  if (radius <= 0) return field;
  const { width, height } = field;
  let cur = field.ink;
  for (let r = 0; r < radius; r++) {
    const next = new Float32Array(cur.length);
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const i = y * width + x;
        let v = cur[i];
        if (x > 0) v = Math.max(v, cur[i - 1]);
        if (x < width - 1) v = Math.max(v, cur[i + 1]);
        if (y > 0) v = Math.max(v, cur[i - width]);
        if (y < height - 1) v = Math.max(v, cur[i + width]);
        next[i] = v;
      }
    }
    cur = next;
  }
  return { ...field, ink: cur };
}

// --- 3. 面积平均降采样（每个目标像素 = 源图对应矩形的精确加权平均） ---
function areaDownsample(field, size) {
  const { width, height, ink } = field;
  const out = new Float32Array(size * size);
  const sx = width / size, sy = height / size;
  for (let dy = 0; dy < size; dy++) {
    const y0 = dy * sy, y1 = y0 + sy;
    for (let dx = 0; dx < size; dx++) {
      const x0 = dx * sx, x1 = x0 + sx;
      let sum = 0, wSum = 0;
      for (let y = Math.floor(y0); y < Math.ceil(y1); y++) {
        const wy = Math.min(y + 1, y1) - Math.max(y, y0);
        if (wy <= 0) continue;
        for (let x = Math.floor(x0); x < Math.ceil(x1); x++) {
          const wx = Math.min(x + 1, x1) - Math.max(x, x0);
          if (wx <= 0) continue;
          sum += ink[y * width + x] * wx * wy;
          wSum += wx * wy;
        }
      }
      out[dy * size + dx] = sum / wSum;
    }
  }
  return out;
}

// 圆角瓷砖的 alpha 覆盖（SDF 抗锯齿）——按目标尺寸直接算，不靠缩放
function tileAlpha(size) {
  const out = new Float32Array(size * size);
  const r = (TILE_RADIUS / 512) * size;
  const half = size / 2;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const dx = Math.max(Math.abs(x + 0.5 - half) - (half - r), 0);
      const dy = Math.max(Math.abs(y + 0.5 - half) - (half - r), 0);
      out[y * size + x] = Math.min(1, Math.max(0, 0.5 - (Math.hypot(dx, dy) - r)));
    }
  }
  return out;
}

// --- 4. 合成目标尺寸的 RGBA ---
function renderSize(field, size) {
  const ink = areaDownsample(field, size);
  const alpha = tileAlpha(size);
  const tile = field.tileLum;
  const out = Buffer.alloc(size * size * 4);
  for (let i = 0; i < size * size; i++) {
    const k = Math.min(1, ink[i]);
    const c = Math.round(tile * (1 - k));
    const o = i * 4;
    out[o] = c;
    out[o + 1] = c;
    out[o + 2] = c;
    out[o + 3] = Math.round(alpha[i] * 255);
  }
  return out;
}

// --- 5. ICO 载荷：32-bit DIB（BITMAPINFOHEADER + BGRA 自下而上 + AND 掩码） ---
// 托盘尺寸一律用 DIB 而不是 PNG-in-ICO：PNG 载荷在 Vista+ 名义上可用，但小尺寸
// （16/20/24）走的是任务栏最老的那条 LoadImage 路径，DIB 是各版本都稳的经典格式，
// 文件也只有几十 KB。AND 掩码按规范填「全不透明」（透明度由 alpha 通道给）。
function encodeDIB(rgba, size) {
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);              // biSize
  header.writeInt32LE(size, 4);             // biWidth
  header.writeInt32LE(size * 2, 8);         // biHeight（XOR + AND 两份）
  header.writeUInt16LE(1, 12);              // biPlanes
  header.writeUInt16LE(32, 14);             // biBitCount
  header.writeUInt32LE(0, 16);              // biCompression = BI_RGB
  header.writeUInt32LE(size * size * 4, 20); // biSizeImage

  // XOR 位图：BGRA、自下而上
  const xor = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    const srcRow = (size - 1 - y) * size * 4;
    rgba.copy(xor, y * size * 4, srcRow, srcRow + size * 4);
  }

  // AND 掩码：1bpp，行按 4 字节对齐，0 = 不透明
  const maskStride = Math.ceil(size / 32) * 4;
  const mask = Buffer.alloc(maskStride * size, 0);
  return Buffer.concat([header, xor, mask]);
}

// --- 6. ICO 容器 ---
function packICO(images) {
  const dir = Buffer.alloc(6 + images.length * 16);
  dir.writeUInt16LE(0, 0);              // reserved
  dir.writeUInt16LE(1, 2);              // type: icon
  dir.writeUInt16LE(images.length, 4);  // image count
  let offset = dir.length;
  images.forEach((img, i) => {
    const o = 6 + i * 16;
    dir[o] = img.size >= 256 ? 0 : img.size;   // 0 表示 256
    dir[o + 1] = img.size >= 256 ? 0 : img.size;
    dir[o + 2] = 0;                            // 调色板数
    dir[o + 3] = 0;                            // reserved
    dir.writeUInt16LE(1, o + 4);               // planes
    dir.writeUInt16LE(32, o + 6);              // bpp
    dir.writeUInt32LE(img.data.length, o + 8);
    dir.writeUInt32LE(offset, o + 12);
    offset += img.data.length;
  });
  return Buffer.concat([dir, ...images.map((i) => i.data)]);
}

// --- 7. 主流程 ---
const src = decodePNG(fs.readFileSync(SRC));
if (src.width !== src.height) throw new Error(`源图必须正方形（当前 ${src.width}x${src.height}）`);
const base = inkField(src);
const images = SIZES.map((size) => {
  // 字标在所有托盘尺寸下都不可读（1~4px 高，糊成一团）→ 一律不画，保持各档一致
  const field = dilate(withoutWordmark(base), RECIPE[size].dilate);
  return { size, data: encodeDIB(renderSize(field, size), size) };
});
fs.writeFileSync(OUT, packICO(images));
console.log(
  `wrote ${OUT} (${fs.statSync(OUT).size} bytes, ${SIZES.join("/")} 32-bit DIB-in-ICO, 源图 ${src.width}x${src.height})`,
);
