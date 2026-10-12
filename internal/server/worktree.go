// 项目会话的工作区准备：会话**直接工作在用户的主检出目录与主检出当前分支**
// （2026-10 架构修订拍板：废除 per-session worktree 与 lxcode/session-* 分支——
// 「不用做隔离，就跟正常一样，也不用创分支」）。每轮结束的检查点提交直接落在
// 主检出当前分支上，方便回滚；真正要推送时由 workspace_sync 直接 push。
//
// 兼容纪律（用户数据零丢失）：存量会话的 worktree 元数据、磁盘 worktree 目录与
// lxcode/session-* 分支**一律保留不删**——resume 时切到直连模式，旧目录与分支
// 原样留给用户手工处理。
package server

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/store"
)

func (s *Server) sessionWorktreeLock(sessionID string) *sync.Mutex {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	lock := s.worktreeLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.worktreeLocks[sessionID] = lock
	}
	return lock
}

func (s *Server) prepareWorkspace(sessionID string, sess *agent.Session) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return s.prepareWorkspaceLocked(sessionID, sess)
}

// prepareWorkspaceLocked 把项目会话对准主检出：workDir = 项目根目录，worktree
// 信息 = 主检出当前分支 + 会话基线 HEAD。**不创建任何分支、任何 worktree、不做
// status 脏检查**——主检出路径恒定，准备只剩 os.Stat + 一次性的基线记录。
//
// WorktreeOf 三列的新语义（wire 与列名不变，消费方按此适配）：
//   - worktree_path / worktree_branch 对新会话**写空串** = 直连模式（无独立 worktree）；
//   - worktree_base = 会话基线 HEAD（首次准备时记录，workspace_rollback 的
//     session-start 锚点）；老会话该列存的是旧 worktree 基线，语义同样成立
//     （它就是「这条会话开始时的 HEAD」）。
//
// 老会话（元数据里有 worktree 路径/分支）：切到直连模式，但**不动存储元数据**——
// SetWorktree 的防改绑守卫会拒绝覆盖，而且旧目录与分支必须原样保留。
func (s *Server) prepareWorkspaceLocked(sessionID string, sess *agent.Session) error {
	if s.st == nil {
		return nil
	}
	workspace, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		return err
	}
	if workspace == "" {
		return nil
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("项目 %s 不存在", workspace)
	}
	// 快路径：主检出路径恒定——本进程已准备过（workDir 已指向主检出）且目录还在
	// → 直接收工。判据是「本进程校验过 + workDir 仍指向主检出 + 目录存在」：
	// os.Stat 是必须的（用户手工删掉/移动项目目录后要落回完整流程）。
	// 失效点只有一处：releaseSessionWorktree 会清掉标记（仅老会话有 worktree 时）。
	if s.worktreeReadyNow(sessionID) && sess.WorkDir() == projectMeta.Path {
		if _, statErr := os.Stat(projectMeta.Path); statErr == nil {
			return nil
		}
	}
	if _, err := project.ValidateDirectory(projectMeta.Path); err != nil {
		return err
	}
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return err
	}
	base := meta.BaseCommit
	if meta.Path == "" {
		// 直连模式：基线 HEAD 记录一次（幂等——SetWorktree 允许写回完全相同的
		// 元数据；重复准备时 base 已非空，不再跑 rev-parse）。
		if base == "" {
			head, err := project.ResolveCommit(s.Ctx(), projectMeta.Path, "HEAD")
			if err != nil {
				// 零提交仓库没有 HEAD：不编基线（回滚的 session-start 会如实报
				// 「基线未知」），不阻塞会话——直连模式不依赖 HEAD 也能工作。
				base = ""
			} else {
				if err := s.st.SetWorktree(sessionID, store.WorktreeMeta{BaseCommit: head}); err != nil {
					return err
				}
				base = head
			}
		}
	}
	// 分支名 = 主检出当前分支（只读展示给模型用——用户切了分支，下次准备时跟着变）。
	branch, err := project.CurrentBranch(s.Ctx(), projectMeta.Path)
	if err != nil {
		return err
	}
	if sess.WorkDir() != projectMeta.Path {
		if err := sess.SetWorkDir(projectMeta.Path); err != nil {
			return err
		}
	}
	// 工作区信息随 workDir 一起挂到会话上：提示词要告诉模型「直接在用户的项目目录
	// 工作」与「每轮自动提交」的语义（未分组会话没有这段，零注入）。无条件写（幂等）
	// ——快路径早退时它已经设过，这里重复设置不会出错。
	sess.SetWorktreeInfo(branch, base)
	s.markWorktreeReady(sessionID)
	return nil
}

