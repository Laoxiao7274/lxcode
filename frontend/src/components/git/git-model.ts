import type { ProjectMeta, SessionMeta } from "../../shared/types";

export type GitTab = "changes" | "branches" | "worktrees" | "history";
export type GitChangeKind = "modified" | "added" | "deleted";

export interface GitChange {
  path: string;
  kind: GitChangeKind;
  additions: number;
  deletions: number;
  staged: boolean;
  diff: string;
}

export interface GitBranch {
  name: string;
  current: boolean;
  ahead: number;
  behind: number;
  sessionId?: string;
  sessionTitle?: string;
}

export interface GitCommit {
  id: string;
  message: string;
  author: string;
  when: string;
  files: number;
}

export interface GitWorktree {
  id: string;
  sessionId: string;
  title: string;
  branch: string;
  path: string;
  state: "clean" | "changes" | "busy";
  changedFiles: number;
}

export interface GitDemoState {
  changes: GitChange[];
  branches: GitBranch[];
  commits: GitCommit[];
  worktrees: GitWorktree[];
}

const DEMO_CHANGES: GitChange[] = [
  {
    path: "frontend/src/App.tsx",
    kind: "modified",
    additions: 8,
    deletions: 2,
    staged: false,
    diff: "@@ -41,6 +41,10 @@ function AppBody({ source }: { source: AgentSource }) {\n   const { state } = useAgent(source);\n-  const [view, setView] = useState<\"chat\" | \"agents\" | \"catalog\">(\"chat\");\n+  const [view, setView] = useState<\"chat\" | \"agents\" | \"catalog\" | \"git\">(\"chat\");\n+  const [gitProjectId, setGitProjectId] = useState(\"\");\n+\n+  const openGit = () => setView(\"git\");\n\n@@ -123,6 +127,8 @@ const mainView =\n     ) : view === \"agents\" ? (\n       <AgentsPage />\n+    ) : view === \"git\" ? (\n+      <GitWorkbenchPage />\n     ) : (\n       <CatalogPage />\n     );",
  },
  {
    path: "internal/project/worktree.go",
    kind: "modified",
    additions: 14,
    deletions: 3,
    staged: true,
    diff: "@@ -145,6 +145,14 @@ func validateExistingWorktree(repo, path, branch string) error {\n   return nil\n }\n+\n+// ReleaseWorktree removes only a clean checkout and retains its branch.\n+func ReleaseWorktree(repoPath string, wt Worktree) error {\n+  if err := validateExistingWorktree(repo, wt.Path, wt.Branch); err != nil {\n+    return err\n+  }\n+  status, err := gitOutput(wt.Path, \"status\", \"--porcelain\", \"--untracked-files=all\")\n+  if status != \"\" {\n+    return fmt.Errorf(\"worktree has uncommitted changes\")\n+  }\n+  return runGit(repo, \"worktree\", \"remove\", wt.Path)\n+}",
  },
  {
    path: "docs/backend-roadmap.md",
    kind: "added",
    additions: 6,
    deletions: 0,
    staged: false,
    diff: "@@ -149,3 +149,9 @@ ## Multi-session and project worktrees\n - Project sessions receive an isolated worktree on first send.\n+- A clean, idle worktree can be explicitly released.\n+- Release keeps the session branch and SQLite metadata.\n+- Dirty and untracked changes block release.\n+- Resume restores the same branch and working directory.\n+- Archive never deletes a worktree.\n+- Git status and commit management remain project-level UI work.",
  },
];

const DEMO_COMMITS: GitCommit[] = [
  { id: "demo-3", message: "Keep session worktrees isolated", author: "你", when: "今天 14:32", files: 4 },
  { id: "demo-2", message: "Add project-scoped instructions", author: "你", when: "今天 11:08", files: 3 },
  { id: "demo-1", message: "Start multi-session runtime", author: "你", when: "昨天 18:46", files: 12 },
];

export function createGitDemoState(
  project: ProjectMeta,
  sessions: SessionMeta[],
): GitDemoState {
  const projectSessions = sessions.filter((session) => session.workspace === project.id && !session.archived);
  const sessionBranches = projectSessions.map((session) => ({
    session,
    name: `lxcode/session-${session.id}`,
  }));
  const branches = [
    { name: "main", current: true, ahead: 1, behind: 0 },
    { name: "feature/git-workbench", current: false, ahead: 3, behind: 0 },
    ...sessionBranches.map(({ session, name }, index) => ({
      name,
      current: false,
      ahead: index,
      behind: 1,
      sessionId: session.id,
      sessionTitle: session.title,
    })),
  ];
  const worktrees = sessionBranches.map(({ session, name }, index) => {
    const state = index === 0 ? "changes" : index === 1 ? "clean" : "busy";
    return {
      id: session.id,
      sessionId: session.id,
      title: session.title,
      branch: name,
      path: `${project.path.replace(/[\\/]+$/, "")}/.lxcode/worktrees/${session.id}`,
      state,
      changedFiles: state === "changes" ? 2 : state === "busy" ? 1 : 0,
    } satisfies GitWorktree;
  });

  return {
    changes: DEMO_CHANGES.map((change) => ({ ...change })),
    branches,
    commits: DEMO_COMMITS.map((commit) => ({ ...commit })),
    worktrees,
  };
}

export function toggleStaged(changes: GitChange[], path: string): GitChange[] {
  return changes.map((change) => change.path === path ? { ...change, staged: !change.staged } : change);
}

export function selectBranch(branches: GitBranch[], name: string): GitBranch[] {
  return branches.map((branch) => ({ ...branch, current: branch.name === name }));
}

export type SimulatedCommitResult =
  | { ok: true; changes: GitChange[]; commits: GitCommit[] }
  | { ok: false; reason: "empty-message" | "nothing-staged" };

export function simulateCommit(changes: GitChange[], commits: GitCommit[], rawMessage: string): SimulatedCommitResult {
  const message = rawMessage.trim();
  if (!message) return { ok: false, reason: "empty-message" };
  const staged = changes.filter((change) => change.staged);
  if (staged.length === 0) return { ok: false, reason: "nothing-staged" };
  const commit: GitCommit = {
    id: `demo-${commits.length + 1}`,
    message,
    author: "你",
    when: "刚刚",
    files: staged.length,
  };
  return {
    ok: true,
    changes: changes.filter((change) => !change.staged),
    commits: [commit, ...commits],
  };
}
