// 助手域（assistant space，助手模式 P0）：并列运行时 + 内置助手 Agent 解析器。
//
// 与主空间的分工：助手会话是**另一个域**——独立的运行时缓存（assistantSessions，
// 与主空间的 sessions 并列同锁）、独立的 Agent 语境（assistantAgentResolver，
// 内置常量 + 从库里现读 Delegates）、独立的方法域（assistant.*，dispatch_assistant.go）
// 与事件域（emit.go 的 assistantEventName 前缀映射）。主空间的函数
// （busySessions/cancelChildAnywhere/session(id) 等）**零改动**——两域的 id 空间
// 不会重叠（各自建行各自建运行时），不需要跨域互查。
package server

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
)

// assistantAgentDef 是内置助手 Agent 的定义常量（P0 不落库——它是代码里的
// 结构成员，与主 Agent 的「结构性」同位；后续要做可编辑再迁移到种子表）。
// Protocol 空 = DefaultProtocol(ProtocolRoleAssistant)（compose.go 的 switch
// 按 Space=assistant 推导）。
var assistantAgentDef = sessiondata.AgentDef{
	ID: "assistant", Name: "助手", IsMain: false, Enabled: true,
	Color: "#8b5cf6",
	Desc:  "通用个人助理：日常事务自己干，项目任务经 run_task 派进项目。",
	// 工具白名单（用户拍板）：只读面 + run_task 能力桥——不含 edit / write_file /
	// bash（改动必须经 run_task 派进项目）。ask_user 在冲突抉择时向用户提问；
	// todo 是会话级清单；ssh_hosts 不默认给（远程执行要显式勾选）；
	// project_list 是项目全景查询（「我有哪些项目」的直接答案权 + run_task
	// 的 project_id 来源，2026-12 助手项目全景三件套）；update_assistant_config
	// 是助手自改配置（2026-12 用户拍板：助手能自己改自己的性格/称呼——只写
	// assistant.json，低危）；mcp_manage 是 MCP 服务器的查询/安装/修改/删除
	//（2026-12 用户拍板：助手有 MCP 管理能力且知道自己有——add 默认自动绑定，
	// 单工具高危确认门，auto 档放行）。
	Tools: []string{
		"read_file", "search", "read_image", "bash_readonly",
		"web_search", "web_fetch", "project_list", "session_search", "todo", "ask_user", "read_skill", "run_task",
		"update_assistant_config", "mcp_manage",
	},
	Delegates: []string{}, // 现读：st.ListAgents() ∩ 启用（assistantAgentResolver 现算）
	Approval:  "auto",
	Model:     "", // 空 = 回落 default 角色（用户在设置里绑哪个模型就走哪个）
	Space:     sessiondata.SpaceAssistant,
}

// assistantAgentResolver 是助手会话的 Agent 语境解析器：助手定义是内置常量，
// 配置（assistant.json）与 Delegates（可委派名单 = 全部启用 Agent）从各自事实源
// 现读——与 storeAgentResolver 同一套「装配期常量 + 运行期现读」纪律，Agent
// 名单与助手配置变更都不需要重启。
//
// config 与 modules 的读取口在装配时注入（AssistantConfigLookup / ModuleSource）：
// resolver 不持有 *Server 引用（避免 server ↔ server 的自引用）；未注入 = 配置
// 空、模块目录空——merge 全走内置值（与 store 为 nil 的 enabledAgents 同一条
// 降级语义）。
type assistantAgentResolver struct {
	st       *store.Store
	configFn func() config.Assistant
	modFn    func() []sessiondata.ModuleSpec
}

// AssistantConfigLookup 是「现读助手配置」的读取口约定（server 装配层构造闭包，
// 内部按配置路径现读文件）。
type AssistantConfigLookup = func() config.Assistant

// ModuleSource 是「现读模块目录」的读取口约定（同一条注入理由）。
type ModuleSource = func() []sessiondata.ModuleSpec

