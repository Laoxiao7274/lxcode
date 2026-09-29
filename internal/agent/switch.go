// 会话切换（/new、/resume）：切之前必须空闲、没有挂起确认——
// 否则用户的裁决会落到另一个会话上。

package agent

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// SwitchNew 结束当前会话（记录保留，可后续 resume），从空历史开始。
// 生成中/有挂起确认时拒绝——切换会撕裂运行中的工具循环。
func (s *Session) SwitchNew(workspace string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.switchGuardLocked(); err != nil {
		return "", err
	}
	dir, err := resolveWorkspaceDir(s.st, workspace)
	if err != nil {
		return "", err
	}
	// 历史、todo 和项目归属必须在同一锁内提交，Send 不能插入两步之间。
	s.id, s.history, s.todos = "", nil, nil
	s.pendingWorkspace, s.workDir = workspace, dir
	s.context = ContextUsage{} // 新会话：占用清零（下一轮重新测量）
	return "", nil
}

// SwitchTo 恢复指定会话：加载其历史并继续追加。当前会话记录保留。
func (s *Session) SwitchTo(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return errors.New("未启用会话存储")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
	msgs, err := s.st.Load(id)
	if err != nil {
		return err
	}
	ws, err := s.st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(s.st, ws)
	if err != nil {
		return err
	}
	// 校验完成再提交；todo 尚未持久化，不能沿用上一会话的内存清单。
	s.id, s.history, s.workDir = id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 换会话：占用重新测量（沿用旧值会误导压力判定）
	return nil
}

// SessionList 列出全部持久会话（无存储模式返回空）。
func (s *Session) SessionList() []sessiondata.SessionMeta {
	if s.st == nil {
		return nil
	}
	metas, err := s.st.List()
	if err != nil {
		log.Printf("列出会话失败: %v", err)
		return nil
	}
	return metas
}

// AttachSessionSearch 把会话搜索接进工具注册表（EnablePersistence 后调用）：
// JSONL 格式归 store 包所有，工具层只拿函数——格式单源不漂移。
func (s *Session) AttachSessionSearch() {
	s.tools.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		if s.st == nil {
			return "", errors.New("会话存储未启用，无法搜索历史")
		}
		hits, total, err := s.st.Search(q)
		if err != nil {
			return "", err
		}
		return sessiondata.FormatSearchHits(hits, total), nil
	})
}

// switchGuardLocked 是切换会话的前置检查（调用方持锁）：
// busy → ErrBusy；挂起确认 → ErrPendingConfirm。
func (s *Session) switchGuardLocked() error {
	if s.busy {
		return fmt.Errorf("%w（先取消当前生成）", ErrBusy)
	}
	if s.pending != nil {
		return ErrPendingConfirm
	}
	return nil
}
