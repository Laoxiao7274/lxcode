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

test("sanitizeHostname：非法字符替换成下划线 + 截断 13（natfrp 只认字母数字下划线）", () => {
  // 连字符（Windows hostname 常见形态 DESKTOP-XXXX）→ _，2026-12 报障根因
  assert.equal(sanitizeHostname("DESK-01"), "desk_01");
  assert.equal(sanitizeHostname("DESKTOP-ABC123"), "desktop_abc12", "连字符→_ 且截断到 13");
  // 中文/空格/点（域名形态）→ _，不再剥掉（剥掉会把两段拼成一段）
  assert.equal(sanitizeHostname("我的电脑"), "____", "中文逐字替换成 _");
  assert.equal(sanitizeHostname("myMachine.local"), "mymachine_loc", "点→_ 且截断到 13");
  // 下划线合法，保留
  assert.equal(sanitizeHostname("work_pc"), "work_pc");
  // 截断到 13（lxcode- 前缀 7 + 13 = 20，API 硬上限）
  assert.equal(sanitizeHostname("a".repeat(40)), "a".repeat(13));
  // "___" 的下划线本身合法，净化非空（不再是 unknown 形态的兜底）；纯空串才落 "pc"
  assert.equal(sanitizeHostname("___"), "___");
  assert.equal(sanitizeHostname(""), "pc", "空 hostname → 兜底 pc");
  assert.equal(sanitizeHostname("我的电脑!!!"), "_______", "四个汉字+三个标点，逐字替换成 _");
  // 主机段截断到 13：中文+标点混合时截断后仍是纯 _（合法形状优先于内容）
});

test("defaultTunnelName：恒为 3-20 长且只含 [A-Za-z0-9_]（API 硬校验形状）", () => {
  const legal = (n) => /^[A-Za-z0-9_]{3,20}$/.test(n);
  for (const host of ["DESKTOP-ABC123", "我的电脑", "a".repeat(40), "___", "myMachine.local", "OK-PC_1"]) {
    const name = defaultTunnelName(host);
    assert.ok(legal(name), `hostname=${host} → ${name} 不满足 natfrp 命名规范`);
    assert.ok(name.startsWith("lxcode_"), `保留 lxcode_ 前缀（播种正则兼容）：${name}`);
  }
  assert.equal(defaultTunnelName("DESKTOP-ABC123"), "lxcode_desktop_abc12", "超长段截断到 13（DESKTOP-ABC123 → desktop_abc12）");
  assert.equal(defaultTunnelName("我的电脑"), "lxcode_____", "中文+标点混合段截到 13");
  assert.equal(defaultTunnelName("___"), "lxcode____", "下划线 hostname 合法保留");
  assert.equal(defaultTunnelName("a".repeat(40)), `lxcode_${"a".repeat(13)}`);
  assert.equal(defaultTunnelName("DESK-01"), "lxcode_desk_01");
});

test("isOwnTunnel：本机名命中（含服务端连字符规范化）", () => {
  assert.ok(isOwnTunnel("lxcode_desk_01", "DESK-01"), "新净化形态精确同名");
  assert.ok(isOwnTunnel("lxcode-desk-01", "DESK-01"), "服务端把下划线规范成连字符也命中（-/_ 一视同仁）");
  // 域名形态 hostname：净化产出下划线形态，isOwnTunnel 与 defaultTunnelName 闭环
  assert.ok(
    isOwnTunnel(defaultTunnelName("myMachine.local"), "myMachine.local"),
    "域名形态 hostname 按净化值命中（与 defaultTunnelName 闭环）",
  );
  // 升级兼容：旧版净化保留连字符，存量隧道名 lxcode-desk-01 在新版比对下仍认领
  assert.ok(isOwnTunnel("lxcode-desk-01", "DESK-01"), "旧版生成的连字符隧道名升级后仍认领");
});

