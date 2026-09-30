// 落库与附着：历史追加、会话行确保、工作目录解析、按 id 精确附着。
// 存储细节关在这里，Session 的其余部分只见 Persistence 接口。

package agent

import (
	"errors"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
)

// checkpointIndexes 标出历史里的压缩检查点下标（前端渲染成「已压缩历史」块）。
// 按内容标记识别：历史就是普通 llm.Message，没有类型字段可依赖。
func checkpointIndexes(msgs []llm.Message) []int {
	var out []int
	for i, m := range msgs {
		if isCheckpointContent(m.Content) {
			out = append(out, i)
		}
	}
	return out
}

func (s *Session) append(m llm.Message) {
	s.mu.Lock()
	// 序号归 store：先落盘拿到号再入内存历史，两边同号（前端按 seq 撤回时
	// 内存与库必须指向同一条消息）
	m.Seq = s.persistLocked(m)
	s.history = append(s.history, m)
	s.mu.Unlock()
}

// persistLocked 落盘并返回 store 分配的序号（调用方持锁；store 挂了才做，
// 失败只记日志不回滚内存——内存才是运行真源，磁盘落后最多丢"最后几条"，
// 比让整轮对话因 IO 抖动失败好）。
//
// 返回 0 = 没有存储或落盘失败：那条消息在内存里没有序号，前端拿不到撤回锚点
// （它本来就没进库，撤回它也删不掉库里的东西——不编一个假号是唯一诚实的做法）。
func (s *Session) persistLocked(m llm.Message) int64 {
	if s.st == nil {
		return 0
	}
	if s.ensureSessionLocked() != nil {
		return 0
	}
	seq, err := s.st.AppendMsg(s.id, m)
	if err != nil {
		log.Printf("会话落盘失败（继续运行）: %v", err)
		return 0
	}
	return seq
}

// ensureSessionLocked 懒创建当前会话行（首条消息才建，避免空会话记录）。调用方持锁。
func (s *Session) ensureSessionLocked() error {
	if s.id != "" || s.st == nil {
		return nil
	}
	id, err := s.st.Create()
	if err != nil {
		return err
	}
	s.id = id
	// 归属项目落库（session.new 时预约的）
	if s.pendingWorkspace != "" {
		if err := s.st.SessionWorkspace(id, s.pendingWorkspace); err != nil {
			log.Printf("会话归属落库失败（继续运行）: %v", err)
		}
		s.pendingWorkspace = ""
	}
	// 通知宿主：会话行已建（侧栏「发消息 → 新对话出现」的信号）
	s.emit(SessionStartedEvent{ID: id})
	return nil
}

// resolveWorkspaceDir 区分未分组与失效项目：只有未分组才允许默认目录。
func resolveWorkspaceDir(st Persistence, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if st == nil {
		return "", errors.New("未启用项目存储")
	}
	meta, found, err := st.ProjectByID(id)
	if err != nil {
		return "", fmt.Errorf("查询项目 %s: %w", id, err)
	}
	if !found {
		return "", fmt.Errorf("项目 %s 不存在", id)
	}
	return project.ValidateDirectory(meta.Path)
}

// EnablePersistence 挂载磁盘存储并恢复最近会话（启动时调用）。
// 库里没有任何会话时保持空历史（全新开始）。幂等：重复调用是 no-op。
//
// 锁纪律：读库与"算占用"都在**锁外**做，只有提交那一刻持 s.mu——占用恢复要解析
// 模型窗口（读注册表），与注册表热加载的写锁交叉持有是自找麻烦（ModelID 同一条）。
func (s *Session) EnablePersistence(st Persistence) error {
	s.mu.Lock()
	if s.st != nil {
		s.mu.Unlock()
		return nil
	}
	if st == nil {
		s.mu.Unlock()
		return errors.New("持久化实现为空")
	}
	if err := s.switchGuardLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	agentID := s.agentID
	s.mu.Unlock()

	id, msgs, err := st.Latest()
	if err != nil {
		return err
	}
	var dir string
	if id != "" {
		ws, err := st.WorkspaceOf(id)
		if err != nil {
			return err
		}
		dir, err = resolveWorkspaceDir(st, ws)
		if err != nil {
			return err
		}
	}
	usage := s.restoredUsage(st, id, agentID, msgs)

	// 全部读取和校验成功后才提交，失败可修复存储后重试。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		return nil // 期间已被挂载（并发调用）：幂等
	}
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = usage
	return nil
}

