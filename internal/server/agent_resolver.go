// 委派名单解析：Agent 上下文（四层组合的输入）从库里现读。
package server

import (
	"slices"

	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
)

type storeAgentResolver struct {
	st *store.Store
}

func (r *storeAgentResolver) Resolve(agentID string) (*sessiondata.AgentContext, bool) {
	return r.resolve(func(a sessiondata.AgentDef) bool { return a.ID == agentID })
}

func (r *storeAgentResolver) ResolveByName(name string) (*sessiondata.AgentContext, bool) {
	return r.resolve(func(a sessiondata.AgentDef) bool { return a.Name == name })
}

func (r *storeAgentResolver) resolve(match func(sessiondata.AgentDef) bool) (*sessiondata.AgentContext, bool) {
	agents, err := r.st.ListAgents()
	if err != nil {
		return nil, false
	}
	var def *sessiondata.AgentDef
	for i := range agents {
		if match(agents[i]) {
			def = &agents[i]
			break
		}
	}
	if def == nil {
		return nil, false
	}
	mods, _ := r.st.ListModules()
	ac := &sessiondata.AgentContext{Def: *def}
	for i := range mods {
		if def.Workflow == mods[i].ID {
			m := mods[i]
			ac.Workflow = &m
		}
		if slices.Contains(def.Skills, mods[i].ID) {
			ac.Skills = append(ac.Skills, mods[i])
		}
	}
	// 主 Agent 的默认委派名单（有效 = 默认 ∩ 启用；会话级覆盖是前端
	// UI 态——协议接入时覆盖名单随 chat.send 计算，M3 dispatch 前够用）
	if def.IsMain {
		for i := range agents {
			if slices.Contains(def.Delegates, agents[i].ID) && agents[i].Enabled {
				ac.Delegates = append(ac.Delegates, agents[i])
			}
		}
	}
	return ac, true
}
