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
	err = s.withCommitLock(sessionID, func() error {
		committed, cerr := project.CommitAll(s.Ctx(), meta.Path, message)
		if cerr != nil {
			return cerr
		}
		if committed {
			log.Printf("自动提交：会话 %s 第 %d 轮已提交到 %s", sessionID, turn, meta.Branch)
			// 轮末自动合并（2026-10 硬钩子，用户拍板）：本轮产生了新提交 → 异步起
			// 一个合并进程。提交与触发必须在同一条成功路径上——不依赖模型记得调
			// merge_request（提示词纪律保留，但它是软触发，这里是硬兜底）。另起
			// goroutine 而不是在本 goroutine 里跑：钩子不该拖住提交路径的收尾
			//（waitAutoCommits 等的是提交，不是合并的发起）。
			s.mergeHookWG.Add(1)
			go func() {
				defer s.mergeHookWG.Done()
				s.maybeAutoMerge(sessionID)
			}()
		}
		return nil
	})
	if err != nil {
		// fail-open：提交失败绝不影响对话，只记日志
		log.Printf("自动提交：会话 %s 第 %d 轮提交失败（不影响对话）: %v", sessionID, turn, err)
	}
}

// maybeAutoMerge 是轮末自动合并钩子：自动提交产生了新提交后，异步起一个合并进程
//（startMergeJobOpts 的 autoHook 形态：目标分支默认、不推送）。用户拍板的行为原话
// 「我不管在哪个会话做完事情，该轮对话结束，agent 自动提交当前修改，然后触发合并」
// ——提交与触发必须在同一条成功路径上，不能指望模型每轮都记得调 merge_request。
//
// 与模型 merge_request 的共存：按项目互斥（mergejob.go 的 mergeMu/mergeJobProject）
// 兜住两边同时发起——谁先占到谁跑，后来者拿「已有合并进程在跑」。
//
// 全静默 fail-open（与自动提交同款纪律）：合并本来就是异步后台任务，钩子绝不向
// 用户报错、绝不打扰对话——所有失败只按原因分档记日志：
//   - 「该项目已有合并进程在跑」：模型纪律先触发了 merge_request / workspace_sync，
//     正常竞争，静默跳过；
//   - 「本项目没有待合并的改动」：防御性（刚有新提交不该扫空——除非改动已被并进
//     集成分支），静默跳过，且不留失败任务（见 startMergeJobOpts 的 autoHook 分支）；
//   - 其他错误：只记日志，不影响会话。
func (s *Server) maybeAutoMerge(sessionID string) {
	jobID, err := s.startMergeJobOpts(sessionID, "", false, true)
	if err == nil {
		log.Printf("自动合并：会话 %s 已触发合并进程（任务 %s）", sessionID, jobID)
		return
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "已有合并进程在跑"):
		log.Printf("自动合并：会话 %s 跳过——该项目已有合并进程在跑（模型已先发起）", sessionID)
	case strings.Contains(msg, "没有待合并的改动"):
		log.Printf("自动合并：会话 %s 跳过——%s（防御性：刚产生新提交，通常是改动已被并进集成分支）", sessionID, msg)
	default:
		log.Printf("自动合并：会话 %s 起合并进程失败（不影响对话）: %v", sessionID, err)
	}
}

// withCommitLock 持会话的提交锁执行 fn。轮结束的自动提交（autoCommit，异步
// goroutine）与 merge_request 的前置提交（commitBeforeMerge，请求 goroutine）都会对
// 同一会话做 CommitAll——不串行化会在 git index 上互相踩（两把 add -A / commit
// 并发跑可能报 index.lock 竞争或产生错乱提交）。锁按会话分（autoCommitLock），
// 不同会话互不阻塞。
func (s *Server) withCommitLock(sessionID string, fn func() error) error {
	lock := s.autoCommitLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

// commitBeforeMerge 在起合并进程前对发起会话做一次检查点提交（mergejob.go 的
// startMergeJob 调用），消除时序缺口：merge_request 是模型**轮内**调用的工具，
// 此时本轮改动还没被 TurnDone 的自动提交落盘——不先提交的话，扫描会把它们算成
// dirty，而 merger 只合提交，本轮改动要等下一次合并才能进集成分支。
//
// 三条语义：
//  1. 提交信息与自动提交同一口径（LastUserTurn 的轮次 + 用户消息摘要）——同一轮
//     的改动无论由谁落盘，提交信息都长一个样；读不到轮次时退回中性信息（不挡合并）；
//  2. 工作树干净 = 直接继续（CommitAll 无改动返回 false，不产生空提交）——
//     workspace_sync 已经先提交过一遍时，这里自然跳过（幂等，无害）；
//  3. git 出错 = 人话错误返回，合并任务不起（fail-closed：带着没提交的改动起
//     合并，merger 合不到它们，等于静默丢改动）。
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

// waitAutoMergeHooks 等所有在途的自动合并钩子完成发起尝试（测试同步用；生产路径
// 不调用）。钩子完成 = startMergeJobOpts 已返回（任务已起或已被静默拒），合并任务
// 本身的收尾由 jobs.Manager 的状态机表达。
func (s *Server) waitAutoMergeHooks() { s.mergeHookWG.Wait() }

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
