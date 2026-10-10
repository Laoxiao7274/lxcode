// Git 管理页：项目主检出的**只读**工作台（真实数据——git.overview / git.diff）。
//
// 纪律：本页不提供任何写主检出的操作（不暂存 / 不提交 / 不切换分支——
// 「一会话一分支」架构下分支切换语义不存在；提交由会话内的检查点提交与
// workspace_sync 负责）。唯一的写操作 = 释放会话工作树（session.worktree.release，
// 弹窗确认，脏目录会被后端拒绝）。
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import type { AgentSource, GitOverview, ProjectMeta } from "../../shared/types";
import { Button, Select } from "../form";
import {
  countDiffLines,
  gitOverviewToState,
  kindGlyph,
  relativeWhen,
  shortHash,
  type GitBranch,
  type GitChange,
  type GitTab,
  type GitWorkbenchState,
  type GitWorktree,
} from "./git-model";

const EMPTY_STATE: GitWorkbenchState = { changes: [], branches: [], commits: [], worktrees: [] };
const TABS: Array<{ id: GitTab; label: string }> = [
  { id: "changes", label: "变更" },
  { id: "branches", label: "分支" },
  { id: "worktrees", label: "工作树" },
  { id: "history", label: "历史" },
];

export function GitWorkbenchPage({
  source,
  projects,
  projectId,
  onProjectChange,
  onOpenSession,
}: {
  source: AgentSource;
  projects: ProjectMeta[];
  projectId: string;
  onProjectChange: (id: string) => void;
  onOpenSession: (id: string) => void;
}) {
  const project = projects.find((item) => item.id === projectId);
  const [tab, setTab] = useState<GitTab>("changes");
  const [displayedTab, setDisplayedTab] = useState<GitTab>("changes");
  const tabPanelRef = useRef<HTMLDivElement>(null);
  const firstTabDisplay = useRef(true);

  // 真实数据：选项目即拉取 git.overview（只读查询），项目切换重拉。
  const [overview, setOverview] = useState<GitOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [reloadTick, setReloadTick] = useState(0);

  const load = useCallback(async () => {
    if (!projectId) {
      setOverview(null);
      return;
    }
    setLoading(true);
    setError("");
    try {
      setOverview(await source.gitOverview(projectId));
    } catch (e) {
      setOverview(null);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [source, projectId]);

  useEffect(() => { void load(); }, [load, reloadTick]);

  const state = useMemo(() => (overview ? gitOverviewToState(overview) : EMPTY_STATE), [overview]);
  const changes = state.changes;
  const branches = state.branches;
  const commits = state.commits;
  const worktrees = state.worktrees;
  const currentBranchInfo = branches.find((branch) => branch.current);
  const currentBranch = currentBranchInfo?.name ?? "—";

  const [selectedPath, setSelectedPath] = useState("");
  // 清单变化时保持选中项有效（文件被提交/撤销后自动落到第一条）。
  useEffect(() => {
    if (selectedPath && changes.some((change) => change.path === selectedPath)) return;
    setSelectedPath(changes[0]?.path ?? "");
  }, [changes, selectedPath]);
  const selectedChange = changes.find((change) => change.path === selectedPath);

  useLayoutEffect(() => {
    if (tab === displayedTab) return;
    const outgoing = tabPanelRef.current;
    if (!outgoing || !motionAllowed()) {
      setDisplayedTab(tab);
      return;
    }
    const context = gsap.context(() => {
      gsap.to(outgoing, {
        opacity: 0,
        y: -4,
        duration: 0.12,
        ease: "power1.in",
        onComplete: () => setDisplayedTab(tab),
      });
    }, outgoing);
    return () => { context.revert(); };
  }, [tab, displayedTab]);

  useLayoutEffect(() => {
    if (firstTabDisplay.current) {
      firstTabDisplay.current = false;
      return;
    }
    const incoming = tabPanelRef.current;
    if (!incoming || !motionAllowed()) return;
    const context = gsap.context(() => {
      gsap.fromTo(incoming, { opacity: 0, y: 5 }, {
        opacity: 1,
        y: 0,
        duration: 0.18,
        ease: "power2.out",
        clearProps: "transform,opacity",
      });
    }, incoming);
    return () => { context.revert(); };
  }, [displayedTab]);

  return (
    <div className="git-page" data-git-page>
      <header className="git-head">
        <div className="git-heading">
          <div className="git-eyebrow">PROJECT / VERSION CONTROL</div>
          <h1 className="git-title">Git 管理</h1>
          <p className="git-subtitle">查看项目变更、会话工作树与提交历史</p>
        </div>
        <div className="git-project-box">
          <span className="git-project-label">项目仓库</span>
          {projects.length > 0 ? (
            <div className="git-project-select" data-git-project-select>
              <Select
                value={project?.id ?? ""}
                onChange={onProjectChange}
                groups={[{ options: projects.map((item) => ({ value: item.id, label: item.name, desc: item.path })) }]}
                placeholder="选择项目"
                ariaLabel="选择 Git 项目"
              />
            </div>
          ) : (
            <span className="git-no-project">暂无项目</span>
          )}
          {project && <code className="git-project-path" title={project.path}>{project.path}</code>}
        </div>
      </header>

      <div className="git-readonly-note" role="note">
        只读视图：这里展示项目仓库的真实状态；提交请在会话里进行（每轮自动检查点，或对会话说「帮我提交」）。
      </div>

      {!project ? (
        <div className="git-empty" data-git="empty">
          <div className="git-empty-icon" aria-hidden>
            <BranchGlyph />
          </div>
          <h2>先添加一个项目仓库</h2>
          <p>从侧栏「项目」旁的 + 注册本地仓库，然后回来查看 Git 工作台。</p>
        </div>
      ) : loading && !overview ? (
        <div className="git-empty" data-git="loading">
          <div className="git-empty-icon" aria-hidden><BranchGlyph /></div>
          <h2>正在读取仓库状态…</h2>
          <p>正在向工作树查询分支、变更与提交历史。</p>
        </div>
      ) : error ? (
        <div className="git-empty" data-git="error">
          <div className="git-empty-icon" aria-hidden><BranchGlyph /></div>
          <h2>无法读取仓库状态</h2>
          <p className="git-error" role="alert">{error}</p>
          <Button type="button" variant="ghost" onClick={() => setReloadTick((n) => n + 1)}>重试</Button>
        </div>
      ) : (
        <>
          <section className="git-repo-strip" aria-label="仓库概览">
            <div className="git-current-branch">
              <BranchGlyph />
              <span className="git-current-label">当前分支</span>
              <strong>{currentBranch}</strong>
              {(currentBranchInfo?.ahead ?? 0) > 0 || (currentBranchInfo?.behind ?? 0) > 0 ? (
                <span className="git-branch-ahead">↑ {currentBranchInfo?.ahead} ↓ {currentBranchInfo?.behind}</span>
              ) : null}
            </div>
            <div className="git-repo-metrics">
              <span><b>{changes.length}</b> 个未提交文件</span>
              <span><b>{worktrees.filter((wt) => wt.state !== "released").length}</b> 个会话工作树</span>
            </div>
          </section>

          <div className="git-tabs" role="group" aria-label="Git 工作台视图">
            {TABS.map((item) => (
              <button
                key={item.id}
                type="button"
                className={"git-tab" + (tab === item.id ? " on" : "")}
                aria-pressed={tab === item.id}
                onClick={() => setTab(item.id)}
                data-git-tab={item.id}
              >
                {item.label}
                {item.id === "changes" && changes.length > 0 && <span className="git-tab-count">{changes.length}</span>}
              </button>
            ))}
          </div>

          <div className="git-tab-panel" ref={tabPanelRef} data-git-tab-panel={displayedTab}>
          {displayedTab === "changes" && (
            <div className="git-changes-layout" data-git="changes">
              <section className="git-panel git-changes-panel" aria-label="文件变更">
                <div className="git-panel-head">
                  <div>
                    <h2>未提交变更</h2>
                    <p>主检出工作区的真实状态</p>
                  </div>
                  <span className="git-count-pill">{changes.length}</span>
                </div>
                {changes.length === 0 ? (
                  <div className="git-empty-inline">工作区干净，当前没有待提交文件。</div>
                ) : (
                  <ChangeList changes={changes} selectedPath={selectedPath} onSelect={setSelectedPath} />
                )}
                <div className="git-commit-box">
                  <div className="git-commit-hint">
                    这里不做暂存与提交：主检出由会话负责——每轮对话结束会自动提交检查点，
                    也可以在会话里说「帮我提交」走合并流程。
                  </div>
                </div>
              </section>
              <DiffPanel source={source} projectId={projectId} change={selectedChange} />
            </div>
          )}

          {displayedTab === "branches" && (
            <section className="git-panel git-list-panel" data-git="branches" aria-label="分支列表">
              <div className="git-panel-head">
                <div><h2>分支</h2><p>当前分支 + 各会话分支（一会话一分支，不支持在此切换）</p></div>
                <span className="git-count-pill">{branches.length}</span>
              </div>
              <div className="git-branch-list">
                {branches.map((branch) => (
                  <BranchRow key={branch.name} branch={branch} onOpenSession={onOpenSession} />
                ))}
              </div>
            </section>
          )}

          {displayedTab === "worktrees" && (
            <WorktreesPanel
              worktrees={worktrees}
              source={source}
              onOpenSession={onOpenSession}
              onReleased={() => setReloadTick((n) => n + 1)}
            />
          )}

          {displayedTab === "history" && (
            <section className="git-panel git-list-panel" data-git="history" aria-label="提交历史">
              <div className="git-panel-head">
                <div><h2>提交历史</h2><p>{currentBranch} · 最近 {commits.length} 条</p></div>
                <span className="git-count-pill">{commits.length}</span>
              </div>
              {commits.length === 0 ? (
                <div className="git-empty-inline">还没有任何提交。</div>
              ) : (
                <ol className="git-history-list">
                  {commits.map((commit) => (
                    <li className="git-history-item" key={commit.id}>
                      <span className="git-history-node" aria-hidden />
                      <div className="git-history-body">
                        <strong>{commit.message}</strong>
                        <div><code>{shortHash(commit.id)}</code><span>{commit.author}</span><span>{relativeWhen(commit.when)}</span></div>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </section>
          )}
          </div>
        </>
      )}
    </div>
  );
}

// ---------- 变更标签页 ----------

function ChangeList({
  changes,
  selectedPath,
  onSelect,
}: {
  changes: GitChange[];
  selectedPath: string;
  onSelect: (path: string) => void;
}) {
  return (
    <div className="git-change-group">
      {changes.map((change) => (
        <div className={"git-file-row" + (selectedPath === change.path ? " selected" : "")} key={change.path}>
          <button type="button" className="git-file-select" onClick={() => onSelect(change.path)} aria-pressed={selectedPath === change.path}>
            <span className={`git-file-kind ${change.kind}`} aria-hidden>{kindGlyph(change.kind)}</span>
            <span className="git-file-path" title={change.path}>{change.path}</span>
            <span className="git-file-kind-label">{kindLabel(change.kind)}</span>
          </button>
        </div>
      ))}
    </div>
  );
}

function kindLabel(kind: GitChange["kind"]): string {
  switch (kind) {
    case "added": return "新增";
    case "deleted": return "删除";
    case "untracked": return "未跟踪";
    default: return "修改";
  }
}

/** DiffPanel 懒加载单个文件的真实差异（git.diff）——点开文件才拉取，
 *  不随 overview 一起拉（diff 可能很大，且一次只看一个文件）。 */
function DiffPanel({ source, projectId, change }: { source: AgentSource; projectId: string; change?: GitChange }) {
  const [diff, setDiff] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setDiff("");
    setError("");
    if (!change || !projectId) return;
    let alive = true;
    setLoading(true);
    source.gitDiff(projectId, change.path)
      .then((text) => { if (alive) setDiff(text); })
      .catch((e) => { if (alive) setError(e instanceof Error ? e.message : String(e)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [source, projectId, change?.path]);

  const counts = countDiffLines(diff);
  return (
    <section className="git-panel git-diff-panel" aria-label="差异预览">
      <div className="git-panel-head git-diff-head">
        <div>
          <h2>差异预览</h2>
          <p>{change?.path ?? "选择一个文件查看差异"}</p>
        </div>
        {change && (counts.additions > 0 || counts.deletions > 0) && (
          <span className="git-diff-summary"><i>+{counts.additions}</i><b>−{counts.deletions}</b></span>
        )}
      </div>
      {!change ? (
        <div className="git-diff-empty">从左侧选择一个文件查看它的未提交差异。</div>
      ) : loading ? (
        <div className="git-diff-empty" data-git="diff-loading">正在读取差异…</div>
      ) : error ? (
        <div className="git-diff-empty git-error" role="alert">{error}</div>
      ) : diff ? (
        <div className="git-diff-content" role="region" aria-label={`${change.path} 的差异`}>
          {diff.split("\n").map((line, index) => {
            const kind = line.startsWith("+++") || line.startsWith("---") ? "meta"
              : line.startsWith("+") ? "add"
              : line.startsWith("-") ? "del"
              : line.startsWith("@@") ? "hunk" : "context";
            return <div key={`${index}-${line}`} className={`git-diff-line ${kind}`}><span>{line}</span></div>;
          })}
        </div>
      ) : (
        <div className="git-diff-empty">这个文件没有未提交的差异（可能已暂存或内容未变）。</div>
      )}
    </section>
  );
}

// ---------- 分支标签页 ----------

function BranchRow({ branch, onOpenSession }: { branch: GitBranch; onOpenSession: (id: string) => void }) {
  const [expanded, setExpanded] = useState(false);
  if (!branch.sessionId) {
    // 主检出当前分支：纯展示（不提供切换——切换语义在本架构下不存在）。
    return (
      <div className={"git-branch-row current" + (branch.archived ? "" : "")} data-git-current-branch={branch.name}>
        <BranchGlyph />
        <span className="git-branch-name">{branch.name}</span>
        {(branch.ahead > 0 || branch.behind > 0) && <span className="git-branch-sync">↑ {branch.ahead}　↓ {branch.behind}</span>}
        <span className="git-current-badge">当前</span>
      </div>
    );
  }
  const sessionTitle = branch.sessionTitle || "未命名会话";
  return (
    <div className="git-branch-session-wrap">
      <div
        className="git-branch-session-row"
        data-git-session-branch={branch.sessionId}
        role="button"
        tabIndex={0}
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setExpanded((value) => !value); } }}
      >
        <BranchGlyph />
        <div className="git-branch-session-copy">
          <span className="git-branch-name">{branch.name}</span>
          <span className="git-branch-session-title" title={sessionTitle}>
            会话：{sessionTitle}{branch.archived ? "（已归档）" : ""}{branch.merged ? " · 已并入主检出" : ""}
          </span>
        </div>
        <div className="git-branch-session-actions">
          <span className="git-branch-sync">↑ {branch.ahead}　↓ {branch.behind}</span>
          <Button
            variant="ghost"
            className="git-branch-open"
            data-git-branch-session={branch.sessionId}
            aria-label={`打开会话：${sessionTitle}`}
            onClick={(e) => { e.stopPropagation(); onOpenSession(branch.sessionId!); }}
          >
            打开会话
          </Button>
        </div>
      </div>
      {expanded && (
        <div className="git-branch-detail" data-git-branch-detail={branch.sessionId}>
          <span>工作树：{branch.hasWorktree ? "存在" : "无（未创建或已释放，分支保留）"}</span>
          {branch.hasWorktree && <span>未提交改动：{branch.dirtyCount ?? 0} 个文件</span>}
          <span>已并入主检出：{branch.merged ? "是" : "否"}</span>
        </div>
      )}
    </div>
  );
}

// ---------- 工作树标签页 ----------

function WorktreesPanel({
  worktrees,
  source,
  onOpenSession,
  onReleased,
}: {
  worktrees: GitWorktree[];
  source: AgentSource;
  onOpenSession: (id: string) => void;
  onReleased: () => void;
}) {
  const [releaseTarget, setReleaseTarget] = useState<GitWorktree | null>(null);
  return (
    <section className="git-panel git-list-panel" data-git="worktrees" aria-label="会话工作树">
      <div className="git-panel-head">
        <div><h2>会话工作树</h2><p>每个项目会话使用独立分支和目录</p></div>
        <span className="git-count-pill">{worktrees.length}</span>
      </div>
      {worktrees.length === 0 ? (
        <div className="git-empty-inline">该项目还没有会话分支。首次在项目会话中发送消息后创建。</div>
      ) : (
        <div className="git-worktree-list">
          {worktrees.map((worktree) => (
            <WorktreeCard
              key={worktree.id}
              worktree={worktree}
              onOpen={() => onOpenSession(worktree.sessionId)}
              onRelease={() => setReleaseTarget(worktree)}
            />
          ))}
        </div>
      )}
      {releaseTarget && (
        <ReleaseDialog
          worktree={releaseTarget}
          source={source}
          onClose={() => setReleaseTarget(null)}
          onReleased={onReleased}
        />
      )}
    </section>
  );
}

function WorktreeCard({ worktree, onOpen, onRelease }: { worktree: GitWorktree; onOpen: () => void; onRelease: () => void }) {
  const status = worktree.state === "released"
    ? "已释放（分支保留）"
    : worktree.state === "changes"
      ? `${worktree.changedFiles} 个文件有改动`
      : "干净";
  return (
    <article className="git-worktree-card" data-git-worktree={worktree.sessionId} data-git-worktree-state={worktree.state}>
      <div className="git-worktree-top">
        <strong>{worktree.title}</strong>
        <span className={`git-worktree-state ${worktree.state}`}>{status}</span>
      </div>
      <code className="git-worktree-branch">{worktree.branch}</code>
      {worktree.path
        ? <code className="git-worktree-path" title={worktree.path}>{worktree.path}</code>
        : <code className="git-worktree-path git-worktree-path-empty">工作树目录已释放；恢复会话时会从原分支重建</code>}
      <div className="git-worktree-foot">
        <span>会话 {worktree.sessionId.slice(-8)}</span>
        <div className="git-worktree-actions">
          {worktree.state !== "released" && (
            <button type="button" className="git-worktree-release" data-git-release={worktree.sessionId} onClick={onRelease}>
              释放工作区
            </button>
          )}
          <button type="button" onClick={onOpen}>打开会话</button>
        </div>
      </div>
    </article>
  );
}

/** ReleaseDialog 复用 session.worktree.release 与侧栏「释放工作区」弹层的确认
 *  模式与文案：只移除检出目录，会话记录和 Git 分支保留；脏目录会被后端拒绝。 */
function ReleaseDialog({ worktree, source, onClose, onReleased }: {
  worktree: GitWorktree;
  source: AgentSource;
  onClose: () => void;
  onReleased: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");

  const close = () => { if (!busy) onClose(); };

  const confirm = async () => {
    setBusy(true);
    setError("");
    try {
      await source.releaseWorktree(worktree.sessionId);
      setDone(true);
      onReleased();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="proj-add-mask"
      role="alertdialog"
      aria-modal="true"
      aria-label="释放工作区"
      onKeyDown={(e) => { e.stopPropagation(); if (e.key === "Escape" && !busy) close(); }}
      onPointerDown={(e) => { e.stopPropagation(); if (e.target === e.currentTarget) close(); }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="proj-add session-release">
        <div className="proj-add-head">
          <span className="proj-add-title">{done ? "工作区已释放" : "释放工作区？"}</span>
          {!busy && (
            <button type="button" className="proj-add-close" aria-label="关闭" onClick={close}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                <path d="m18 6-12 12M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>
        <div className="proj-add-body">
          {done ? (
            <p className="proj-field-hint">会话记录和分支已保留。下次恢复该会话时，会从原分支重建工作区。</p>
          ) : (
            <>
              <p className="proj-field-hint">只移除检出目录；会话记录和 Git 分支会保留，不会自动合并到主项目。</p>
              <p className="proj-field-hint">未提交或未跟踪的文件会阻止释放。忽略文件（例如依赖和构建缓存）会随工作区目录一起删除。</p>
              <code className="git-release-path">{worktree.path || worktree.branch}</code>
              {error && <div className="proj-add-error git-error" role="alert">{error}</div>}
            </>
          )}
        </div>
        <div className="proj-add-foot">
          {done ? (
            <button type="button" className="proj-add-ok" autoFocus onClick={close}>完成</button>
          ) : (
            <>
              <button type="button" className="proj-add-cancel" disabled={busy} autoFocus onClick={close}>取消</button>
              <button type="button" className="proj-add-ok" disabled={busy} aria-busy={busy} data-git-release-confirm onClick={() => void confirm()}>
                {busy ? "正在释放…" : "释放工作区"}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function BranchGlyph() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="6" cy="6" r="2" />
      <circle cx="18" cy="6" r="2" />
      <circle cx="6" cy="18" r="2" />
      <path d="M6 8v8M18 8a6 6 0 0 1-6 6H8" />
    </svg>
  );
}
