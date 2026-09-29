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
func (s *Session) EnablePersistence(st Persistence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		return nil
	}
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
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
	// 全部读取和校验成功后才提交，失败可修复存储后重试。
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 占用随会话走：换了历史就得重新测量
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
	defer s.mu.Unlock()
	if s.busy {
		return fmt.Errorf("%w（不能附着到别的会话）", ErrBusy)
	}
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 占用随会话走：换了历史就得重新测量
	return nil
}
