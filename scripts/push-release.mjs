// push-release.mjs —— 发版聚合推送：把本地逐轮检查点历史压成发布线推出去。
//
// 为什么需要它：每轮对话结束会落一个检查点提交（git add -A，一轮一个），这是
// **本地回滚粒度**；直接推 master 会让公开仓库历史被「第 N 轮：…」淹没。所以
// 约定是：
//   - 本地 master = 完整历史（检查点 + 手工提交），**永不推送**，逐轮回滚靠它；
//   - 发布线     = 每次发版一个快照提交（`release: <version>`），公开仓库只看到它。
//
// 关键设计：发布线是**独立的一条线**（每个提交的树 = 当时 master 的树，父提交 =
// 上一个发布提交），不是 master 的祖先。所以：
//   - 本地 master 一根毫毛都不动（检查点全留着）；
//   - 发布提交的正文带 `master-commit: <sha>` 尾注，下一次发版据此知道「从哪个
//     master 提交开始算新改动」（尾注缺失时按树相等在 master 历史里回溯定位）；
//   - 推送目标是 `origin/master`（公开仓库的默认分支就是发布线），快进推送，
//     不需要 force——除非走一次性的 --bootstrap。
//
// 用法：
//   node scripts/push-release.mjs                 # 版本号取 shell/package.json
//   node scripts/push-release.mjs 0.2.19          # 显式指定版本
//   node scripts/push-release.mjs --dry-run       # 只打印将生成的提交，不动 ref、不推
//   node scripts/push-release.mjs --no-push       # 更新本地 release 分支但不推
//   node scripts/push-release.mjs --bootstrap     # 【破坏性，一次性】清理远端旧历史
//   node scripts/push-release.mjs --bootstrap --push-backup  # 同时把备份分支推到远端
//
// 常规发版（幂等）：工作区干净 → 读远端发布线 → 收集上次发布之后的 master 提交
// → 生成一个发布提交（tree = HEAD 的 tree）→ 更新本地 release → 推 origin/master。
//
// --bootstrap 是**一次性清理**：把远端历史重写成「初始提交 + 各发布版本」的线性
// 历史（逐轮检查点不进远端）。执行前强制备份（全量 bundle 到 temp/ + 远端
// backup/pre-cleanup-<时间戳> 分支），然后 force push。
//
// 只依赖 git 与 node 标准库；判定与文案组装都是纯函数（push-release.test.mjs 覆盖）。
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

/** 检查点提交正文的标记（与 internal/project/checkpoint.go 的 CheckpointMarker 同值）。 */
export const CHECKPOINT_MARKER = "[checkpoint]";

/** 发布提交的标题前缀：`release: <version>`。 */
const RELEASE_PREFIX = "release:";

/** 发布提交正文的尾注：这条发布线对应的 master 提交。 */
const MASTER_TRAILER = "master-commit:";

/** 提交类型顺序（发布说明里按这个顺序分组）。 */
const TYPE_ORDER = ["feat", "fix", "perf", "refactor", "docs", "test", "build", "chore", "style", "ci", "revert"];

/** 每个分组最多列出的条目数（再多就折成「另有 N 项」）。 */
const GROUP_LIMIT = 20;

/** 发布说明里手工提交的条数上限（检查点才是主体）。 */
const MANUAL_LIMIT = 15;

// ---------------------------------------------------------------- git 执行

// GIT_TERMINAL_PROMPT=0：凭据缺失时立刻失败而不是挂起等终端输入（与后端
// internal/project/git.go 同一纪律——发版脚本卡在凭据提示上比报错更糟）。
const GIT_ENV = { ...process.env, GIT_TERMINAL_PROMPT: "0" };
const NET_TIMEOUT_MS = 180_000;

function git(cwd, args, opts = {}) {
  const network = args[0] === "fetch" || args[0] === "push" || args[0] === "ls-remote";
  const r = spawnSync("git", args, {
    cwd,
    encoding: "utf8",
    env: opts.env ?? GIT_ENV,
    timeout: opts.timeout ?? (network ? NET_TIMEOUT_MS : undefined),
    ...opts,
  });
  if (r.error) throw r.error;
  if (r.status !== 0) {
    const err = new Error(`git ${args.join(" ")} 失败（exit ${r.status}）\n${(r.stdout || "") + (r.stderr || "")}`);
    err.status = r.status;
    throw err;
  }
  return r.stdout;
}

