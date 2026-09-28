// 项目域：project.*（增列 / 项目守则读写——只按项目 id 寻址）。

package server

import (
	"encoding/json"
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
