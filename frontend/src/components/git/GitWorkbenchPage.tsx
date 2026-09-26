import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import type { ProjectMeta, SessionMeta } from "../../shared/types";
import { Button, Select, Textarea } from "../form";
import {
  createGitDemoState,
  selectBranch,
  simulateCommit,
  toggleStaged,
  type GitBranch,
  type GitChange,
  type GitCommit,
  type GitTab,
  type GitWorktree,
} from "./git-model";

const EMPTY_STATE = { changes: [] as GitChange[], branches: [] as GitBranch[], commits: [] as GitCommit[], worktrees: [] as GitWorktree[] };
const TABS: Array<{ id: GitTab; label: string }> = [
  { id: "changes", label: "变更" },
  { id: "branches", label: "分支" },
  { id: "worktrees", label: "工作树" },
  { id: "history", label: "历史" },
];

export function GitWorkbenchPage({
  projects,
  projectId,
  sessions,
  onProjectChange,
  onOpenSession,
}: {
  projects: ProjectMeta[];
  projectId: string;
  sessions: SessionMeta[];
  onProjectChange: (id: string) => void;
  onOpenSession: (id: string) => void;
}) {
  const project = projects.find((item) => item.id === projectId);
  const demo = useMemo(
    () => project ? createGitDemoState(project, sessions) : EMPTY_STATE,
    [project?.id, project?.path, sessions],
  );
  const [tab, setTab] = useState<GitTab>("changes");
  const [displayedTab, setDisplayedTab] = useState<GitTab>("changes");
  const tabPanelRef = useRef<HTMLDivElement>(null);
  const firstTabDisplay = useRef(true);
  const [changes, setChanges] = useState(demo.changes);
  const [branches, setBranches] = useState(demo.branches);
  const [commits, setCommits] = useState(demo.commits);
  const [selectedPath, setSelectedPath] = useState(demo.changes[0]?.path ?? "");
  const [commitMessage, setCommitMessage] = useState("");
  const [commitError, setCommitError] = useState("");
  const selectedChange = changes.find((change) => change.path === selectedPath);
  const staged = changes.filter((change) => change.staged);
  const unstaged = changes.filter((change) => !change.staged);
  const currentBranchInfo = branches.find((branch) => branch.current);
  const currentBranch = currentBranchInfo?.name ?? "—";
  const worktrees = demo.worktrees;

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

  const commit = () => {
    const result = simulateCommit(changes, commits, commitMessage);
    if (!result.ok) {
      setCommitError(result.reason === "empty-message" ? "请填写提交说明。" : "先暂存至少一个文件再提交。");
      return;
    }
    setChanges(result.changes);
    setCommits(result.commits);
    setSelectedPath(result.changes[0]?.path ?? "");
    setCommitMessage("");
    setCommitError("");
  };

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

      <div className="git-demo-banner" role="note">
        <span className="git-demo-dot" aria-hidden />
        原型数据 · 页面操作不会执行 Git 命令或修改仓库
      </div>

      {!project ? (
        <div className="git-empty" data-git="empty">
          <div className="git-empty-icon" aria-hidden>
            <BranchGlyph />
          </div>
          <h2>先添加一个项目仓库</h2>
          <p>从侧栏「项目」旁的 + 注册本地仓库，然后回来查看 Git 工作台。</p>
        </div>
      ) : (
        <>
          <section className="git-repo-strip" aria-label="仓库概览">
            <div className="git-current-branch">
              <BranchGlyph />
              <span className="git-current-label">当前分支</span>
              <strong>{currentBranch}</strong>
              <span className="git-branch-ahead">↑ {currentBranchInfo?.ahead ?? 0} ↓ {currentBranchInfo?.behind ?? 0}</span>
            </div>
            <div className="git-repo-metrics">
              <span><b>{changes.length}</b> 个未提交文件</span>
              <span><b>{worktrees.length}</b> 个会话工作树</span>
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
                    <h2>文件变更</h2>
                    <p>暂存后才会进入提交</p>
                  </div>
                  <span className="git-count-pill">{changes.length}</span>
                </div>
                {changes.length === 0 ? (
                  <div className="git-empty-inline">工作区干净，当前没有待提交文件。</div>
                ) : (
                  <>
                    {staged.length > 0 && <ChangeGroup title="已暂存" changes={staged} selectedPath={selectedPath} onSelect={setSelectedPath} onToggle={(path) => setChanges((current) => toggleStaged(current, path))} />}
                    {unstaged.length > 0 && <ChangeGroup title="未暂存" changes={unstaged} selectedPath={selectedPath} onSelect={setSelectedPath} onToggle={(path) => setChanges((current) => toggleStaged(current, path))} />}
                  </>
                )}
                <div className="git-commit-box">
                  <div className="git-commit-label">提交说明</div>
                  <Textarea
                    value={commitMessage}
                    onChange={(value) => { setCommitMessage(value); setCommitError(""); }}
                    placeholder="描述这次改动…"
                    className="git-commit-input"
                    ariaLabel="提交说明"
                    rows={3}
                  />
                  {commitError && <div className="git-error" role="alert">{commitError}</div>}
                  <div className="git-commit-hint">
                    {staged.length === 0 ? "先暂存至少一个文件后才能提交。" : !commitMessage.trim() ? "填写提交说明后启用提交。" : "只提交已暂存文件；本次操作为本地演示。"}
                  </div>
                  <Button type="button" variant="primary" className="git-commit-button" data-git="commit" disabled={!project || staged.length === 0 || !commitMessage.trim()} onClick={commit}>
                    提交 {staged.length > 0 ? `${staged.length} 个已暂存文件` : "已暂存的文件"}
                  </Button>
                </div>
              </section>
              <DiffPanel change={selectedChange} />
            </div>
          )}

          {displayedTab === "branches" && (
            <section className="git-panel git-list-panel" data-git="branches" aria-label="分支列表">
              <div className="git-panel-head">
                <div><h2>本地分支</h2><p>项目分支可模拟切换；会话分支绑定对应工作树</p></div>
                <span className="git-count-pill">{branches.length}</span>
              </div>
              <div className="git-branch-list">
                {branches.map((branch) => {
                  const sessionId = branch.sessionId;
                  if (sessionId) {
                    const sessionTitle = branch.sessionTitle || "未命名会话";
                    return (
                      <div className="git-branch-session-row" key={branch.name} data-git-session-branch={sessionId}>
                        <BranchGlyph />
                        <div className="git-branch-session-copy">
                          <span className="git-branch-name">{branch.name}</span>
                          <span className="git-branch-session-title" title={sessionTitle}>会话：{sessionTitle}</span>
                        </div>
                        <div className="git-branch-session-actions">
                          <span className="git-branch-sync">↑ {branch.ahead}　↓ {branch.behind}</span>
                          <Button
                            variant="ghost"
                            className="git-branch-open"
                            data-git-branch-session={sessionId}
                            aria-label={`打开会话：${sessionTitle}`}
                            onClick={() => onOpenSession(sessionId)}
                          >
                            打开会话
                          </Button>
                        </div>
                      </div>
                    );
                  }
                  return (
                    <button
                      type="button"
                      key={branch.name}
                      className={"git-branch-row" + (branch.current ? " current" : "")}
                      aria-pressed={branch.current}
                      onClick={() => setBranches((current) => selectBranch(current, branch.name))}
                    >
                      <BranchGlyph />
                      <span className="git-branch-name">{branch.name}</span>
                      <span className="git-branch-sync">↑ {branch.ahead}　↓ {branch.behind}</span>
                      {branch.current && <span className="git-current-badge">当前</span>}
                    </button>
                  );
                })}
              </div>
              <div className="git-prototype-note">项目分支切换、ahead/behind 与会话分支状态均为演示值；不会 checkout 真实文件。</div>
            </section>
          )}

          {displayedTab === "worktrees" && (
            <section className="git-panel git-list-panel" data-git="worktrees" aria-label="会话工作树">
              <div className="git-panel-head">
                <div><h2>会话工作树</h2><p>每个项目会话使用独立分支和目录</p></div>
                <span className="git-count-pill">{worktrees.length}</span>
              </div>
              {worktrees.length === 0 ? (
                <div className="git-empty-inline">该项目还没有关联的会话工作树。首次在项目会话中发送消息后创建。</div>
              ) : (
                <div className="git-worktree-list">
                  {worktrees.map((worktree) => (
                    <WorktreeCard key={worktree.id} worktree={worktree} onOpen={() => onOpenSession(worktree.sessionId)} />
                  ))}
                </div>
              )}
              <div className="git-prototype-note">状态来自演示数据。真实工作区的安全释放入口仍在会话行菜单中。</div>
            </section>
          )}

          {displayedTab === "history" && (
            <section className="git-panel git-list-panel" data-git="history" aria-label="提交历史">
              <div className="git-panel-head">
                <div><h2>提交历史</h2><p>{currentBranch} · 最近提交</p></div>
                <span className="git-count-pill">{commits.length}</span>
              </div>
              <ol className="git-history-list">
                {commits.map((commit) => (
                  <li className="git-history-item" key={commit.id}>
                    <span className="git-history-node" aria-hidden />
                    <div className="git-history-body">
                      <strong>{commit.message}</strong>
                      <div><code>{commit.id}</code><span>{commit.author}</span><span>{commit.when}</span></div>
                    </div>
                    <span className="git-history-files">{commit.files} 个文件</span>
                  </li>
                ))}
              </ol>
              {commits.length === 0 && <div className="git-empty-inline">还没有演示提交记录。</div>}
            </section>
          )}
          </div>
        </>
      )}
    </div>
  );
}