func (r *assistantAgentResolver) config() config.Assistant {
	if r.configFn == nil {
		return config.Assistant{}
	}
	return r.configFn()
}

func (r *assistantAgentResolver) modules() []sessiondata.ModuleSpec {
	if r.modFn == nil {
		return nil
	}
	return r.modFn()
}

// assistantName 取 merge 后的显示名（ResolveByName 的命中判定用；配置未注入
// 时回落内置名）。
func (r *assistantAgentResolver) assistantName() string {
	cfg := r.config()
	if cfg.Name != "" {
		return cfg.Name
	}
	return assistantAgentDef.Name
}

func (r *assistantAgentResolver) Resolve(id string) (*sessiondata.AgentContext, bool) {
	if id != assistantAgentDef.ID && id != "" {
		// 助手会话直发非助手 Agent（WithAgent("coder")）？不拦——名单解析按
		// 库里现读（与主域同一条路径），四层组合按被指名的 Agent 走。
		return r.resolveStored(id)
	}
	return r.resolveAssistant(), true
}

func (r *assistantAgentResolver) ResolveByName(name string) (*sessiondata.AgentContext, bool) {
	if name == assistantAgentDef.Name || (name != "" && r.assistantName() == name) {
		// 注意：用户改过显示名后，旧名「助手」不再命中 ResolveByName（名单卡上
		// 看到的名字是新的，按新名寻址才自洽）；空名回落内置。
		return r.resolveAssistant(), true
	}
	return r.resolveStoredByName(name)
}

// resolveAssistant 组装助手 Agent 语境：内置常量副本 + 现读配置 merge（Name/
// Persona/Skills/Workflow）+ 现读 Delegates。
//
// 只读配置不改 assistantAgentDef 常量本体（进程级共享，写它就是跨会话污染）。
// Delegates 从库里现读——Agent 名单变更不需要重启。
func (r *assistantAgentResolver) resolveAssistant() *sessiondata.AgentContext {
	def := r.mergedAssistantDef()
	ac := &sessiondata.AgentContext{Def: def}
	ac.Delegates = r.enabledAgents()
	// Skills/Workflow 灌 ac：resolver 现读 modules 匹配（照 agent_resolver.go
	// 的 storeAgentResolver 同款过滤）——配置里声明了但目录里没有的 id 静默跳过
	//（「白名单里的 id 不存在 = 声明错了」的同一套宽容；set 时已现读校验，
	// 这里兜的是手改文件/热加载窗口）。
	mods := r.modules()
	for i := range mods {
		if def.Workflow == mods[i].ID {
			m := mods[i]
			ac.Workflow = &m
		}
		if slices.Contains(def.Skills, mods[i].ID) {
			ac.Skills = append(ac.Skills, mods[i])
		}
	}
	return ac
}

