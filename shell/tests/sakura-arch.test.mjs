// sakura-arch.test.mjs —— sakura-arch.ts 纯函数单测：
//   1. pickFrpcArchKey：win 优先（行为不变）/ darwin arm64 / darwin amd64 /
//      匹配失败（null，调用方给出可手动放置的错误信息）；
//   2. isFrpcArchive：URL 后缀 / content-type / 都不是；
//   3. 隧道归属：sanitizeHostname / defaultTunnelName / isOwnTunnel
//      （本机名 / 旧默认名 / 其它设备名 / 服务端连字符规范化）。
// sakura-arch.ts 无 electron 依赖，直接 import 源码（ts-loader 转译）。
import test from "node:test";
import assert from "node:assert/strict";
import {
  defaultTunnelName,
  extractFrpcFromArchive,
  isFrpcArchive,
  isOwnTunnel,
  LEGACY_TUNNEL_NAME,
  pickFrpcArchKey,
  sanitizeHostname,
} from "../src/sakura-arch.ts";

// ---- pickFrpcArchKey ----

test("archKey：win32 优先 win（行为与旧版一字不变）", () => {
  const keys = ["darwin-arm64", "linux-amd64", "win-amd64", "win-x86"];
  assert.equal(pickFrpcArchKey(keys, { platform: "win32", arch: "x64" }), "win-amd64");
  // 旧判定是 /win/i + /amd64|x64|64/i：键名不规整时同样命中（回归钉子）
  assert.equal(pickFrpcArchKey(["win_64", "darwin-arm64"], { platform: "win32", arch: "x64" }), "win_64");
  // win 侧匹配不到 → null（错误信息分支由调用方按平台给「Windows 下载项」原文）
  assert.equal(pickFrpcArchKey(["darwin-arm64", "linux-amd64"], { platform: "win32", arch: "x64" }), null);
});

test("archKey：darwin arm64 命中（mac 修复主路径）", () => {
  const keys = ["win-amd64", "linux-amd64", "darwin-arm64", "darwin-amd64"];
  assert.equal(pickFrpcArchKey(keys, { platform: "darwin", arch: "arm64" }), "darwin-arm64");
});

test("archKey：darwin amd64 / x86_64 / x64 词形都命中", () => {
  for (const key of ["darwin-amd64", "darwin-x86_64", "darwin-x64"]) {
    assert.equal(
      pickFrpcArchKey(["win-amd64", "darwin-arm64", key], { platform: "darwin", arch: "x64" }),
      key,
      `键名 ${key}`,
    );
  }
  // Apple Silicon 的 arch 名也可能出现在键里（aarch64 词形）
  assert.equal(
    pickFrpcArchKey(["darwin-aarch64", "darwin-amd64"], { platform: "darwin", arch: "arm64" }),
    "darwin-aarch64",
  );
});

test("archKey：darwin 键缺失时不回落到别的平台（宁缺毋滥）", () => {
  assert.equal(pickFrpcArchKey(["win-amd64", "linux-amd64"], { platform: "darwin", arch: "arm64" }), null);
  // 平台词命中但没有架构词命中（darwin-i386）→ null，不拿错架构的二进制
  assert.equal(pickFrpcArchKey(["darwin-i386", "linux-amd64"], { platform: "darwin", arch: "arm64" }), null);
});

test("archKey：匹配失败返回 null（调用方给出含手动放置出路的错误）", () => {
  // macos 词形键（平台词兜底联合匹配）也该命中——真实 API 键名未钉死，宽松匹配
  assert.equal(pickFrpcArchKey(["macos-arm64", "win-amd64"], { platform: "darwin", arch: "arm64" }), "macos-arm64");
  // 平台词无命中且联合匹配也无命中 → null
  assert.equal(pickFrpcArchKey(["win-amd64"], { platform: "darwin", arch: "arm64" }), null);
  // 未知平台（freebsd 等）→ null
  assert.equal(pickFrpcArchKey(["win-amd64", "darwin-arm64"], { platform: "freebsd", arch: "amd64" }), null);
});

// ---- isFrpcArchive ----

test("isFrpcArchive：URL 后缀（.tar.gz/.tgz/.zip）与 content-type 两条判定通道", () => {
  assert.ok(isFrpcArchive("https://x/frpc_0.51.0_darwin_arm64.tar.gz", null));
  assert.ok(isFrpcArchive("https://x/frpc.tgz", null));
  assert.ok(isFrpcArchive("https://x/frpc.zip", null));
  assert.ok(isFrpcArchive("https://x/download?id=1", "application/gzip"));
  assert.ok(isFrpcArchive("https://x/download?id=1", "application/zip"));
  assert.ok(!isFrpcArchive("https://x/frpc", null), "直接二进制（无后缀无压缩 CT）不是压缩包");
  assert.ok(!isFrpcArchive("https://x/frpc", "application/octet-stream"));
});

// ---- 隧道归属 ----

test("sanitizeHostname：去非法字符 + 截断 24", () => {
  assert.equal(sanitizeHostname("DESK-01"), "desk-01");
  assert.equal(sanitizeHostname("myMachine.local"), "mymachinelocal");
  assert.equal(sanitizeHostname("a".repeat(40)), "a".repeat(24), "截断到 24");
  assert.equal(sanitizeHostname("___"), "unknown", "全非法字符 → unknown");
});

test("defaultTunnelName：lxcode-<hostname>，保留 lxcode 前缀", () => {
  assert.equal(defaultTunnelName("DESK-01"), "lxcode-desk-01");
  assert.match(defaultTunnelName("whatever"), /^lxcode-/);
});

test("isOwnTunnel：本机名命中（含服务端连字符规范化）", () => {
  assert.ok(isOwnTunnel("lxcode-desk-01", "DESK-01"), "精确同名");
  assert.ok(isOwnTunnel("lxcode_desk_01", "DESK-01"), "服务端把连字符规范成下划线");
  // 域名形态 hostname：净化剥掉非法字符（.），隧道名按净化值比对——
  // 断言的是净化后形态的命中（defaultTunnelName 也产出这个名字）。
  assert.ok(
    isOwnTunnel(defaultTunnelName("myMachine.local"), "myMachine.local"),
    "域名形态 hostname 按净化值命中（与 defaultTunnelName 闭环）",
  );
});

test("isOwnTunnel：历史遗留默认名恒命中（归最先用的那台）", () => {
  assert.ok(isOwnTunnel("lxcode-backend", "any-other-machine"), "旧默认名任何机器都认领（兼容规则覆盖）");
  assert.ok(isOwnTunnel("lxcode_backend", "any"), "下划线变形同命中");
  assert.equal(LEGACY_TUNNEL_NAME, "lxcode-backend");
});

test("isOwnTunnel：其它设备名 / 其它 lxcode 前缀名 / 空 → 不认领", () => {
  assert.ok(!isOwnTunnel("lxcode-other-box", "DESK-01"), "别的机器 hostname 命名");
  assert.ok(!isOwnTunnel("lxcode-biz-tunnel", "DESK-01"), "用户面板自建的 lxcode 前缀隧道");
  assert.ok(!isOwnTunnel("myweb", "DESK-01"), "无关名");
  assert.ok(!isOwnTunnel(undefined, "DESK-01"), "名字缺失");
  // 本机 hostname 恰为 backend 时，旧默认名与 hostname 规则重合（无害）
  assert.ok(isOwnTunnel("lxcode-backend", "backend"));
});
