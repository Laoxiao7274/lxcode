// 每轮结束的检查点提交：项目会话在助手给出最终答复后，把**主检出**里的改动提交到
// 用户当前的分支（一轮一个提交）。2026-10 架构修订：会话直连主检出，不再有
// per-session worktree——提交路径就是会话的 workDir，分支就是主检出当前分支。
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

// autoCommitLock 返回按会话的提交锁。与 workspaceLocks 分开的理由：提交发生在事件
// 路径的 goroutine 上，而 workspaceLocks 在 prepareWorkspace / release 里被持有且
// 可能做磁盘校验——共用会让两条路径互相阻塞。
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

// maybeAutoCommit 在轮结束事件上被触发：项目会话异步把本轮改动提交为一个检查点
// 提交。**绝不阻塞事件路径**——这里只起 goroutine，不做任何 git 调用。
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
//
// 提交路径的新来源（2026-10 架构修订）：**会话运行时的 workDir**——直连模式下它
// 就是主检出，老会话 resume 后同样被 prepareWorkspace 对准主检出。提交本来就跑在
// workDir，workDir 变了它自然落在主检出当前分支。未分组会话不提交；workDir 拿不到
// 时回落 WorktreeOf 的旧 worktree 元数据（老会话尚未重新准备的防御路径），两者都
// 空就直接放弃——绝不猜路径。
func (s *Server) autoCommit(sessionID string) {
	ws, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		log.Printf("自动提交：读取会话 %s 的归属项目失败: %v", sessionID, err)
		return
	}
	if ws == "" {
		return // 未分组会话：不提交
	}
	path := ""
	if sess := s.peekSession(sessionID); sess != nil {
		path = sess.WorkDir()
	}
	if path == "" {
		meta, err := s.st.WorktreeOf(sessionID)
		if err != nil {
			log.Printf("自动提交：读取会话 %s 的工作区元数据失败: %v", sessionID, err)
			return
		}
		path = meta.Path // 空 = 尚未准备过（防御路径，正常链路不会走到）
		if path == "" {
			return
		}
	}
	turn, lastUser, err := s.st.LastUserTurn(sessionID)
	if err != nil {
		log.Printf("自动提交：读取会话 %s 的轮次与摘要失败: %v", sessionID, err)
		return
	}
	message := commitMessage(turn, lastUser)
	err = s.withCommitLock(sessionID, func() error {
		committed, cerr := project.CommitAll(s.Ctx(), path, message)
		if cerr != nil {
			return cerr
		}
		if committed {
			log.Printf("自动提交：会话 %s 第 %d 轮已提交到 %s（主检出当前分支）", sessionID, turn, path)
		}
		return nil
	})
	if err != nil {
		// fail-open：提交失败绝不影响对话，只记日志
		log.Printf("自动提交：会话 %s 第 %d 轮提交失败（不影响对话）: %v", sessionID, turn, err)
	}
}

// withCommitLock 持会话的提交锁执行 fn。轮结束的自动提交（autoCommit，异步
// goroutine）与 workspace_sync 的提交、遗留合并进程的前置提交（commitBeforeMerge，
// 请求 goroutine）都会对同一会话做 CommitAll——不串行化会在 git index 上互相踩
// （两把 add -A / commit 并发跑可能报 index.lock 竞争或产生错乱提交）。锁按会话分
//（autoCommitLock），不同会话互不阻塞。
func (s *Server) withCommitLock(sessionID string, fn func() error) error {
	lock := s.autoCommitLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

// commitBeforeMerge 是**遗留**函数：在起合并进程前对发起会话做一次检查点提交
//（mergejob.go 的 startMergeJob 调用——合并进程已退役为遗留，主链路不再走到这里，
// 代码与测试保留以便将来恢复隔离模式）。
//
// 三条语义：
//  1. 提交信息与自动提交同一口径（LastUserTurn 的轮次 + 用户消息摘要）——同一轮
//     的改动无论由谁落盘，提交信息都长一个样；读不到轮次时退回中性信息（不挡合并）；
//  2. 工作树干净 = 直接继续（CommitAll 无改动返回 false，不产生空提交）；
//  3. git 出错 = 人话错误返回，合并任务不起（fail-closed）。
//
// 与自动提交共用 withCommitLock：轮内先提交，随后 TurnDone 的自动提交发现工作树
// 干净 → 跳过（无空提交），时序收敛。
func (s *Server) commitBeforeMerge(sessionID, worktreePath string) error {
	turn, lastUser, err := s.st.LastUserTurn(sessionID)
	message := "合并前检查点提交"
	if err == nil {
		message = commitMessage(turn, lastUser)
	}
	return s.withCommitLock(sessionID, func() error {
		committed, err := project.CommitAll(s.Ctx(), worktreePath, message)
		if err != nil {
			return fmt.Errorf("提交本次改动失败：%v，合并未发起", err)
		}
		if committed {
			log.Printf("合并前提交：会话 %s 第 %d 轮改动已提交（merge_request 前置）", sessionID, turn)
		}
		return nil
	})
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