// mergedAssistantDef 内置定义 + 配置覆盖的副本。只覆盖**非空**字段（空 = 用
// 内置值）；Persona 追加进 Def.Prompt（第三层，非整段替换——内置 Prompt 现在
// 为空，追加与替换等价，但语义上 persona 是「补充设定」不是「重写职责边界」）。
//
// 工具白名单（2026-12 用户拍板：助手默认工具完全可配）按三段合成：
//   - base = cfg.ToolWhitelist 非空 ? whitelist : 内置 14 项——whitelist 是
//     **完整覆盖**（内置 bash/edit 也可勾入，用户担责）；whitelist 里悬空的
//     id（配置声明了但工具已不在目录）按目录过滤掉，st 为 nil 不过滤（纯内存
//     模式无从查，照常放行）；
//   - 旧 cfg.Tools（自定义命令工具追加）**仅在 whitelist 为空时生效**——双源
//     叠加会让「勾掉的工具经旧通道回来」，过渡期旧行为只在用户没碰新字段时
//     保持；
//   - MCP 工具**恒最后追加**（mcpServers 是独立语义的动态面，与静态面正交
//     ——whitelist 覆盖不含 MCP；mergeMCPTools 新建切片，不碰共享底层）。
//
// MCP 服务器绑定（cfg.MCPServers 非空）在这里现读现合：经 st.ListTools() 过滤
// 出 Source=="mcp" && Server∈声明集合 的条目，**新建切片**合入 def.Tools
// ——严禁 append 到 assistantAgentDef.Tools 的共享底层（值拷贝共享数组，append
// 会写穿常量本体，跨会话污染）。现读现算 = 服务器启停/条目撤下**下一轮自动
// 生效**；合入后仍悬空的名字（如 MCP 服务器未启用）由 compose 层的 missingTools
// 提示兜住，不在这里二次猜。
func (r *assistantAgentResolver) mergedAssistantDef() sessiondata.AgentDef {
	def := assistantAgentDef // 值拷贝（Tools/Delegates 切片共享底层——只读不改，安全）
	cfg := r.config()
	if cfg.Name != "" {
		def.Name = cfg.Name
	}
	if cfg.Persona != "" {
		def.Prompt = strings.TrimSpace(def.Prompt + "\n\n" + cfg.Persona)
	}
	if len(cfg.Skills) > 0 {
		def.Skills = cfg.Skills
	}
	if cfg.Workflow != "" {
		def.Workflow = cfg.Workflow
	}
	switch {
	case len(cfg.ToolWhitelist) > 0:
		def.Tools = r.filterKnownTools(cfg.ToolWhitelist)
	case len(cfg.Tools) > 0:
		def.Tools = r.mergeCustomTools(def.Tools, cfg.Tools)
	}
	if len(cfg.MCPServers) > 0 {
		def.Tools = r.mergeMCPTools(def.Tools, cfg.MCPServers)
	}
	return def
}

// filterKnownTools 把 whitelist 里目录中真实存在的 id 过滤出来（**新建切片**，
// 顺序按声明序）。白名单是完整覆盖语义，所以是**过滤**而不是追加；目录读失败
// = 不过滤照常放行（降级可见——悬空 id 由 compose 层 missingTools 兜住）。
// st 为 nil（纯内存模式）照常放行。
func (r *assistantAgentResolver) filterKnownTools(ids []string) []string {
	if r.st == nil {
		return ids
	}
	specs, err := r.st.ListTools()
	if err != nil {
		log.Printf("读工具目录失败（本轮白名单不做存在性过滤）: %v", err)
		return ids
	}
	known := make(map[string]bool, len(specs))
	for i := range specs {
		known[specs[i].ID] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if known[id] {
			out = append(out, id)
		}
	}
	return out
}

