// job.* 域（后台任务——docs/jobs.md §4）：列表 / 结束 / 读全量日志。
//
// 纪律（AGENTS.md §5 坑 14）：**先按方法前缀判「是不是我的」，不是就返回 nil
// 让给下一个域**，再做自己的前置校验（未装配 / 参数 / 未知任务）。把前置校验写在
// 方法匹配之前会吞掉它之后的所有方法域（search.* 被吞过一次）。
package server

import (
	"encoding/json"
	"strings"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// isJobMethod 报告方法是否属于 job.* 域（只看前缀，不做任何前置校验）。
func isJobMethod(method string) bool { return strings.HasPrefix(method, "job.") }

func (s *Server) dispatchJobs(req *protocol.Request, params json.RawMessage) *protocol.Response {
	if !isJobMethod(req.Method) {
		return nil // 不是我的方法：让给下一个域（§5 坑 14）
	}
	mgr := s.jobsManager()
	if mgr == nil {
		return protocol.NewError(req.ID, protocol.CodeInternal, "后台任务未装配")
	}
	switch req.Method {
	case protocol.MethodJobList:
		var p protocol.JobListParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		list := mgr.List(p.SessionID)
		out := protocol.JobListResult{Jobs: make([]protocol.JobInfo, 0, len(list))}
		for _, snap := range list {
			out.Jobs = append(out.Jobs, s.jobInfo(snap))
		}
		return protocol.NewResult(req.ID, out)

	case protocol.MethodJobKill:
		var p protocol.JobKillParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.ID) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		// 用户点「结束」= EndedBy=user：与 agent 的 job_kill 工具**同一条
		// 路径**，只有 by 不同——两条路径的行为永远一致
		snap, err := mgr.Kill(p.ID, jobs.EndedUser)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 用户交互（哪怕只是点「结束」）重置唤醒预算（契约 §5）
		s.resetWakes(snap.SessionID)
		return protocol.NewResult(req.ID, protocol.JobKillResult{Job: s.jobInfo(snap)})

	case protocol.MethodJobLog:
		var p protocol.JobLogParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.ID) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		res, err := jobLog(mgr, p.ID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, res)
	}
	return nil
}
