// Git 管理页的数据映射层：把 git.overview 的 wire 形态折成页面用的 UI 类型。
// 这里**没有**任何演示数据合成——页面展示的是后端只读查询的真实结果
//（git.overview / git.diff）；演示模式的假数据在 agent/demo/index.ts 里。
import type { GitOverview } from "../../shared/types";

export type GitTab = "changes" | "branches" | "worktrees" | "history";
export type GitChangeKind = "modified" | "added" | "deleted" | "untracked";

export interface GitChange {
  path: string;
  kind: GitChangeKind;
}

export interface GitBranch {
  name: string;
  current: boolean;
  ahead: number;
  behind: number;
  /** 会话分支才有（主检出分支缺省）。 */
  sessionId?: string;
  sessionTitle?: string;
  archived?: boolean;
  hasWorktree?: boolean;
  worktreePath?: string;
  dirtyCount?: number;
  merged?: boolean;
}

export interface GitCommit {
  id: string;
  message: string;
  author: string;
  when: string; // ISO（后端给），相对时间由前端折算
}

export interface GitWorktree {
  id: string;
  sessionId: string;
  title: string;
  branch: string;
  /** 工作树目录的真实路径；已释放 = 空串（分支保留、目录不在）。 */
  path: string;
  state: "clean" | "changes" | "released";
  changedFiles: number;
  archived?: boolean;
}

export interface GitWorkbenchState {
  changes: GitChange[];
  branches: GitBranch[];
  commits: GitCommit[];
  worktrees: GitWorktree[];
}

const CHANGE_KINDS: ReadonlySet<string> = new Set(["modified", "added", "deleted", "untracked"]);

/** kindOf 把 wire 上的 kind 字符串折成页面的四类枚举（未知值按 modified 兜底——
 *  后端加了新类别时页面不至于崩，宁可显示成"改动"也不丢条目）。 */
export function kindOf(raw: string): GitChangeKind {
  return CHANGE_KINDS.has(raw) ? (raw as GitChangeKind) : "modified";
}

/** gitOverviewToState 把 git.overview 结果映射成页面状态（纯函数，测试钉它）。
 *
 *  工作树卡片从会话分支派生：有工作树 → clean/changes（按 dirtyCount），
 *  没有工作树（未创建或已释放）→ released——分支和会话记录都还在，如实展示。 */
export function gitOverviewToState(overview: GitOverview): GitWorkbenchState {
  const branches: GitBranch[] = overview.branches.map((info) => ({
    name: info.name,
    current: info.current,
    ahead: info.ahead ?? 0,
    behind: info.behind ?? 0,
    sessionId: info.session_id,
    sessionTitle: info.session_title,
    archived: info.archived,
    hasWorktree: info.has_worktree,
    worktreePath: info.worktree_path,
    dirtyCount: info.dirty_count,
    merged: info.merged,
  }));
  const worktrees: GitWorktree[] = branches
    .filter((branch) => branch.sessionId)
    .map((branch) => ({
      id: branch.sessionId!,
      sessionId: branch.sessionId!,
      title: branch.sessionTitle || "未命名会话",
      branch: branch.name,
      path: branch.worktreePath ?? "",
      state: branch.hasWorktree ? (branch.dirtyCount ? "changes" : "clean") : "released",
      changedFiles: branch.dirtyCount ?? 0,
      archived: branch.archived,
    }));
  return {
    changes: (overview.dirty ?? []).map((change) => ({ path: change.path, kind: kindOf(change.kind) })),
    branches,
    commits: (overview.commits ?? []).map((commit) => ({
      id: commit.hash,
      message: commit.message,
      author: commit.author,
      when: commit.when,
    })),
    worktrees,
  };
}

/** shortHash 把 40 位提交哈希折成 7 位短码（老提交对象模型同款长度）。 */
export function shortHash(hash: string): string {
  return hash.length > 7 ? hash.slice(0, 7) : hash;
}

/** countDiffLines 数 diff 文本里的新增/删除行（+/- 前缀；@@ hunk 头与
 *  "+++ / ---" 文件头不计）。给文件行的 ± 统计与差异面板摘要用——真实数据
 *  没有 staged/additions 字段，能算的只有 diff 本身。 */
export function countDiffLines(diff: string): { additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const line of diff.split("\n")) {
    if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("@@")) continue;
    if (line.startsWith("+")) additions += 1;
    else if (line.startsWith("-")) deletions += 1;
  }
  return { additions, deletions };
}

/** relativeWhen 把 ISO 时间折成简短相对文案（后端只给 ISO，展示层折算）。 */
export function relativeWhen(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const ms = Date.now() - t;
  const minute = 60_000;
  if (ms < minute) return "刚刚";
  if (ms < 60 * minute) return `${Math.floor(ms / minute)} 分钟前`;
  if (ms < 24 * 60 * minute) return `${Math.floor(ms / (60 * minute))} 小时前`;
  if (ms < 7 * 24 * 60 * minute) return `${Math.floor(ms / (24 * 60 * minute))} 天前`;
  const d = new Date(t);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** 未跟踪文件的 kind 徽标字母（modified→M / added→A / deleted→D / untracked→U）。 */
export function kindGlyph(kind: GitChangeKind): string {
  switch (kind) {
    case "added": return "A";
    case "deleted": return "D";
    case "untracked": return "U";
    default: return "M";
  }
}
