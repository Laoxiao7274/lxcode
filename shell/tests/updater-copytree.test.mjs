// updater-copytree.test.mjs —— copyTree 的 asar 补丁语义复现单测（0.2.16→0.2.17
// 事故回归钉子：copyTree 曾混用补丁版 fs（readdirSync/statSync 走顶部 import 的
// node:fs，被 Electron 打了 asar 补丁）与 originalFs（mkdir/copyFile）——补丁版
// statSync 把 app.asar 报成目录、readdirSync 能列出 asar 虚拟内容（build/icon.png
// 等），递归进虚拟目录后 originalFs copyFileSync 找不到真实文件 → ENOENT。
//
// updater.ts 引 electron 不能整模块加载，这里把源码转译后注入受控模块环境执行：
// 补丁版 fs = 对 Electron asar 行为的最小模拟（路径含 app.asar → stat 报目录 +
// readdir 列虚拟内容），originalFs = node:fs 真身。断言：无论补丁开还是关，
// 复制结果里 app.asar 都必须以普通文件形态落位、大小与源一致。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, mkdirSync, writeFileSync, rmSync, statSync, readdirSync, existsSync } from "node:fs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import * as realFs from "node:fs";
import { join as pathJoin } from "node:path";
import ts from "typescript";

const updaterSrc = readFileSync(
  join(fileURLToPath(new URL("../src/", import.meta.url)), "updater.ts"),
  "utf8",
);

// 从 updater.ts 源码里切出 copyTree 函数（含 docstring）单独执行——避免加载整个模块
// （electron/original-fs 顶层依赖）。切取边界：从 "/** 递归复制目录" 到下一个顶层函数声明。
function extractCopyTree(src) {
  const start = src.indexOf("/** 递归复制目录");
  const end = src.indexOf("/** 暂存防御校验");
  assert.ok(start > 0 && end > start, "updater.ts 里必须能定位 copyTree（夹在两个 docstring 之间）");
  return src.slice(start, end);
}

// 受控模块环境：{ electron: { originalFs }, original-fs, node:fs(补丁版), node:path }。
// patched fs 模拟 Electron asar 语义：路径含 app.asar 且路径在 asarRoot 之下 →
// statSync 报目录、readdirSync 列出虚拟内容；其余行为与真 fs 相同。
function makePatchedFs(asarRoot, virtualEntries) {
  const real = { ...await0() };
  function inAsar(p) {
    return typeof p === "string" && p.includes(asarRoot);
  }
  const fs = {
    ...real,
    statSync(p, opts) {
      if (inAsar(p)) {
        // Electron 补丁语义：asar 根与内部虚拟目录报 isDirectory()=true，
        // 内部虚拟文件报 isFile()=true（0.2.17 事故里 readdir 列出的
        // build/icon.png 就是这么被当成待复制文件的）
        const rel = p.slice(p.indexOf(asarRoot) + asarRoot.length).replace(/^[\/]/, "");
        if (rel === "" || rel in virtualEntries) {
          return { isDirectory: () => true, isFile: () => false, size: 0, mtimeMs: 0 };
        }
        return { isDirectory: () => false, isFile: () => true, size: 42, mtimeMs: 0 };
      }
      return statSync(p, opts);
    },
    readdirSync(p, opts) {
      if (inAsar(p)) {
        // 虚拟内容：app.asar 内部结构（0.2.17 事故里 readdir 拿到的就是这些）
        const rel = p.slice(p.indexOf(asarRoot) + asarRoot.length).replace(/^[\\/]/, "");
        return virtualEntries[rel] ?? [];
      }
      return readdirSync(p, opts);
    },
    copyFileSync: (a, b) => {
      if (inAsar(a)) {
        // 与事故报错一致：originalFs 不认识 asar 虚拟路径 → ENOENT copyfile
        const e = new Error(`ENOENT: no such file or directory, copyfile '${a}' -> '${b}'`);
        e.code = "ENOENT";
        throw e;
      }
      return require("node:fs").copyFileSync(a, b);
    },
  };
  return fs;
}
function await0() {
  return {
    statSync,
    readdirSync,
    copyFileSync: (...a) => require("node:fs").copyFileSync(...a),
    mkdirSync,
    rmSync,
    existsSync,
    readFileSync,
    writeFileSync,
  };
}

// 在受控环境里执行 copyTree 源码，返回该函数。
function buildCopyTreeWith(fsEnv) {
  const tsCode = extractCopyTree(updaterSrc);
  const { outputText } = ts.transpileModule(tsCode, {
    compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 },
  });
  const module = new Function(
    "loadOriginalFs",
    "join",
    "__patchedFs",
    `
      const statSync = (p, o) => __patchedFs.statSync(p, o);
      const readdirSync = (p, o) => __patchedFs.readdirSync(p, o);
      ${outputText}
      return copyTree;
    `,
  );
  return module(
    () => realFs, // originalFs = 真 fs（不认识 asar 虚拟路径）
    (a, b) => pathJoin(a, b),
    fsEnv,
  );
}

