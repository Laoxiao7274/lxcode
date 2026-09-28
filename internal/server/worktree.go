// 项目会话的 git 工作树：首次发送时从当时 HEAD 建独立分支与工作树，
// 重启/目录丢失时恢复同一分支，用户可显式释放空闲且干净的。
package server

import (
	"fmt"
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

func (s *Server) prepareWorktree(sessionID string, sess *agent.Session) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return s.prepareWorktreeLocked(sessionID, sess)
}

func (s *Server) prepareWorktreeLocked(sessionID string, sess *agent.Session) error {
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
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return err
	}
	// 快路径：本进程内已校验过这条会话的工作树，且目录还在 → 直接收工。
	//
	// 校验（project.RestoreWorktree）要跑 4 个 git 子进程，实测 ~130ms；而
	// chat.history / session.resume 每次都会走到 prepareWorktree——一条项目会话
	// 点开一次就是两次校验，切会话于是成了肉眼可见的「卡一下」（帧级实测：
	// 项目会话点击 → 内容切换 ~480ms，其中服务端两次校验占 ~260ms；未分组
	// 会话同一动作 0-12ms，因为它不走这条路）。
	//
	// 判据是「本进程校验过 + 会话的 workDir 仍指向它 + 目录还在」：workDir 只在
	// prepare 成功后写入（见下），所以三者同时成立就等价于「刚校验过且没动过」。
	// os.Stat 是必须的——用户手工删掉目录后要能落回下面的完整恢复流程。
	// 失效点只有一处：releaseSessionWorktree 会清掉标记。
	if meta.Path != "" && sess.WorkDir() == meta.Path && s.worktreeReadyNow(sessionID) {
		if _, statErr := os.Stat(meta.Path); statErr == nil {
			return nil
		}
	}
	if meta.Path == "" {
		if meta.Branch != "" || meta.BaseCommit != "" {
			return fmt.Errorf("会话 %s 的 worktree 元数据不完整", sessionID)
		}
		wt, err := project.CreateWorktree(projectMeta.Path, s.st.WorktreeRoot(), workspace, sessionID)
		if err != nil {
			return err
		}
		meta = store.WorktreeMeta{Path: wt.Path, Branch: wt.Branch, BaseCommit: wt.BaseCommit}
		if err := s.st.SetWorktree(sessionID, meta); err != nil {
			if rollbackErr := project.RemoveWorktree(projectMeta.Path, wt); rollbackErr != nil {
				return fmt.Errorf("保存 worktree 元数据失败: %v；回滚也失败: %w", err, rollbackErr)
			}
			return err
		}
	} else if err := project.RestoreWorktree(projectMeta.Path, meta.Path, meta.Branch); err != nil {
		return err
	}
	if sess.WorkDir() != meta.Path {
		if err := sess.SetWorkDir(meta.Path); err != nil {
			return err
		}
	}
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

func (s *Server) releaseSessionWorktree(sessionID string) error {
	if s.st == nil {
		return fmt.Errorf("会话存储未启用，无法释放工作区")
	}
	sess, err := s.session(sessionID)
	if err != nil {
		return err
	}
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if sess.Busy() {
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
		return fmt.Errorf("会话尚未创建完整的 worktree")
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