/** 同 git()，但失败返回 null（用于「这条命令的失败是正常分支」的只读探测）。 */
function gitOrNull(cwd, args) {
  const r = spawnSync("git", args, { cwd, encoding: "utf8", env: GIT_ENV, timeout: NET_TIMEOUT_MS });
  return r.status === 0 ? r.stdout : null;
}

// ---------------------------------------------------------------- 纯函数部分

/**
 * 解析 `git log --format=%H%x1f%an%x1f%aI%x1f%s%x1f%b%x1e` 的输出。
 * 返回 [{hash, author, date, subject, body}]，顺序即输入顺序（调用方用 --reverse）。
 */
export function parseLog(out) {
  return out
    .split("\x1e")
    .map((rec) => rec.replace(/^\r?\n/, "").trimEnd())
    .filter((rec) => rec !== "")
    .map((rec) => {
      const [hash, author, date, subject, ...rest] = rec.split("\x1f");
      return { hash, author, date, subject: subject ?? "", body: rest.join("\x1f") ?? "" };
    });
}

/** 提交分类：release（发布线聚合）/ checkpoint（逐轮检查点）/ manual（手工提交）。 */
export function classifyCommit(commit) {
  const subject = commit.subject ?? "";
  if (subject.startsWith(RELEASE_PREFIX)) return "release";
  // 新格式：正文带 [checkpoint]；老格式：标题以「第 N 轮」开头（历史兼容）。
  if ((commit.body ?? "").includes(CHECKPOINT_MARKER)) return "checkpoint";
  if (/^第\s*\d+\s*轮/.test(subject)) return "checkpoint";
  return "manual";
}

/** 从 conventional commits 标题里取类型与 scope（不合法时类型记 other）。 */
export function subjectType(subject) {
  const m = /^([a-z]+)(?:\(([^)]*)\))?!?:\s*(.*)$/.exec(subject ?? "");
  if (!m) return { type: "other", scope: "", text: (subject ?? "").trim() };
  return { type: m[1], scope: m[2] ?? "", text: m[3] ?? "" };
}

/** 读发布提交正文里的 `master-commit:` 尾注（缺失返回 null）。 */
export function parseMasterTrailer(body) {
  const m = new RegExp(`${MASTER_TRAILER}\\s*([0-9a-f]{7,40})`).exec(body ?? "");
  return m ? m[1] : null;
}

/** 上一发版之后是否还有新内容（树相同 = 只是重复执行，不该产生空转的发布提交）。 */
export function hasNewContent(prevTree, tree) {
  return !prevTree || prevTree !== tree;
}

/**
 * 组装发布提交信息：
 *   release: <version>
 *
 *   聚合自 master：<N> 个检查点 + <M> 个手工提交。
 *
 *   ### feat
 *   - server: 子会话确认门代理
 *   ### fix
 *   - shell: 更新器暂存复制修复
 *   ### 手工提交
 *   - <手工提交标题>
 *
 *   master-commit: <sha>
 */
export function buildReleaseMessage(version, commits, opts = {}) {
  const checkpoints = commits.filter((c) => classifyCommit(c) === "checkpoint");
  const manual = commits.filter((c) => classifyCommit(c) === "manual");
  const groups = new Map();
  const seen = new Set();
  for (const c of checkpoints) {
    const { type, scope, text } = subjectType(c.subject);
    const key = `${type}\x1f${scope}\x1f${text}`;
    if (seen.has(key)) continue; // 同一件事改了好几轮：发布说明里只留一条
    seen.add(key);
    const line = scope ? `- ${scope}: ${text}` : `- ${text}`;
    if (!groups.has(type)) groups.set(type, []);
    groups.get(type).push(line);
  }
  const lines = [`${RELEASE_PREFIX} ${version}`, ""];
  lines.push(`聚合自 master：${checkpoints.length} 个检查点 + ${manual.length} 个手工提交。`);
  const orderedTypes = [...groups.keys()].sort((a, b) => {
    const ia = TYPE_ORDER.indexOf(a);
    const ib = TYPE_ORDER.indexOf(b);
    return (ia < 0 ? TYPE_ORDER.length : ia) - (ib < 0 ? TYPE_ORDER.length : ib);
  });
  for (const type of orderedTypes) {
    lines.push("", `### ${type === "other" ? "其他" : type}`);
    const items = groups.get(type);
    for (const item of items.slice(0, GROUP_LIMIT)) lines.push(item);
    if (items.length > GROUP_LIMIT) lines.push(`- …另有 ${items.length - GROUP_LIMIT} 项`);
  }
  if (manual.length > 0) {
    lines.push("", "### 手工提交");
    for (const c of manual.slice(0, MANUAL_LIMIT)) lines.push(`- ${c.subject}`);
    if (manual.length > MANUAL_LIMIT) lines.push(`- …另有 ${manual.length - MANUAL_LIMIT} 项`);
  }
  if (opts.extra) lines.push("", opts.extra);
  if (opts.masterCommit) lines.push("", `${MASTER_TRAILER} ${opts.masterCommit}`);
  return lines.join("\n");
}

