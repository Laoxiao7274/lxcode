// 栈版本一致性检查——防止「前端热重载、后端不热重载」导致的静默版本错配。
//
// 背景（2026-09-21 实测事故）：dev 栈由 scripts/dev.mjs 启动时编译一次 Go 后端，
// 之后 vite 对前端热重载，但 Go 二进制**不会**重建——于是前端跑着最新代码、
// 后端还停在几天前的提交。表现是「子 Agent 的权限请求跑到外层、卡片一直执行中」，
// 排查时极易误判为前端 bug（前后端四层映射代码全都正确、只有运行中的二进制旧）。
//
// 判定手段：Go 二进制内嵌构建元数据（buildvcs），`go version -m` 可读出
// 编译时的 commit（vcs.revision / mod 伪版本号后缀）。与 git HEAD 比对即可。
// 用元数据而不是字节搜索——字节搜索对 Go 二进制不可靠（同一 tag 字符串会被
// 链接器去重，实测正对照都能搜不到）。
//
// 用法: node scripts/check-stack.mjs [--quiet]
// 退出码: 0 = 一致或无法判定（不阻塞开发）；1 = 明确版本落后（可当 CI 门禁）
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const quiet = process.argv.includes('--quiet');

/** 后端二进制的候选位置（dev 用 bin/；打包产物在 shell/release 内）。 */
const CANDIDATES = [
  resolve(root, 'bin/lxcode.exe'),
  resolve(root, 'bin/lxcode'),
];

/** 读一个二进制的内嵌构建信息；读不出返回 null（非 Go 产物/被裁剪）。 */
export function readBuildInfo(exe) {
  let out;
  try {
    out = execFileSync('go', ['version', '-m', exe], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  } catch {
    return null;
  }
  const revision = /\bvcs\.revision\s+([0-9a-f]{7,40})/.exec(out)?.[1]
    // mod 伪版本号后缀也是 commit（v0.0.0-20260920034710-bad1bd72f3a9）
    ?? /v0\.0\.0-\d{14}-([0-9a-f]{7,40})/.exec(out)?.[1]
    ?? null;
  const modified = /\bvcs\.modified\s+true\b/.test(out);
  return { revision, modified, raw: out };
}

/** 取仓库当前 HEAD（短 hash）。 */
export function readHead(cwd = root) {
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], { cwd, encoding: 'utf8' }).trim();
  } catch {
    return null;
  }
}

/** 比较两个 hash 是否同一提交（短/长前缀兼容）。 */
export function sameCommit(a, b) {
  if (!a || !b) return null; // 无法判定
  const n = Math.min(a.length, b.length);
  return a.slice(0, n) === b.slice(0, n);
}

/** 二进制编译点之后被改动的 **Go 侧** 文件（*.go / go.mod / go.sum）。
 *  仓库无 go:embed，后端二进制完全由这些文件决定；纯前端/脚本提交不影响它——
 *  只比 commit 会把「仅前端提交」误报成后端落后（实测踩到），于是这里再判一层。
 *  取不到（非法 revision / 非 git）返回 null = 无法判定。 */
export function goChangedSince(revision, cwd = root) {
  try {
    const out = execFileSync('git', ['diff', '--name-only', revision, 'HEAD', '--', '*.go', 'go.mod', 'go.sum'],
      { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    return out.trim().split('\n').filter(Boolean);
  } catch {
    return null;
  }
}

export function checkStack() {
  const exe = CANDIDATES.find((p) => existsSync(p)) ?? null;
  if (!exe) return { status: 'no-binary', exe: null };
  const info = readBuildInfo(exe);
  if (!info || !info.revision) return { status: 'unknown', exe, info };
  const head = readHead();
  if (!head) return { status: 'unknown', exe, info };
  const same = sameCommit(info.revision, head);
  if (same === true) return { status: 'ok', exe, info, head, goFiles: [] };
  // 版本不同：只有 Go 侧代码变了才需要重建后端（纯前端提交改了 commit 但不改后端行为）
  const goFiles = goChangedSince(info.revision);
  if (goFiles === null) return { status: 'unknown', exe, info, head, goFiles: null };
  return { status: goFiles.length > 0 ? 'stale' : 'ok', exe, info, head, goFiles };
}

// 作为脚本直接运行时报告（被 import 时不执行——便于测试）
if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  const r = checkStack();
  const short = (h) => (h ? h.slice(0, 8) : '(未知)');

  if (r.status === 'no-binary') {
    if (!quiet) console.log('未找到后端二进制（bin/lxcode.exe）——跳过栈版本检查');
    process.exit(0);
  }
  if (r.status === 'unknown') {
    if (!quiet) console.log(`无法判定栈版本（二进制缺内嵌构建信息或非 git 仓库）: ${r.exe}`);
    process.exit(0);
  }
  if (r.status === 'ok') {
    if (!quiet) {
      const same = sameCommit(r.info.revision, r.head) === true;
      console.log(same
        ? `栈版本一致: 后端 ${short(r.info.revision)} = HEAD ${short(r.head)}`
        : `栈版本一致（后端行为未落后）: 后端编译于 ${short(r.info.revision)}，其后的 ${short(r.head)} 无 Go 文件改动——后端无需重建`);
    }
    process.exit(0);
  }

  // stale —— 出错要说清「为什么」和「怎么办」
  console.error('✗ 后端二进制落后于当前代码（前端热的、后端不热）');
  console.error(`    后端编译于提交: ${short(r.info.revision)}`);
  console.error(`    当前 HEAD:      ${short(r.head)}`);
  if (r.info.modified) console.error('    且编译时工作区有未提交改动（vcs.modified=true）');
  console.error(`    有 ${r.goFiles.length} 个 Go 侧文件在此之后被改过：`);
  for (const f of r.goFiles.slice(0, 5)) console.error(`      ${f}`);
  if (r.goFiles.length > 5) console.error(`      …等共 ${r.goFiles.length} 个`);
  console.error('    影响：前端是最新的、后端是旧的——协议字段缺失会表现为');
  console.error('          前端诡异 bug（如确认卡跑到外层、卡片永远"执行中"）。');
  console.error('    修复：重启 dev 栈（node scripts/dev.mjs --electron）会重新编译后端。');
  const behind = (() => {
    try {
      return execFileSync('git', ['rev-list', '--count', `${r.info.revision}..HEAD`], { cwd: root, encoding: 'utf8' }).trim();
    } catch { return null; }
  })();
  if (behind) console.error(`    落后 ${behind} 个提交。`);
  process.exit(1);
}