function ChangeGroup({
  title,
  changes,
  selectedPath,
  onSelect,
  onToggle,
}: {
  title: string;
  changes: GitChange[];
  selectedPath: string;
  onSelect: (path: string) => void;
  onToggle: (path: string) => void;
}) {
  return (
    <div className="git-change-group">
      <div className="git-group-title">{title}<span>{changes.length}</span></div>
      {changes.map((change) => (
        <div className={"git-file-row" + (selectedPath === change.path ? " selected" : "")} key={change.path}>
          <button type="button" className="git-file-select" onClick={() => onSelect(change.path)} aria-pressed={selectedPath === change.path}>
            <span className={`git-file-kind ${change.kind}`} aria-hidden>{change.kind === "added" ? "A" : change.kind === "deleted" ? "D" : "M"}</span>
            <span className="git-file-path" title={change.path}>{change.path}</span>
            <span className="git-file-stats"><i>+{change.additions}</i><b>−{change.deletions}</b></span>
          </button>
          <button
            type="button"
            className="git-stage-toggle"
            aria-label={`${change.staged ? "取消暂存" : "暂存"} ${change.path}`}
            title={change.staged ? "取消暂存" : "暂存文件"}
            onClick={() => onToggle(change.path)}
          >
            {change.staged ? "−" : "+"}
          </button>
        </div>
      ))}
    </div>
  );
}