/** 从发布提交标题里取版本号（`release: 0.2.18` → `0.2.18`；android 版本原样返回）。 */
export function releaseVersion(subject) {
  return (subject ?? "").replace(new RegExp(`^${RELEASE_PREFIX}\\s*`), "").trim();
}

/**
 * 为 --bootstrap 生成干净历史计划：按时间顺序取每个 `release:` 提交，
 * 返回 [{version, tree, date, subject}]（保留原日期，公开历史看起来才是真实时间线）。
 */
export function planCleanHistory(releaseCommits) {
  return releaseCommits.map((c) => ({
    version: releaseVersion(c.subject),
    tree: c.tree,
    date: c.date,
    subject: c.subject,
  }));
}

/**
 * 在 master 历史里找树等于 targetTree 的最近提交（发布提交缺 `master-commit:`
 * 尾注时的回退定位——例如清理历史之前建的发布线）。找不到返回 null。
 */
export function findCommitByTree(logLines, targetTree) {
  for (const line of logLines) {
    const [hash, tree] = line.trim().split(/\s+/);
    if (tree === targetTree) return hash;
  }
  return null;
}

// ---------------------------------------------------------------- 命令行部分

function main(argv) {
  const args = argv.slice(2);
  const flags = new Set(args.filter((a) => a.startsWith("--")));
  const positional = args.filter((a) => !a.startsWith("--"));
  const dryRun = flags.has("--dry-run");
  const noPush = flags.has("--no-push");
  const bootstrap = flags.has("--bootstrap");
  const remote = "origin";
  const publicBranch = "master"; // 公开仓库的默认分支 = 发布线
  const localBranch = "release"; // 本地镜像发布线的分支

  const root = git(process.cwd(), ["rev-parse", "--show-toplevel"]).trim();
  const version = positional[0] || readVersion(root);

  ensureClean(root);
  if (bootstrap) {
    bootstrapRemote(root, remote, version, {
      dryRun,
      publicBranch,
      localBranch,
      pushBackup: flags.has("--push-backup"),
    });
    return;
  }

  const current = git(root, ["rev-parse", "--abbrev-ref", "HEAD"]).trim();
  if (current !== "master") fail(`当前分支是 ${current}，发布线只从 master 出（先切回 master）`);

  // 公开发布线的当前末端：远端 master（清理过历史后它就是发布线）。
  gitOrNull(root, ["fetch", remote, "--prune", "--quiet"]);
  const publicTip = remoteRef(root, remote, `refs/heads/${publicBranch}`);

  if (publicTip) {
    const subject = git(root, ["log", "-1", "--format=%s", publicTip]).trim();
    if (!subject.startsWith(RELEASE_PREFIX)) {
      fail(
        `远端 ${publicBranch} 还是逐轮检查点历史（最新提交「${subject}」不是发布提交）。\n` +
          `先跑一次 node scripts/push-release.mjs --bootstrap 清理（会先备份），之后的发版才能快进推送。`
      );
    }
  }

  const prevTree = publicTip ? git(root, ["rev-parse", `${publicTip}^{tree}`]).trim() : null;
  const prevMaster = publicTip ? locatePreviousMasterCommit(root, publicTip) : null;

  // 上次发布之后的 master 提交（发布提交本身不进说明——它是基线）。
  const range = prevMaster ? `${prevMaster}..HEAD` : "HEAD";
  const commits = parseLog(
    git(root, ["log", "--reverse", "--format=%H%x1f%an%x1f%aI%x1f%s%x1f%b%x1e", range])
  ).filter((c) => classifyCommit(c) !== "release");

  const tree = git(root, ["rev-parse", "HEAD^{tree}"]).trim();
  const headCommit = git(root, ["rev-parse", "HEAD"]).trim();
  const message = buildReleaseMessage(version, commits, { masterCommit: headCommit });

  console.log(`=== 发布聚合 ${version}（推送 → ${remote}/${publicBranch}） ===`);
  console.log(`发布线末端: ${publicTip ? publicTip.slice(0, 12) : "（无，首次发布）"}`);
  console.log(`上次发布对应的 master 提交: ${prevMaster ? prevMaster.slice(0, 12) : "（无，聚合全部历史）"}`);
  console.log(
    `提交: ${commits.length} 个（检查点 ${commits.filter((c) => classifyCommit(c) === "checkpoint").length}` +
      ` + 手工 ${commits.filter((c) => classifyCommit(c) === "manual").length}）`
  );
  console.log(`树:   ${tree.slice(0, 12)}（= master 的树）`);
  console.log("\n--- 提交信息 ---\n" + message + "\n---------------\n");

  if (dryRun) {
    console.log("[dry-run] 未创建提交、未更新 ref、未推送");
    return;
  }

  if (!hasNewContent(prevTree, tree)) {
    console.log(`发布线与 master 的树一致（${tree.slice(0, 12)}），没有新内容——不产生发布提交。`);
    return;
  }

  // 发布提交：树 = master 的树，父 = 上一个发布提交（快进推送）。
  const commitArgs = ["commit-tree", tree, "-m", message];
  if (publicTip) commitArgs.push("-p", publicTip);
  const newCommit = git(root, commitArgs).trim();
  git(root, ["update-ref", `refs/heads/${localBranch}`, newCommit]);
  console.log(`已更新本地 ${localBranch} → ${newCommit.slice(0, 12)}`);

  if (noPush) {
    console.log("[--no-push] 未推送");
    return;
  }
  git(root, ["push", remote, `refs/heads/${localBranch}:refs/heads/${publicBranch}`]);
  console.log(`已推送 ${remote}/${publicBranch}`);
}

