package server

import "github.com/moyunteng/lxcode/internal/tools"

// AttachSSHHostLookup 装配 ssh_hosts 工具的主机别名解析（hosts.json 读取口，
// serve.go 的 sshHostLookupOf 构造闭包）。nil = 不装配（工具回「未配置」）。
//
// hosts.json 里是凭据（密码支持 ${ENV_VAR} 引用），清单快照在装配时确定——
// 不做周期热加载（改配置重启后端生效，见 serve.go 的理由）。
func (s *Server) AttachSSHHostLookup(fn tools.SSHHostLookupFunc) {
	if s.treg != nil {
		s.treg.SetSSHHostLookup(fn)
	}
}
