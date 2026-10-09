// 每轮结束的检查点提交：项目会话在助手给出最终答复后，把本会话 worktree 里的
// 改动提交到 lxcode/session-<id> 分支（一轮一个提交）。
//
// 三条纪律（缺一条就会伤到对话或 git 状态）：
//  1. **不阻塞事件路径**：emit 里只起 goroutine，git 调用全在 goroutine 里；
//  2. **fail-open**：失败只记日志，绝不向用户报错、绝不中断事件流；
//  3. **同一会话串行**：按会话加提交锁，并发轮次（子会话/并行 dispatch）不互相
//     打断 git index。
package server

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/moyunteng/lxcode/internal/project"
)

// autoCommitSummaryMax 是提交信息里摘要的字符上限（约 60 字符）。按 rune 计——
// 中文一个汉字算一个字符，按字节截会把中文砍成乱码。
const autoCommitSummaryMax = 60

// autoCommitLock 返回按会话的提交锁。与 worktreeLocks 分开的理由：提交发生在事件
// 路径的 goroutine 上，而 worktreeLocks 在 prepareWorktree / release 里被持有且可能
// 跑较久的 git 校验——共用会让两条路径互相阻塞。
func (s *Server) autoCommitLock(sessionID string) *sync.Mutex {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()
	lock := s.commitLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.commitLocks[sessionID] = lock
	}
	return lock
}

// maybeAutoCommit 在轮结束事件上被触发：项目会话有 worktree 时，异步把本轮改动提交
// 为一个检查点提交。**绝不阻塞事件路径**——这里只起 goroutine，不做任何 git 调用。
func (s *Server) maybeAutoCommit(sessionID string) {
	if s.st == nil || sessionID == "" {
		return
	}
	s.commitWG.Add(1)
	go func() {
		defer s.commitWG.Done()
		s.autoCommit(sessionID)
	}()
}

// autoCommit 是提交本体（在 goroutine 里跑）。
func (s *Server) autoCommit(sessionID string) {
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		log.Printf("自动提交：读取会话 %s 的 worktree 元数据失败: %v", sessionID, err)
		return
	}
	if meta.Path == "" || meta.Branch == "" {
		return // 未分组会话（没有 worktree）：不提交
	}
	turn, lastUser, err := s.st.LastUserTurn(sessionID)
	if err != nil {
		log.Printf("自动提交：读取会话 %s 的轮次与摘要失败: %v", sessionID, err)
		return
	}
	message := commitMessage(turn, lastUser)
	lock := s.autoCommitLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	committed, err := project.CommitAll(s.Ctx(), meta.Path, message)
	if err != nil {
		// fail-open：提交失败绝不影响对话，只记日志
		log.Printf("自动提交：会话 %s 第 %d 轮提交失败（不影响对话）: %v", sessionID, turn, err)
		return
	}
	if committed {
		log.Printf("自动提交：会话 %s 第 %d 轮已提交到 %s", sessionID, turn, meta.Branch)
	}
}

// waitAutoCommits 等所有在途的自动提交结束（测试同步用；生产路径不调用）。
func (s *Server) waitAutoCommits() { s.commitWG.Wait() }

// commitMessage 组装检查点提交信息：「第 N 轮：<本轮用户请求摘要>」。
func commitMessage(turn int, lastUser string) string {
	if summary := commitSummary(lastUser); summary != "" {
		return fmt.Sprintf("第 %d 轮：%s", turn, summary)
	}
	return fmt.Sprintf("第 %d 轮", turn)
}

// commitSummary 取本轮用户消息的首行并截断到约 60 字符（提交信息要一眼看出是哪一轮）。
func commitSummary(text string) string {
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > autoCommitSummaryMax {
		text = strings.TrimSpace(string([]rune(text)[:autoCommitSummaryMax])) + "…"
	}
	return text
}