test("copyTree 在 asar 补丁环境下把 app.asar 当普通文件复制（0.2.17 事故回归钉子）", () => {
  const root = mkdtempSync(join(tmpdir(), "lxcode-copytree-"));
  try {
    const payload = join(root, "payload");
    const staged = join(root, "staged");
    mkdirSync(join(payload, "resources", "bin"), { recursive: true });
    const asarBytes = Buffer.from("FAKE-ASAR-CONTENTS-" + "x".repeat(128));
    writeFileSync(join(payload, "resources", "app.asar"), asarBytes);
    writeFileSync(join(payload, "resources", "bin", "lxcode.exe"), "FAKE-EXE");

    // Electron asar 补丁模拟：payload/resources/app.asar 报目录 + 虚拟内容 build/icon.png
    const patched = makePatchedFs(join(payload, "resources", "app.asar"), {
      "": ["build"],
      build: ["icon.png"],
    });
    const copyTree = buildCopyTreeWith(patched);
    copyTree(payload, staged);

    // app.asar 必须以文件形态落位且字节一致——事故里这里是被递归出的虚拟目录
    const st = statSync(join(staged, "resources", "app.asar"));
    assert.ok(st.isFile(), "staged 里的 app.asar 必须是普通文件（不是目录）");
    assert.equal(st.size, asarBytes.length, "app.asar 大小必须与 payload 一致");
    assert.deepEqual(readFileSync(join(staged, "resources", "app.asar")), asarBytes, "app.asar 字节一致");
    assert.ok(existsSync(join(staged, "resources", "bin", "lxcode.exe")), "其余文件照常复制");
    // 事故里虚拟出来的路径不得存在
    assert.ok(!existsSync(join(staged, "resources", "app.asar", "build")), "不得递归出 asar 虚拟子目录");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("copyTree 在无补丁环境下行为不变（正常文件树复制）", () => {
  const root = mkdtempSync(join(tmpdir(), "lxcode-copytree-plain-"));
  try {
    const payload = join(root, "payload");
    const staged = join(root, "staged");
    mkdirSync(join(payload, "resources", "bin"), { recursive: true });
    writeFileSync(join(payload, "resources", "app.asar"), "ASAR");
    writeFileSync(join(payload, "resources", "bin", "lxcode.exe"), "EXE");
    const copyTree = buildCopyTreeWith(await0()); // 真 fs，无补丁
    copyTree(payload, staged);
    assert.equal(readFileSync(join(staged, "resources", "app.asar"), "utf8"), "ASAR");
    assert.equal(readFileSync(join(staged, "resources", "bin", "lxcode.exe"), "utf8"), "EXE");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("源码钉子：copyTree 的 readdir/stat 必须 fs.*（originalFs），不得用顶部 import 的补丁版", () => {
  const fn = extractCopyTree(updaterSrc);
  assert.ok(!/[^.\w](readdirSync|statSync|copyFileSync|existsSync|mkdirSync)\(/.test(fn),
    "copyTree 内所有 fs 调用必须走 fs.xxx（loadOriginalFs 的返回值），不得裸调补丁版");
  assert.ok(fn.includes("fs.readdirSync") && fn.includes("fs.statSync") && fn.includes("fs.copyFileSync"),
    "遍历+复制全程 originalFs");
});

test("正对照：旧版混用 copyTree（readdir/stat 走补丁版 fs）在同环境下复现事故 ENOENT", () => {
  const root = mkdtempSync(join(tmpdir(), "lxcode-copytree-old-"));
  try {
    const payload = join(root, "payload");
    const staged = join(root, "staged");
    mkdirSync(join(payload, "resources"), { recursive: true });
    writeFileSync(join(payload, "resources", "app.asar"), "FAKE-ASAR");
    const patched = makePatchedFs(join(payload, "resources", "app.asar"), { "": ["build"], build: ["icon.png"] });
    // 旧版实现原样（0.2.16 产物）：mkdir/copy 走 originalFs，readdir/stat 走顶部 import 的补丁版 fs
    const oldCopyTree = new Function(
      "loadOriginalFs", "join", "statSync", "readdirSync",
      `return function copyTree(src, dst) {
         const fs = loadOriginalFs();
         fs.mkdirSync(dst, { recursive: true });
         for (const name of readdirSync(src)) {
           const s = join(src, name);
           const d = join(dst, name);
           if (statSync(s).isDirectory()) copyTree(s, d);
           else fs.copyFileSync(s, d);
         }
       };`,
    )(() => realFs, pathJoin, patched.statSync, patched.readdirSync);
    assert.throws(() => oldCopyTree(payload, staged),
      /ENOENT.*copyfile/, "旧版混用实现必须在补丁环境下复现事故 ENOENT（正对照：本测试环境确实能抓住这个 bug）");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