// AttachTo 把会话挂到指定存储与**既有会话 id**（子会话创建/续跑用）。
// 与 EnablePersistence 的区别：那个走"恢复最近会话"的路径（Latest），这个按 id
// 精确附着——派发开的子会话刚建好行（历史为空）或要续跑（历史在库里）。
// 校验全部成功后才提交，失败保留原状态。
func (s *Session) AttachTo(st Persistence, id string) error {
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if id == "" {
		return errors.New("会话 id 不能为空")
	}
	msgs, err := st.Load(id) // 会话不存在时报错
	if err != nil {
		return err
	}
	ws, err := st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(st, ws)
	if err != nil {
		return err
	}
	s.mu.Lock()
	agentID := s.agentID // 窗口按归属 Agent 的模型解析（子会话可以绑自己的模型）
	s.mu.Unlock()
	usage := s.restoredUsage(st, id, agentID, msgs)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return fmt.Errorf("%w（不能附着到别的会话）", ErrBusy)
	}
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = usage
	return nil
}

// restoredUsage 决定附着一条会话之后内存里的占用测量（**调用方不持锁**——它要读库、
// 还要解析模型窗口，两件都不该占着会话锁）。三条路，按权威性从高到低：
//
//  1. 库里有落库的测量（重启前最后一轮主轮的值）→ 原样用，**包括窗口与 Estimated**。
//     原样用的理由：chat.done 的实时值与 chat.history 的回放值必须是同一份数字
//     （本仓库为"两条路径分叉"吃过三次亏），在这里按当前模型重算窗口就会分叉。
//  2. 库里没有（老会话/从没跑过主轮）→ 按**已加载的历史**重算一次估算，并照实标注
//     Estimated=true。复用 estimateContextUsage 同一份实现——估算绝不写第二份。
//  3. 两者都没有（空历史）→ 零值（Used=0）→ wire 上整键缺席，前端显示中性态。
//     不编数：空会话显示 0% 比显示「—」更坏（用户会以为"上下文是空的"）。
//
// 为什么要有第 2 条：用户实测「查看会话，他的上下文信息怎么是空的」——那条会话其实
// 有很长的历史，它的占用是**已知事实**（历史就在库里），不该因为后端重启就变成未知。
func (s *Session) restoredUsage(st Persistence, id, agentID string, msgs []llm.Message) ContextUsage {
	if st != nil && id != "" {
		if u, ok, err := st.ContextUsageOf(id); err != nil {
			log.Printf("读上下文占用失败（回落估算）: %v", err)
		} else if ok {
			return u
		}
	}
	// 空历史 = 两个来源都没有：返回**零值**（wire 上整键缺席、前端中性态），不编数。
	// 注意不能直接拿 estimateContextUsage 的结果：它对空历史也会给出 System=blockOverhead
	// 那 4 个 token 的"结构开销"——那是个估算器的内部常量，不是这条会话的占用事实。
	if len(msgs) == 0 {
		return ContextUsage{}
	}
	u := estimateContextUsage("", nil, msgs)
	u.Window = s.contextWindowFor(agentID)
	return u
}

// contextWindowFor 解析该 Agent 当前模型的上下文窗口（0 = 未知/未配置）。
// 解析失败不打断附着：窗口只是占比的分母，缺了就是"算不出占比"（前端中性态），
// **绝不编一个上限**。
func (s *Session) contextWindowFor(agentID string) int {
	if s.reg == nil {
		return 0 // 无注册表的调用方（纯内存模式/单测）
	}
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return 0
	}
	m, err := s.modelFor(ac)
	if err != nil {
		return 0
	}
	return m.ContextWindow
}