/**
 * 发布提交对应的 master 提交：优先读正文尾注，缺失时按「树相等」在 master 历史里
 * 回溯（清理前建的发布线没有尾注）。两者都失败返回 null（退化为聚合全部历史）。
 */
function locatePreviousMasterCommit(root, releaseCommit) {
  const body = git(root, ["log", "-1", "--format=%B", releaseCommit]);
  const fromTrailer = parseMasterTrailer(body);
  if (fromTrailer) {
    const full = gitOrNull(root, ["rev-parse", "--verify", `${fromTrailer}^{commit}`]);
    if (full) return full.trim();
  }
  const tree = git(root, ["rev-parse", `${releaseCommit}^{tree}`]).trim();
  const lines = git(root, ["log", "--format=%H %T", "master"]).split("\n").filter(Boolean);
  return findCommitByTree(lines, tree);
}

/** 版本号唯一源 = shell/package.json（与 scripts/build.mjs 同源）。 */
function readVersion(root) {
  const p = join(root, "shell", "package.json");
  if (!existsSync(p)) fail(`找不到 ${p}（版本号唯一源）`);
  return JSON.parse(readFileSync(p, "utf8")).version;
}

function ensureClean(root) {
  const dirty = git(root, ["status", "--porcelain", "--untracked-files=all"]).trim();
  if (dirty) fail(`工作区不干净，先提交或清理：\n${dirty}`);
}

function remoteRef(root, remote, ref) {
  const out = gitOrNull(root, ["ls-remote", remote, ref]);
  if (!out) return null;
  const line = out.trim().split("\n").find(Boolean);
  return line ? line.split("\t")[0] : null;
}

function fail(message) {
  console.error(`错误：${message}`);
  process.exit(1);
}

// ---------------------------------------------------------------- 一次性清理

/**
 * 把远端历史重写成「初始提交 + 各发布版本」的线性历史。
 *
 * 破坏性：远端 master 的逐轮检查点提交不再存在。因此强制先备份：
 *   1. `git bundle create temp/git-backup-<ts>.bundle --all`（本地全量，含所有分支）；
 *   2. 可选 `--push-backup`：把当前 master 推成远端 `backup/pre-cleanup-<ts>` 分支
 *      （**默认不推**——远端是公开仓库，备份分支会让检查点历史继续公开可查）。
 * 之后构建干净链条（每个历史 release 提交一个快照，保留原提交日期），
 * **本地 master 一动不动**（检查点回滚能力保留），只把本地 release 指到干净链条末端，
 * 再 force push 到远端 master。
 */
