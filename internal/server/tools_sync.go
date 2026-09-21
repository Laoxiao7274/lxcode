package server

import (
	"log"
	"strings"

	"github.com/moyunteng/lxcode/internal/tools"
)

// 工具目录 → 注册表的同步（M4 执行面）。
//
// 目录是事实源：用户在拓展页建/改/删自定义工具后，注册表必须跟着变——
// 否则「目录里有、模型看不见」（不注册）或「目录删了、模型还在调」（不注销）。
// 三种来源的处理：
//   - builtin：实现就在注册表里，目录条目只是元数据（描述/参数/文档）——不同步
//   - binary：命令模板经进程边界执行 → 注册（未配置 command 的跳过并记日志：
//     种子里的 ripgrep/browser 是「声明了但没装」的形态，不该让它们把
//     整份目录带下水）
//   - mcp：能力由 MCP 服务器注册时提供（M4 后半段），此处不同步
func (s *Server) syncDynamicTools() {
	if s.st == nil {
		return
	}
	list, err := s.st.ListTools()
	if err != nil {
		log.Printf("同步自定义工具失败（注册表保持原样）: %v", err)
		return
	}
	defs := make([]*tools.Def, 0, len(list))
	for _, spec := range list {
		if spec.Source != "binary" {
			continue
		}
		def, err := tools.CustomDef(spec)
		if err != nil {
			log.Printf("自定义工具 %s 暂不可用（已跳过）: %v", spec.ID, err)
			continue
		}
		defs = append(defs, def)
	}
	if skipped := s.treg.SetDynamic(defs); len(skipped) > 0 {
		log.Printf("自定义工具与内置工具同名，已跳过（内置实现优先）: %s", strings.Join(skipped, "、"))
	}
}