// mergeMCPTools 把声明集合里的 MCP 工具合入白名单（**新建切片**，不碰入参的
// 共享底层）。绑定的是**服务器 id**——该服务器已物化（Source=="mcp"）的全部
// 目录条目按 Server 过滤合入，ID 即暴露名（<server>_<tool>）。
func (r *assistantAgentResolver) mergeMCPTools(base, servers []string) []string {
	if r.st == nil {
		return base
	}
	specs, err := r.st.ListTools()
	if err != nil {
		// 目录读失败 = 这轮不合入（内置白名单照常——降级可见，不编人）。
		log.Printf("读工具目录失败（本轮不合入 MCP 服务器工具）: %v", err)
		return base
	}
	declared := make(map[string]bool, len(servers))
	for _, id := range servers {
		declared[id] = true
	}
	// 顺序：内置白名单原样在前，MCP 工具按目录序在后（重复合入防御性去重）。
	seen := make(map[string]bool, len(base)+8)
	out := make([]string, 0, len(base)+8)
	for _, name := range base {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for i := range specs {
		spec := &specs[i]
		if spec.Source != "mcp" || !declared[spec.Server] {
			continue
		}
		if !seen[spec.ID] {
			seen[spec.ID] = true
			out = append(out, spec.ID)
		}
	}
	return out
}

// mergeCustomTools 把声明集合里的**自定义命令工具**合入白名单（**新建切片**，
// 不碰入参的共享底层——mergeMCPTools 同一条硬边界）。绑定的是条目 id（目录
// 粒度）：只有 custom=true && source=="binary" 的条目合入——内置工具
//（bash/edit 等，seedTools 也在 tools 表里）与非 custom 条目声明了也滤掉，
// 不报错（set 时已人话拒绝，这里兜手改文件/热加载窗口）。目录读失败 = 这轮
// 不合入（内置白名单照常——降级可见，不编人）。
func (r *assistantAgentResolver) mergeCustomTools(base, ids []string) []string {
	if r.st == nil {
		return base
	}
	specs, err := r.st.ListTools()
	if err != nil {
		log.Printf("读工具目录失败（本轮不合入自定义工具）: %v", err)
		return base
	}
	declared := make(map[string]bool, len(ids))
	for _, id := range ids {
		declared[id] = true
	}
	// 顺序：现有白名单原样在前，命中的自定义工具按目录序在后（重复合入防御
	// 性去重——与 mergeMCPTools 同款）。
	seen := make(map[string]bool, len(base)+8)
	out := make([]string, 0, len(base)+8)
	for _, name := range base {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for i := range specs {
		spec := &specs[i]
		if spec.Source != "binary" || !spec.Custom || !declared[spec.ID] {
			continue
		}
		if !seen[spec.ID] {
			seen[spec.ID] = true
			out = append(out, spec.ID)
		}
	}
	return out
}

// enabledAgents 列出全部启用的 Agent（run_task 的可选 agent 参数候选）。
// 库读失败返回空（助手提示词就没有委派名单——降级可见，不编人）。
func (r *assistantAgentResolver) enabledAgents() []sessiondata.AgentDef {
	if r.st == nil {
		return nil
	}
	agents, err := r.st.ListAgents()
	if err != nil {
		log.Printf("助手委派名单现读失败（本轮没有可委派名单）: %v", err)
		return nil
	}
	var out []sessiondata.AgentDef
	for i := range agents {
		if agents[i].Enabled && !agents[i].IsMain {
			out = append(out, agents[i])
		}
	}
	return out
}

func (r *assistantAgentResolver) resolveStored(id string) (*sessiondata.AgentContext, bool) {
	agents, err := r.st.ListAgents()
	if err != nil {
		return nil, false
	}
	for i := range agents {
		if agents[i].ID == id {
			agents[i].Space = sessiondata.SpaceMain
			return &sessiondata.AgentContext{Def: agents[i], Delegates: r.enabledAgents()}, true
		}
	}
	return nil, false
}

func (r *assistantAgentResolver) resolveStoredByName(name string) (*sessiondata.AgentContext, bool) {
	agents, err := r.st.ListAgents()
	if err != nil {
		return nil, false
	}
	for i := range agents {
		if agents[i].Name == name {
			agents[i].Space = sessiondata.SpaceMain
			return &sessiondata.AgentContext{Def: agents[i], Delegates: r.enabledAgents()}, true
		}
	}
	return nil, false
}

// newAssistantRuntime 构建助手会话运行时（与 newRuntime 同构，三处刻意不同）：
//   - Agent 解析器 = assistantAgentResolver（内置助手定义 + 库现读名单）；
//   - 不挂 JobNotify / ProjectDocs：助手的通告按事件域走（前台收 assistant.*），
//     它没有项目归属，项目守则无从谈起；**挂 ProjectPanorama**（项目全景
//     注入段）——助手的 run_task 需要 project_id，而 id 是不可读的时间戳
//     随机串，注入段是它唯一的常驻来源（主域项目会话不挂：已有归属与
//     AGENTS.md 注入，跨项目全景对它没有行动价值）；
//   - AttachTo 之外，新会话首次 send 前允许懒建（EnablePersistence 的空 id 路径
//     不适用——助手会话必须显式建行落 space=assistant，由 assistantSession 创建）；
//   - AttachTo 成功后注入助手专属工作目录（assistantWorkDir 非空时）——
//     AttachTo 每次都按库值重写 workDir，注入必须在其后（顺序反了会被
//     AttachTo 的空 workspace 解析覆盖回进程目录）。
//
// 审批档恢复与主域同款（behavior 默认档兜底）。
func (s *Server) newAssistantRuntime(id string) (*agent.Session, error) {
	sess := agent.New(s.reg, s.treg, func(ev agent.Event) { s.emitEvent(id, ev) })
	if s.stream != nil {
		sess.SetStream(s.stream)
	}
	sess.SetAgentResolver(&assistantAgentResolver{
		st:       s.st,
		configFn: s.assistantConfig,
		modFn:    s.modulesSnapshot,
	})
	// 项目全景注入（助手语境限定——Session.projectPanoramaFor 里还按
	// Space=assistant 二次判定，这里挂的是数据源）。每轮组装时现读
	//（每轮快照，与 project_list 工具的实时查询同源 ListProjectStats）。
	if s.st != nil {
		pano := s.st
		sess.SetProjectPanorama(func() []sessiondata.ProjectStats {
			stats, err := pano.ListProjectStats()
			if err != nil {
				// 读失败 = 这轮不注入（助手不知道项目清单好过注入半份错的）；
				// 只记日志，不打断本轮。
				log.Printf("读项目全景失败（本轮不注入可用项目段）: %v", err)
				return nil
			}
			return stats
		})
		// 「当前状态」注入段（感知用户当前活动，2026-12）：每轮现读一份
		// 快照——最近会话（List 头部几条）+ 此刻运行中的会话。项目名在这里
		// 解析成文本（agent 层不查库）；项目映射用 ListProjects 一次拉全
		//（运行中的会话寥寥，逐个 WorkspaceOf 反而更多往返）。锁纪律：st 的
		// 库锁与 s.busySessions 的 sessionsMu 是两把锁——回调里先后调用、
		// 不嵌套持锁。读失败整段缺席（不注入半份错的）；未分组的会话项目名
		// 记「主域 · 未分组」（域前缀——呈现层归属标注，见 describe 注释）。
		sess.SetCurrentActivity(func() agent.ActivitySnapshot {
			recent, err := s.st.List()
			if err != nil {
				log.Printf("读最近会话失败（本轮不注入当前状态段）: %v", err)
				return agent.ActivitySnapshot{}
			}
			projects, err := s.st.ListProjects()
			if err != nil {
				log.Printf("读项目名映射失败（本轮不注入当前状态段）: %v", err)
				return agent.ActivitySnapshot{}
			}
			names := make(map[string]string, len(projects))
			for i := range projects {
				names[projects[i].ID] = projects[i].Name
			}
			// describe 的项目名带**域前缀**（2026-12 呈现层归属标注）：注入段
			// 都是主域（项目模式）会话，但「未分组」光秃秃三个字是个弱线索——
			// 助手实测把主域未分组会话当成了「咱俩的对话」。项目名风格与
			// projectPanoramaSection 一致（都是名字/明确标识）。
			describe := func(meta store.SessionMeta) string {
				when := ""
				if meta.UpdatedAt != "" {
					when = "，" + meta.UpdatedAt
				}
				project := "主域 · 未分组"
				if meta.Workspace != "" {
					if name := names[meta.Workspace]; name != "" {
						project = name
					}
				}
				return fmt.Sprintf("「%s」（%s%s）", meta.Title, project, when)
			}
			snap := agent.ActivitySnapshot{}
			for i := range recent {
				if len(snap.Recent) >= agent.ActivityRecentCap {
					break
				}
				snap.Recent = append(snap.Recent, describe(recent[i]))
			}
			for _, id := range s.busySessions() {
				meta, err := s.st.SessionMetaOf(id)
				if err != nil {
					continue // 归属查不动不硬猜（刚建未落盘等窗口态）
				}
				snap.Running = append(snap.Running, describe(meta))
			}
			return snap
		})
	}
	if s.st != nil {
		if err := sess.AttachTo(s.st, id); err != nil {
			return nil, err
		}
		// 助手专属工作目录（<sessions>/../assistant，runServe 装配）：助手会话
		// 没有项目归属，AttachTo 解析出的 workDir 是空——工具的相对路径与
		// bash 默认目录此前落后端进程 cwd（dev=仓库根、SCM=System32，都不该）。
		// 必须在 AttachTo **之后**（它每次重写 workDir）。不落库纯内存注入：
		// 不造伪项目行，重启由同一装配路径重注入。空 = 未装配（内存测试模式
		// 保持旧行为）。注意 run_task 子会话不受影响——RunProjectTask 在
		// openChildSession 之后显式 SetWorkDir(项目根) 覆盖。
		if s.assistantWorkDir != "" {
			if err := sess.SetWorkDir(s.assistantWorkDir); err != nil {
				return nil, err
			}
		}
		if stored, err := s.st.ApprovalOf(id); err != nil {
			log.Printf("读助手会话审批档失败（回落默认档）: %v", err)
		} else if stored == "" {
			if def := s.behaviorConfig().Approval; def != "" && def != "confirm" {
				sess.SetApproval(def)
			}
		}
	}
	return sess, nil
}

// modulesSnapshot 现读模块目录（resolver 的 modFn 读取口——merge Skills/Workflow
// 的事实源）。读失败返回 nil（本轮技能/模板不注入——降级可见，不编人）。
func (s *Server) modulesSnapshot() []sessiondata.ModuleSpec {
	if s.st == nil {
		return nil
	}
	mods, err := s.st.ListModules()
	if err != nil {
		log.Printf("读模块目录失败（本轮不注入技能/模板）: %v", err)
		return nil
	}
	return mods
}

// assistantSession 取助手会话运行时（未打开的现建一份——与主域 session(id)
// 同一语义；子会话库行继承父 space='assistant'，故子会话历史同样走
// assistant.history 域内寻址，CreateChildIn 落库时已继承该域）。
func (s *Server) assistantSession(id string) (*agent.Session, error) {
	s.sessionsMu.RLock()
	sess := s.assistantSessions[id]
	s.sessionsMu.RUnlock()
	if sess != nil {
		return sess, nil
	}
	if s.st == nil {
		return nil, errors.New("会话存储未启用")
	}
	sess, err := s.newAssistantRuntime(id)
	if err != nil {
		return nil, err
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if existing := s.assistantSessions[id]; existing != nil {
		return existing, nil
	}
	s.assistantSessions[id] = sess
	return sess, nil
}

// peekAssistantSession 只看内存里的助手运行时，不创建（纯读场景）。
func (s *Server) peekAssistantSession(id string) *agent.Session {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.assistantSessions[id]
}

// isAssistantSession 报告 id 是否是助手域会话（事件域映射的判定源）：
// 内存里有助手运行时即助手域；不在内存时查库的 space 列（重启恢复后
// 事件映射仍正确——emitEvent 是热路径，这里多一次点查只在会话不在内存时
// 发生，而事件只从**活着**的运行时发出，活着必在内存；库查是防御性兜底，
// 正常路径不走）。
func (s *Server) isAssistantSession(id string) bool {
	if id == "" {
		return false
	}
	if sess := s.peekAssistantSession(id); sess != nil {
		return true
	}
	return false
}

// broadcastAssistant 是助手域会话状态的广播入口（approval/model 等会话级
// 状态由 dispatch_assistant 显式广播，不经 emitEvent 的内核事件路径——
// 裸 s.broadcast 会以 chat 前缀发出）。与 emitEvent 的 emit 同一套域判定：
// 助手域会话换 assistant 前缀，其余原样（防御性——助手域方法寻址到的必是
// 助手会话，正常路径恒命中换名分支）。
func (s *Server) broadcastAssistant(sessionID, method string, params any) {
	if s.isAssistantSession(sessionID) {
		method = assistantEventName(method)
	}
	s.broadcast(method, params)
}