function bootstrapRemote(root, remote, version, { dryRun, publicBranch, localBranch, pushBackup }) {
  const masterTip = git(root, ["rev-parse", "master"]).trim();
  const masterTree = git(root, ["rev-parse", "master^{tree}"]).trim();

  // 收集全部 release 提交（按时间正序，带 tree 与日期）。
  const releases = parseLog(
    git(root, ["log", "--reverse", "--format=%H%x1f%an%x1f%aI%x1f%s%x1f%b%x1e", "master"])
  ).filter((c) => c.subject.startsWith(RELEASE_PREFIX));
  const plan = planCleanHistory(
    releases.map((c) => ({ ...c, tree: git(root, ["rev-parse", `${c.hash}^{tree}`]).trim() }))
  );

  const ts = timestamp();
  const bundlePath = join(root, "temp", `git-backup-${ts}.bundle`);
  const backupBranch = `backup/pre-cleanup-${ts}`;

  console.log("=== 远端历史清理（--bootstrap，破坏性） ===");
  console.log(`本地 master: ${masterTip.slice(0, 12)}（${plan.length} 个历史 release 提交，本地不动）`);
  console.log(`备份 bundle: ${bundlePath}`);
  console.log(`备份分支:   ${pushBackup ? `${remote}/${backupBranch}` : "（不推远端；加 --push-backup 才推）"}`);
  console.log(`公开历史:   初始提交 + ${plan.length} 个历史发布 + 本次 ${version} = ${plan.length + 1} 个提交`);

  if (dryRun) {
    console.log("\n[dry-run] 将生成的干净历史（版本 → 树）：");
    for (const r of plan) console.log(`- ${r.version}  ${r.tree.slice(0, 12)}`);
    console.log(`- ${version}  ${masterTree.slice(0, 12)}（当前 master 树）`);
    console.log("[dry-run] 未备份、未改 ref、未推送");
    return;
  }

  // 1. 备份：本地全量 bundle + 远端备份分支
  mkdirSync(join(root, "temp"), { recursive: true });
  git(root, ["bundle", "create", bundlePath, "--all"]);
  console.log(`已备份本地全量历史 → ${bundlePath}`);
  if (pushBackup) {
    git(root, ["push", remote, `refs/heads/master:refs/heads/${backupBranch}`]);
    console.log(`已推送备份分支 → ${remote}/${backupBranch}`);
  }

  // 2. 构建干净链条：每个历史 release 一个提交，最后接当前 master 树
  let parent = null;
  for (const r of plan) {
    const args = ["commit-tree", r.tree, "-m", r.subject];
    if (parent) args.push("-p", parent);
    const env = { ...process.env, GIT_AUTHOR_DATE: r.date, GIT_COMMITTER_DATE: r.date };
    parent = git(root, args, { env }).trim();
  }
  const finalArgs = [
    "commit-tree",
    masterTree,
    "-m",
    buildReleaseMessage(version, [], { masterCommit: masterTip }),
  ];
  if (parent) finalArgs.push("-p", parent);
  const finalCommit = git(root, finalArgs).trim();
  console.log(`干净历史共 ${plan.length + 1} 个提交，末端 ${finalCommit.slice(0, 12)}`);

  // 3. 只动本地 release（本地 master 是检查点历史，绝不动）
  git(root, ["update-ref", `refs/heads/${localBranch}`, finalCommit]);
  console.log(`本地 ${localBranch} → ${finalCommit.slice(0, 12)}（本地 master 未改动）`);

  // 4. force push 公开分支
  git(root, ["push", "--force", remote, `refs/heads/${localBranch}:refs/heads/${publicBranch}`]);
  console.log(`已 force push ${remote}/${publicBranch}`);
  console.log(`
本地回滚能力不受影响：master 完整历史留在本地，全量备份在 ${bundlePath}。`);
  if (pushBackup) console.log(`如需回滚远端：git push --force ${remote} ${backupBranch}:${publicBranch}`);
  else console.log(`如需回滚远端：先 git push ${remote} <本地 master>:refs/heads/${backupBranch}，再 force push 它到 ${publicBranch}`);
}

function timestamp() {
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
}

// 仅在直接执行时跑 main（测试 import 本文件时不触发）。
const invokedDirectly = process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url;
if (invokedDirectly) main(process.argv);