func (s *Server) worktreeReadyNow(sessionID string) bool {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	return s.worktreeReady[sessionID]
}

func (s *Server) markWorktreeReady(sessionID string) {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	if s.worktreeReady == nil {
		s.worktreeReady = map[string]bool{}
	}
	s.worktreeReady[sessionID] = true
}

func (s *Server) clearWorktreeReady(sessionID string) {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	delete(s.worktreeReady, sessionID)
}

// releaseSessionWorktree 是**遗留**入口（session.worktree.release）：只对存量
// worktree 会话有意义——移除干净的检出目录、保留分支。直连模式的新会话没有
// worktree，到这里只会得到一句解释。代码保留：老会话的目录清理仍走它。
func (s *Server) releaseSessionWorktree(sessionID string) error {
	if s.st == nil {
		return fmt.Errorf("会话存储未启用，无法释放工作区")
	}
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	// 不创建运行时：释放是纯磁盘操作，为一个没打开的会话拉起运行时（读历史、
	// 附着存储）没有意义。不在内存里 = 没有正在跑的一轮 = 不忙。
	if sess := s.peekSession(sessionID); sess != nil && sess.Busy() {
		return agent.ErrBusy
	}
	workspace, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		return err
	}
	if workspace == "" {
		return fmt.Errorf("未分组会话没有独立 worktree")
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("项目 %s 不存在", workspace)
	}
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return err
	}
	if meta.Path == "" || meta.Branch == "" {
		return fmt.Errorf("本会话直接工作在项目主检出（直连模式），没有独立 worktree 可释放")
	}
	if err := project.ReleaseWorktree(projectMeta.Path, project.Worktree{
		Path: meta.Path, Branch: meta.Branch, BaseCommit: meta.BaseCommit,
	}); err != nil {
		return err
	}
	// 目录已经移除：清掉校验标记，否则下次恢复会话会走快路径、不再重建工作树。
	s.clearWorktreeReady(sessionID)
	return nil
}

// releaseArchivedWorktrees 释放某会话及其子会话的工作区目录（归档时的可选附加动作，
// **遗留**：只对存量 worktree 会话生效）。子会话可能有自己的旧 worktree（标签页被
// 打开过）——dispatch 深度恒 1，只遍历一层；直连模式的目标（Path 为空）跳过、不报错。
//
// 归档是主操作，这里只做尽力而为：**绝不向上返回错误**，只把每个目标的失败原因
// 收集起来（「；」拼接）交给前端提示。
//
// 返回：是否至少成功释放了一个目标且没有失败；失败原因（空串 = 无失败）。
func (s *Server) releaseArchivedWorktrees(sessionID string) (bool, string) {
	targets := []string{sessionID}
	if children, err := s.st.ChildrenOf(sessionID); err == nil {
		for _, child := range children {
			targets = append(targets, child.ID)
		}
	}
	released := false
	var failures []string
	for _, id := range targets {
		meta, err := s.st.WorktreeOf(id)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", id, err))
			continue
		}
		if meta.Path == "" {
			continue // 直连模式（或没建过 worktree）——没有目录可释放，跳过不报错
		}
		if err := s.releaseSessionWorktree(id); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", id, err))
			continue
		}
		released = true
	}
	return released && len(failures) == 0, strings.Join(failures, "；")
}
