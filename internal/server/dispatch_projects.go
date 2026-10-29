// 项目域：project.*（增列 / 移除注册 / 项目守则读写——只按项目 id 寻址）。

package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) dispatchProjects(req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodProjectAdd:
		var p protocol.ProjectAddParams
		if err := json.Unmarshal(params, &p); err != nil || p.Path == "" || strings.TrimSpace(p.Name) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 name 与 path")
		}
		saved, err := project.Add(s.st, p.Name, p.Path)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventProjectChanged, map[string]any{})
		// 协议映射（store 类型无 json tag——直接序列化会漏键名，session.list 踩过同款）
		return protocol.NewResult(req.ID, protocol.ProjectMeta{
			ID: saved.ID, Name: saved.Name, Path: saved.Path,
		})

	case protocol.MethodProjectRemove:
		var p protocol.ProjectRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 project_id")
		}
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		// 边界：项目下**有会话正在跑** → 拒绝（移除后侧栏没有入口停它们，
		// 而项目根仍在被这些会话当作工作目录用——先停再删是唯一不会让人
		// 困惑的顺序）。空闲会话不拦：它们留在库里，历史一条不丢。
		if running := s.runningSessionsOfProject(p.ProjectID); running > 0 {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				fmt.Sprintf("该项目还有 %d 个会话正在运行——请先停止它们再移除项目", running))
		}
		// 会话条数在**移除之前**数（移除会把归属清空，之后数出来是 0）——
		// 结果回给调用方做提示文案：「N 条会话保留在库里（落进未分组）」
		n, err := s.st.ProjectSessionCount(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if err := s.st.RemoveProject(p.ProjectID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventProjectChanged, map[string]any{})
		return protocol.NewResult(req.ID, protocol.ProjectRemoveResult{Removed: true, Sessions: n})

	case protocol.MethodProjectList:
		metas, err := s.st.ListProjects()
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		out := make([]protocol.ProjectMeta, len(metas))
		for i, m := range metas {
			out[i] = protocol.ProjectMeta{ID: m.ID, Name: m.Name, Path: m.Path}
		}
		return protocol.NewResult(req.ID, out)

	// 项目守则（项目根 AGENTS.md）读写——「自定义指令」的项目级入口。
	// 只按项目 id 寻址：路径由服务端从项目根解析（客户端传不了任意路径）。
	case protocol.MethodProjectInstructionsGet:
		var p protocol.ProjectInstructionsParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 project_id")
		}
		meta, ok, err := s.st.ProjectByID(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "项目不存在: "+p.ProjectID)
		}
		f := project.LoadInstructions(meta.Path)
		return protocol.NewResult(req.ID, protocol.ProjectInstructionsResult{
			Path: f.Path, Content: f.Content, Exists: f.Exists, Note: f.Note,
		})

	case protocol.MethodProjectInstructionsSave:
		var p protocol.ProjectInstructionsParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 project_id")
		}
		meta, ok, err := s.st.ProjectByID(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "项目不存在: "+p.ProjectID)
		}
		path, err := project.WriteInstructions(meta.Path, p.Content)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 无需广播：守则是每轮现读进提示词，不参与客户端间同步的状态
		return protocol.NewResult(req.ID, protocol.ProjectInstructionsResult{
			Path: path, Content: p.Content, Exists: true,
		})
	}
	return nil
}
