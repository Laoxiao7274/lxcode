// 项目会话的工作区准备：会话**直接工作在用户的主检出目录与主检出当前分支**
// （2026-10 架构修订拍板：废除 per-session worktree 与 lxcode/session-* 分支——
// 「不用做隔离，就跟正常一样，也不用创分支」）。轮末自动检查点提交已移除
// （2026-10 用户拍板）：改动留在工作区，提交与推送都由用户显式要求的
// workspace_sync 完成。
//
// 兼容纪律（用户数据零丢失）：存量会话的 worktree 元数据、磁盘 worktree 目录与
// lxcode/session-* 分支**一律保留不删**——resume 时切到直连模式，旧目录与分支
// 原样留给用户手工处理。
package server

import (
	"fmt"
	"log"
	"os"
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
	// 零提交仓库（unborn HEAD）由 project.CurrentBranch 内部回退 symbolic-ref 兜住；
	// 走到这里的错误基本是「目录不是 git 仓库」（.git 被删 / git init 从未成功）：
	// 降级运行而非失败——提示词如实告知没有版本管理，会话照常创建与发送。
	branch, err := project.CurrentBranch(s.Ctx(), projectMeta.Path)
	if err != nil {
		log.Printf("工作区准备：读取项目 %s 的 git 分支失败，会话按非 git 目录降级: %v", projectMeta.Path, err)
		branch = ""
	}
	if sess.WorkDir() != projectMeta.Path {
		if err := sess.SetWorkDir(projectMeta.Path); err != nil {
			return err
		}
	}
	// 工作区信息随 workDir 一起挂到会话上：提示词要告诉模型「直接在用户的项目目录
	// 工作」与「不会自动提交」的语义（未分组会话没有这段，零注入；非 git 目录只
	// 注入降级说明）。无条件写（幂等）——快路径早退时它已经设过，这里重复设置
	// 不会出错。branch 为空 = 非 git 降级（CurrentBranch 成功时恒返回非空名字）。
	sess.SetWorktreeInfo(branch, base, branch == "")
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