test("isOwnTunnel：旧版净化规则的存量隧道名升级后仍认领（候选名集合匹配）", () => {
  // 旧版规则（提交 75825cd 引入时的 sanitizeHostname）：小写、只留 [a-z0-9-]
  // （剥点号不替换）、截断 24、前缀 lxcode-——与新版（点→_、截断 13）不同名。
  // 实测失配三组（2026-12 验收缺陷）：升级后必须仍能接管。
  assert.ok(
    isOwnTunnel("lxcode-DESKTOP-ABC123", "DESKTOP-ABC123"),
    "旧版连字符名（>13 截断失配的典型）：desktop-abc123 ≠ 新版 desktop_abc12",
  );
  assert.ok(
    isOwnTunnel("lxcode-mymachinelocal", "myMachine.local"),
    "旧版剥点号名：mymachinelocal ≠ 新版 mymachine_loc",
  );
  assert.ok(
    isOwnTunnel("lxcode-myverylongmachine01", "myverylongmachine01"),
    "旧版 19 字符纯字母数字名（超新版 13 上限）",
  );
  // 服务端把连字符规范成下划线的变形同样命中（-/_ 一视同仁）
  assert.ok(isOwnTunnel("lxcode_desktop_abc123", "DESKTOP-ABC123"), "旧名经服务端 -→_ 规范化后同命中");
  // 旧版截断到 24：超长 hostname 的旧名是前 24 字符；>24 主机段的名不可能是旧版产物
  assert.ok(isOwnTunnel(`lxcode-${"a".repeat(20)}`, "a".repeat(20)), "旧版 24 上限内的长名");
  assert.ok(isOwnTunnel(`lxcode-${"a".repeat(24)}`, "a".repeat(30)), "旧版截到 24：30 字符 hostname 的旧名是前 24 字符");
  assert.ok(!isOwnTunnel(`lxcode-${"a".repeat(30)}`, "a".repeat(30)), "旧版不可能生成 >24 主机段的名（截断后精确比对）");
});

test("isOwnTunnel：负例不放宽——不同 hostname 的名字必须不认领", () => {
  assert.ok(!isOwnTunnel("lxcode_desktop_abc123", "OTHER-PC"), "别机的旧版名对本机 hostname");
  assert.ok(!isOwnTunnel("lxcode_desktop_abc12x", "DESKTOP-ABC123"), "名字差一个字符（精确比对，不是前缀）");
  assert.ok(!isOwnTunnel("lxcode-mymachinelocalx", "myMachine.local"), "旧名多一字（非模糊匹配）");
  assert.ok(!isOwnTunnel("lxcode-myverylongmachine011", "myverylongmachine01"), "旧名超长不截断到本机名");
  assert.ok(!isOwnTunnel("lxcode-my_machine_local", "myMachine.local"), "新版形态（点→_）不是本机旧版剥点形态的反向命中——两净化候选都精确比对");
  assert.ok(!isOwnTunnel("lxcode-my-machine", "myMachine.local"), "旧版会剥点，绝不出现含点的净化值");
});

test("isOwnTunnel：历史遗留默认名恒命中（归最先用的那台）", () => {
  assert.ok(isOwnTunnel("lxcode-backend", "any-other-machine"), "旧默认名任何机器都认领（兼容规则覆盖）");
  assert.ok(isOwnTunnel("lxcode_backend", "any"), "下划线变形同命中");
  assert.equal(LEGACY_TUNNEL_NAME, "lxcode-backend");
});

test("isOwnTunnel：其它设备名 / 其它 lxcode 前缀名 / 空 → 不认领", () => {
  assert.ok(!isOwnTunnel("lxcode-other_box", "DESK-01"), "别的机器 hostname 命名");
  assert.ok(!isOwnTunnel("lxcode-biz-tunnel", "DESK-01"), "用户面板自建的 lxcode 前缀隧道");
  assert.ok(!isOwnTunnel("myweb", "DESK-01"), "无关名");
  assert.ok(!isOwnTunnel(undefined, "DESK-01"), "名字缺失");
  // 本机 hostname 恰为 backend 时，旧默认名与 hostname 规则重合（无害）
  assert.ok(isOwnTunnel("lxcode-backend", "backend"));
});
