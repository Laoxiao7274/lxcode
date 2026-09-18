// Agent 注册表与拓展目录的协议分发（M1——store 直通 + 协议映射层）。
// 分层纪律：store 类型过协议边界必须经映射（历史 bug 两次：直接序列化
// store 类型漏 json tag，前端拿到全大写键）；错误自解释（校验拒绝带
// 引用方名单）。
package server

import (
	"encoding/json"
	"strings"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// ---- 映射（store/sessiondata → protocol 载荷） ----

func toProtocolAgents(list []sessiondata.AgentDef) []protocol.AgentEntry {
	out := make([]protocol.AgentEntry, len(list))
	for i, a := range list {
		out[i] = protocol.AgentEntry{
			ID: a.ID, Name: a.Name, Desc: a.Desc, Color: a.Color, Model: a.Model,
			Tools: a.Tools, Workflow: a.Workflow, Skills: a.Skills, Delegates: a.Delegates,
			Approval: a.Approval, Enabled: a.Enabled, IsMain: a.IsMain, Prompt: a.Prompt,
			Protocol: a.Protocol, Custom: a.Custom,
		}
	}
	return out
}

func fromProtocolAgent(e protocol.AgentEntry) sessiondata.AgentDef {
	return sessiondata.AgentDef{
		ID: e.ID, Name: e.Name, Desc: e.Desc, Color: e.Color, Model: e.Model,
		Tools: e.Tools, Workflow: e.Workflow, Skills: e.Skills, Delegates: e.Delegates,
		Approval: e.Approval, Enabled: e.Enabled, IsMain: e.IsMain, Prompt: e.Prompt,
		Protocol: e.Protocol, Custom: e.Custom,
	}
}

func toProtocolModules(list []sessiondata.ModuleSpec) []protocol.ModuleEntry {
	out := make([]protocol.ModuleEntry, len(list))
	for i, m := range list {
		out[i] = protocol.ModuleEntry{ID: m.ID, Desc: m.Desc, Kind: m.Kind, Body: m.Body, Custom: m.Custom}
	}
	return out
}

func toProtocolTools(list []sessiondata.ToolSpec) []protocol.ToolEntry {
	out := make([]protocol.ToolEntry, len(list))
	for i, t := range list {
		params := make([]protocol.ToolParamEntry, len(t.Params))
		for j, p := range t.Params {
			params[j] = protocol.ToolParamEntry{Name: p.Name, Type: p.Type, Required: p.Required, Desc: p.Desc}
		}
		out[i] = protocol.ToolEntry{
			ID: t.ID, Desc: t.Desc, Risk: t.Risk, Source: t.Source, Params: params,
			Doc: t.Doc, Server: t.Server, Command: t.Command, Example: t.Example,
			PackageFile: t.PackageFile, Custom: t.Custom,
		}
	}
	return out
}

func fromProtocolTool(e protocol.ToolEntry) sessiondata.ToolSpec {
	params := make([]sessiondata.ToolParam, len(e.Params))
	for j, p := range e.Params {
		params[j] = sessiondata.ToolParam{Name: p.Name, Type: p.Type, Required: p.Required, Desc: p.Desc}
	}
	return sessiondata.ToolSpec{
		ID: e.ID, Desc: e.Desc, Risk: e.Risk, Source: e.Source, Params: params,
		Doc: e.Doc, Server: e.Server, Command: e.Command, Example: e.Example,
		PackageFile: e.PackageFile, Custom: e.Custom,
	}
}

func toProtocolMcServers(list []sessiondata.McServerSpec) []protocol.McServerEntry {
	out := make([]protocol.McServerEntry, len(list))
	for i, m := range list {
		out[i] = protocol.McServerEntry{
			ID: m.ID, Desc: m.Desc, Transport: m.Transport, Command: m.Command, Args: m.Args,
			Env: m.Env, URL: m.URL, Enabled: m.Enabled, Custom: m.Custom,
		}
	}
	return out
}

// dispatchAgentCatalog 处理 agent.*/catalog.* 方法（store 未挂载时拒绝——
// 无持久化形态没有注册表可言）。
func (s *Server) dispatchAgentCatalog(id json.RawMessage, method string, params json.RawMessage) *protocol.Response {
	if s.st == nil {
		return protocol.NewError(id, protocol.CodeInvalidParams, "会话存储未挂载（--sessions）——Agent 注册表不可用")
	}
	switch method {

	// ---- agents ----
	case protocol.MethodAgentList:
		list, err := s.st.ListAgents()
		if err != nil {
			return protocol.NewError(id, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(id, toProtocolAgents(list))

	case protocol.MethodAgentAdd:
		var p protocol.AgentAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.st.AddAgent(fromProtocolAgent(p.Agent)); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventAgentChanged, protocol.AgentChangedParams{Reason: "add"})
		return protocol.NewResult(id, map[string]any{})

	case protocol.MethodAgentUpdate:
		var p protocol.AgentAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.st.UpdateAgent(fromProtocolAgent(p.Agent)); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventAgentChanged, protocol.AgentChangedParams{Reason: "update"})
		return protocol.NewResult(id, map[string]any{})

	case protocol.MethodAgentRemove:
		var p protocol.AgentRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.ID) == "" {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.RemoveAgent(p.ID); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventAgentChanged, protocol.AgentChangedParams{Reason: "remove"})
		return protocol.NewResult(id, map[string]any{})

	// ---- catalog.modules ----
	case protocol.MethodCatalogModuleList:
		list, err := s.st.ListModules()
		if err != nil {
			return protocol.NewError(id, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(id, toProtocolModules(list))

	case protocol.MethodCatalogModuleAdd, protocol.MethodCatalogModuleUpdate:
		var p protocol.ModuleAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		m := sessiondata.ModuleSpec{ID: p.Module.ID, Desc: p.Module.Desc, Kind: p.Module.Kind, Body: p.Module.Body, Custom: p.Module.Custom}
		var err error
		if method == protocol.MethodCatalogModuleAdd {
			err = s.st.AddModule(m)
		} else {
			err = s.st.UpdateModule(m)
		}
		if err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "modules", Reason: "update"})
		return protocol.NewResult(id, map[string]any{})

	case protocol.MethodCatalogModuleRemove:
		var p protocol.ModuleRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.RemoveModule(p.ID); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "modules", Reason: "remove"})
		return protocol.NewResult(id, map[string]any{})

	// ---- catalog.tools ----
	case protocol.MethodCatalogToolList:
		list, err := s.st.ListTools()
		if err != nil {
			return protocol.NewError(id, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(id, toProtocolTools(list))

	case protocol.MethodCatalogToolAdd, protocol.MethodCatalogToolUpdate:
		var p protocol.ToolAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		t := fromProtocolTool(p.Tool)
		var err error
		if method == protocol.MethodCatalogToolAdd {
			err = s.st.AddTool(t)
		} else {
			err = s.st.UpdateTool(t)
		}
		if err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "tools", Reason: "update"})
		return protocol.NewResult(id, map[string]any{})

	case protocol.MethodCatalogToolRemove:
		var p protocol.ToolRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.RemoveTool(p.ID); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "tools", Reason: "remove"})
		return protocol.NewResult(id, map[string]any{})

	// ---- catalog.mcp ----
	case protocol.MethodCatalogMcpList:
		list, err := s.st.ListMcServers()
		if err != nil {
			return protocol.NewError(id, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(id, toProtocolMcServers(list))

	case protocol.MethodCatalogMcpAdd, protocol.MethodCatalogMcpUpdate:
		var p protocol.McServerAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		m := sessiondata.McServerSpec{
			ID: p.Server.ID, Desc: p.Server.Desc, Transport: p.Server.Transport, Command: p.Server.Command,
			Args: p.Server.Args, Env: p.Server.Env, URL: p.Server.URL, Enabled: p.Server.Enabled, Custom: p.Server.Custom,
		}
		var err error
		if method == protocol.MethodCatalogMcpAdd {
			err = s.st.AddMcServer(m)
		} else {
			err = s.st.UpdateMcServer(m)
		}
		if err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "mcp", Reason: "update"})
		return protocol.NewResult(id, map[string]any{})

	case protocol.MethodCatalogMcpRemove:
		var p protocol.McServerRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(id, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.RemoveMcServer(p.ID); err != nil {
			return protocol.NewError(id, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventCatalogChanged, protocol.CatalogChangedParams{Kind: "mcp", Reason: "remove"})
		return protocol.NewResult(id, map[string]any{})
	}
	return nil // 未命中（调用方回落 unknown method）
}
