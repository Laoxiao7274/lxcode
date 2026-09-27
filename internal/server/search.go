package server

import (
	"encoding/json"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/websearch"
)

// AttachSearch 装配网页搜索服务：把渠道配置接进服务端与工具注册表。
//
// 两件事一起做（缺一件都不可用）：
//  1. 工具注册表挂上搜索实现——否则模型调用 web_search 会拿到「未装配」；
//  2. 服务端持引用——协议方法（渠道增删改查/测试）需要它。
//
// 变更回调也在这里挂：渠道配置变化要广播 search.changed，
// 而热加载（serve.go 的 reloadLoop）只负责调 Reload，广播由服务自己触发。
func (s *Server) AttachSearch(svc *websearch.Service) {
	s.search = svc
	if s.treg != nil {
		s.treg.SetWebSearch(svc.Search)
	}
	svc.SetNotifier(s.broadcastSearch)
}

// searchChannels 生成渠道快照载荷（也是 search.changed 的载荷）。
func (s *Server) searchChannels() protocol.SearchChannelsResult {
	if s.search == nil {
		return protocol.SearchChannelsResult{}
	}
	cfg := s.search.Config()
	return protocol.SearchChannelsResult{
		Channels: s.search.Channels(),
		Primary:  cfg.Primary,
		Ready:    s.search.Ready(),
	}
}

// broadcastSearch 渠道配置变更后通知全部客户端刷新。
func (s *Server) broadcastSearch() {
	s.broadcast(protocol.EventSearchChanged, s.searchChannels())
}

// NotifySearch 供外部（热加载发现变更）触发 search.changed 广播。
func (s *Server) NotifySearch() { s.broadcastSearch() }

// handleSearch 分发 search.* 方法；返回 nil 表示方法不在此域（交回主分发）。
//
// 单独一个文件一个 switch 的理由：server.go 的 dispatch 已经很长，
// 搜索域有 5 个方法——塞进去会让主分发再长一截且与模型域混在一起。
func (s *Server) handleSearch(req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodSearchChannelsList:
		return protocol.NewResult(req.ID, s.searchChannels())

	case protocol.MethodSearchChannelSave:
		var p protocol.SearchChannelSaveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.search == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "搜索服务未装配")
		}
		if err := s.search.SaveChannel(p.ID, websearch.ChannelConfig{
			APIKey:   p.APIKey,
			BaseURL:  p.BaseURL,
			Options:  p.Options,
			Disabled: !p.Enabled,
		}); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 保存会触发服务的变更回调（AttachSearch 里挂的 broadcastSearch），
		// 所以这里不再重复广播——重复广播会让前端白刷两次。
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSearchChannelRemove:
		var p protocol.SearchChannelRefParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.search == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "搜索服务未装配")
		}
		if err := s.search.RemoveChannel(p.ID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSearchPrimarySet:
		var p protocol.SearchPrimarySetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.search == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "搜索服务未装配")
		}
		if err := s.search.SetPrimary(p.ID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSearchTest:
		var p protocol.SearchTestParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.search == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "搜索服务未装配")
		}
		query := p.Query
		if query == "" {
			// 用户点「测试」通常不想先想关键词：给一个能验证连通性的默认词。
			query = "lxcode web search connectivity test"
		}
		start := time.Now()
		resp, err := s.search.SearchWith(s.Ctx(), p.ID, query, websearch.Options{NumResults: 3})
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, protocol.SearchTestResult{
			Provider:  resp.Provider,
			Answer:    resp.Answer,
			Results:   resp.Results,
			ElapsedMS: int(time.Since(start).Milliseconds()),
		})
	}
	return nil
}
