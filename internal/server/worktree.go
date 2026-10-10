// 项目会话的 git 工作树：首次发送时从当时 HEAD 建独立分支与工作树，
// 重启/目录丢失时恢复同一分支，用户可显式释放空闲且干净的。
package server

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

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
	// 耗时测量：整条 git 链（快路径只付 os.Stat，慢路径串行 3+ 个 git 子进程）。
	// 超过 200ms 才记一行——预热路径与 send 路径都会走这里，阈值之下保持安静。
	start := time.Now()
	defer func() {
		if ms := time.Since(start).Milliseconds(); ms >= 200 {
			log.Printf("worktree 准备耗时 %dms（session=%s）", ms, sessionID)
		}
	}()
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
	// 工作树信息随 workDir 一起挂到会话上：提示词要告诉模型分支名与「每轮自动提交」
	// 的语义（未分组会话没有这段，零注入）。无条件写（幂等）——快路径早退时它已经
	// 设过，这里重复设置不会出错。
	sess.SetWorktreeInfo(meta.Branch, meta.BaseCommit)
	s.markWorktreeReady(sessionID)
	return nil
}

// preheatWorktree 在 session.new 之后**异步**预热项目会话的 git 工作树：把建工作树
// 的 git 链（show-ref + rev-parse + worktree add 完整检出，~200ms 起、冷态秒级）
// 挪出首条 chat.send 的等待路径。
//
// 防重入用 per-session 互斥锁的 TryLock（实现选它而非另设 preheating map：锁本身
// 就是「同一会话串行」的事实源——sendSession 持同一把锁，TryLock 抢不到说明
// send/另一个预热正在跑，直接放弃即可，prepareWorktreeLocked 幂等， send 路径
// 自会把工作树建出来；一张额外的 map 只会引入第二个需要保持一致的状态）。
//
// fail-open：预热失败（项目目录不可用、仓库没有 HEAD 等）只记一行日志，绝不广播
// 错误、绝不影响 send——sendSession 会按既有行为再试并把这个错误带给前端。
// 只对「workspace 非空且尚未建过工作树」的会话做：已建过的走快路径（~0ms），
// 预热纯属浪费；未分组会话没有独立工作树，不预热。
func (s *Server) preheatWorktree(sessionID string, sess *agent.Session) {
	if s.st == nil {
		return
	}
	go func() {
		meta, err := s.st.WorktreeOf(sessionID)
		if err != nil || meta.Path != "" {
			// 读不到元数据 / 已建过：都不需要预热（后者本就走快路径）。
			return
		}
		lock := s.sessionWorktreeLock(sessionID)
		if !lock.TryLock() {
			return // 已有预热或 sendSession 在跑：放弃，send 会再试
		}
		defer lock.Unlock()
		if err := s.prepareWorktreeLocked(sessionID, sess); err != nil {
			log.Printf("worktree 预热失败（不影响发送，首条消息时会重试）: session=%s: %v", sessionID, err)
		}
	}()
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

// releaseArchivedWorktrees 释放某会话及其子会话的工作区目录（归档时的可选附加动作）。
// 子会话可能已有自己的独立 worktree（标签页被打开过）——dispatch 深度恒 1，只遍历
// 一层；没有 worktree 的目标（子会话没打开过标签页）按 Path 跳过、不报错。
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
			continue // 没有 worktree（子会话没打开过标签页）——跳过不报错
		}
		if err := s.releaseSessionWorktree(id); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", id, err))
			continue
		}
		released = true
	}
	return released && len(failures) == 0, strings.Join(failures, "；")
}