function DiffPanel({ change }: { change?: GitChange }) {
  return (
    <section className="git-panel git-diff-panel" aria-label="差异预览">
      <div className="git-panel-head git-diff-head">
        <div>
          <h2>差异预览</h2>
          <p>{change?.path ?? "选择一个文件查看差异"}</p>
        </div>
        {change && <span className="git-diff-summary"><i>+{change.additions}</i><b>−{change.deletions}</b></span>}
      </div>
      {change ? (
        <div className="git-diff-content" role="region" aria-label={`${change.path} 的差异`}>
          {change.diff.split("\n").map((line, index) => {
            const kind = line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : line.startsWith("@@") ? "hunk" : "context";
            return <div key={`${index}-${line}`} className={`git-diff-line ${kind}`}><span>{line}</span></div>;
          })}
        </div>
      ) : (
        <div className="git-diff-empty">提交后变更会从工作区移除，历史中保留提交摘要。</div>
      )}
    </section>
  );
}

function WorktreeCard({ worktree, onOpen }: { worktree: GitWorktree; onOpen: () => void }) {
  const status = worktree.state === "busy" ? "会话运行中" : worktree.state === "changes" ? `${worktree.changedFiles} 个文件有改动` : "干净";
  return (
    <article className="git-worktree-card">
      <div className="git-worktree-top">
        <strong>{worktree.title}</strong>
        <span className={`git-worktree-state ${worktree.state}`}>{status}</span>
      </div>
      <code className="git-worktree-branch">{worktree.branch}</code>
      <code className="git-worktree-path" title={worktree.path}>{worktree.path}</code>
      <div className="git-worktree-foot">
        <span>会话 {worktree.sessionId.slice(-8)}</span>
        <button type="button" onClick={onOpen}>打开会话</button>
      </div>
    </article>
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
