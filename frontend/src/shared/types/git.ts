// Git 管理页（git.overview / git.diff 的 wire 形态；全部只读查询）。


/** 主检出的一条未提交/未跟踪变更。kind: modified | added | deleted | untracked。 */
export interface GitChange {
  path: string;
  kind: string;
}

/** 一条分支：主检出当前分支（current=true）或某会话分支（带会话元数据）。 */
export interface GitBranchInfo {
  name: string;
  current: boolean;
  ahead: number;
  behind: number;
  /** 会话分支才有（主检出分支整键缺席）。 */
  session_id?: string;
  session_title?: string;
  archived?: boolean;
  has_worktree?: boolean;
  worktree_path?: string;
  dirty_count?: number;
  merged?: boolean;
}

/** 一条提交（git log 条目；when 为 ISO，相对时间由前端折算）。 */
export interface GitCommitInfo {
  hash: string;
  message: string;
  author: string;
  when: string;
}

/** git.overview 结果：项目主检出的只读快照。 */
export interface GitOverview {
  path: string;
  branch: string;
  dirty: GitChange[];
  branches: GitBranchInfo[];
  commits: GitCommitInfo[];
}
