// Git 管理页（前端 Git 工作台）的只读查询：git.overview / git.diff。
//
// 纪律（对齐 workspace.go 的三条）：**全部只读**——只走 project 包带 ctx+超时的
// 只读封装，绝不 commit / checkout / reset 主检出；页面上的提交由会话内检查点与
// workspace_sync 负责，唯一的写入口是既有 session.worktree.release。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/store"
)

// gitOverviewCommitLimit 是 git.overview 携带的提交条数：给前端「历史」标签页
// 渲染的最近提交，20 条足够看脉络，再多只是拖应答。
const gitOverviewCommitLimit = 20

func (s *Server) dispatchGit(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodGitOverview:
		var p protocol.GitOverviewParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		result, err := s.gitOverview(c.sessionID, p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, result)

	case protocol.MethodGitDiff:
		var p protocol.GitDiffParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.Path) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 path")
		}
		meta, err := s.resolveGitProject(c.sessionID, p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		diff, err := project.DiffFile(s.Ctx(), meta.Path, p.Path)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "读取文件差异失败: "+err.Error())
		}
		return protocol.NewResult(req.ID, protocol.GitDiffResult{Diff: diff})
	}
	return nil
}

// resolveGitProject 解析 Git 页查询的目标项目：projectID 非空 = 显式指定（项目
// 切换器），空 = 当前会话归属的项目。复用 workspace.go 的 resolveProject——
// 「当前会话没有归属项目」的报错文案两边同一条。
func (s *Server) resolveGitProject(sessionID, projectID string) (store.ProjectMeta, error) {
	return s.resolveProject(sessionID, projectID)
}

// gitOverview 汇总项目主检出的只读快照：状态 + 分支（含会话分支）+ 最近提交。
func (s *Server) gitOverview(sessionID, projectID string) (protocol.GitOverviewResult, error) {
	meta, err := s.resolveGitProject(sessionID, projectID)
	if err != nil {
		return protocol.GitOverviewResult{}, err
	}
	ctx := s.Ctx()
	main, err := project.ProjectStatus(ctx, meta.Path)
	if err != nil {
		return protocol.GitOverviewResult{}, fmt.Errorf("读取主检出状态失败: %w", err)
	}
	result := protocol.GitOverviewResult{
		Path:   meta.Path,
		Branch: main.Branch,
		Dirty:  mapDirty(main.Files),
	}

	// 主检出当前分支：ahead/behind 相对上游 origin/<branch>（没有上游就都是 0，
	// 不编数）。当前分支恒列出，且排在最前。
	current := protocol.GitBranchInfo{Name: main.Branch, Current: true}
	if ahead, behind, tracked, uerr := upstreamAheadBehind(ctx, meta.Path, main.Branch); uerr == nil && tracked {
		current.Ahead, current.Behind = ahead, behind
	}
	result.Branches = append(result.Branches, current)

	// 会话分支：该项目下全部**顶层**会话（含归档——归档的分支同样是备份，标注），
	// 分支存在才列（还没发过消息的会话没有分支，不编一条出来）。
	st, err := s.workspaceStore()
	if err != nil {
		return protocol.GitOverviewResult{}, err
	}
	sessions, err := st.List()
	if err != nil {
		return protocol.GitOverviewResult{}, fmt.Errorf("列出会话失败: %w", err)
	}
	for _, sess := range sessions {
		if sess.Workspace != meta.ID || sess.ParentID != "" {
			continue
		}
		branch := "lxcode/session-" + sess.ID
		exists, err := project.BranchExists(ctx, meta.Path, branch)
		if err != nil {
			return protocol.GitOverviewResult{}, fmt.Errorf("查询会话分支失败: %w", err)
		}
		if !exists {
			continue
		}
		info := protocol.GitBranchInfo{
			Name:         branch,
			SessionID:    sess.ID,
			SessionTitle: sess.Title,
			Archived:     sess.Archived,
		}
		// ahead/behind 相对主检出当前分支（一会话一分支架构下没有「切换」语义，
		// 这两个数只回答「这个会话相对主检出多了/少了几个提交」）。
		if ahead, err := project.CommitCountBetween(ctx, meta.Path, main.Branch, branch); err == nil {
			info.Ahead = ahead
		}
		if behind, err := project.CommitCountBetween(ctx, meta.Path, branch, main.Branch); err == nil {
			info.Behind = behind
		}
		if merged, err := project.IsAncestor(ctx, meta.Path, branch, main.Branch); err == nil {
			info.Merged = merged
		}
		// 工作树：元数据里有路径且目录存在才算「有」——释放只删目录、保留分支，
		// 「分支在、目录不在」是常态。
		wt, err := st.WorktreeOf(sess.ID)
		if err != nil {
			return protocol.GitOverviewResult{}, fmt.Errorf("读取会话工作树元数据失败: %w", err)
		}
		if wt.Path != "" {
			if _, statErr := os.Stat(wt.Path); statErr == nil {
				info.HasWorktree = true
				info.WorktreePath = wt.Path
				if dirty, err := project.StatusLines(ctx, wt.Path); err == nil {
					info.DirtyCount = len(dirty)
				}
			}
		}
		result.Branches = append(result.Branches, info)
	}

	commits, err := project.LogRecent(ctx, meta.Path, gitOverviewCommitLimit)
	if err != nil {
		return protocol.GitOverviewResult{}, fmt.Errorf("读取提交历史失败: %w", err)
	}
	result.Commits = make([]protocol.GitCommitInfo, len(commits))
	for i, cm := range commits {
		result.Commits[i] = protocol.GitCommitInfo{Hash: cm.Hash, Message: cm.Message, Author: cm.Author, When: cm.When}
	}
	return result, nil
}

// mapDirty 把 status --porcelain 行映射成 GitChange（Kind 取两列状态里更"重"
// 的那档；重命名行取箭头后的新路径）。解析不了的行按 modified 兜底——宁可
// 多显示一条也不静默丢。
func mapDirty(lines []string) []protocol.GitChange {
	changes := make([]protocol.GitChange, 0, len(lines))
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1]
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path == "" {
			continue
		}
		kind := "modified"
		switch {
		case x == '?' && y == '?':
			kind = "untracked"
		case x == 'A' || y == 'A':
			kind = "added"
		case x == 'D' || y == 'D':
			kind = "deleted"
		}
		changes = append(changes, protocol.GitChange{Path: path, Kind: kind})
	}
	return changes
}

// upstreamAheadBehind 返回本地分支相对其上游 origin/<branch> 的 ahead/behind；
// 上游引用不存在时 tracked=false（调用方按「无上游」处理，ahead/behind 记 0
// 但不展示箭头语义——前端对当前分支的 0/0 视为「未知」）。
func upstreamAheadBehind(ctx context.Context, repoDir, branch string) (ahead, behind int, tracked bool, err error) {
	upstream := "refs/remotes/origin/" + branch
	exists, err := project.RefExists(ctx, repoDir, upstream)
	if err != nil || !exists {
		return 0, 0, false, err
	}
	ahead, err = project.CommitCountBetween(ctx, repoDir, upstream, branch)
	if err != nil {
		return 0, 0, true, err
	}
	behind, err = project.CommitCountBetween(ctx, repoDir, branch, upstream)
	if err != nil {
		return 0, 0, true, err
	}
	return ahead, behind, true, nil
}
