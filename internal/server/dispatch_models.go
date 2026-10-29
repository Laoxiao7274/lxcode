// 模型注册表域：model.*（列表 / 增删改 / 启停 / 角色绑定 / 目录与端点探测）。

package server

import (
	"encoding/json"
	"fmt"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/modelcatalog"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) dispatchModels(req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodModelList:
		return protocol.NewResult(req.ID, s.modelList())

	case protocol.MethodModelAdd, protocol.MethodModelUpdate:
		var m config.ModelConfig
		if err := json.Unmarshal(params, &m); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		var err error
		if req.Method == protocol.MethodModelAdd {
			err = s.reg.Add(m)
		} else {
			err = s.reg.Update(m)
		}
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodModelRemove:
		var p protocol.ModelRemoveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.reg.Remove(p.ID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodModelEnable:
		var p protocol.ModelEnableParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		m, ok := s.reg.Get(p.ID)
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "模型不存在: "+p.ID)
		}
		m.Enabled = p.Enabled
		if err := s.reg.Update(m); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodRoleSet:
		var p protocol.RoleSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.reg.SetRole(p.Role, p.ModelID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	// ---- 模型目录与端点探测（不改注册表：只回建议，用户勾选后走 model.add）----

	case protocol.MethodModelCatalogList:
		var p protocol.ModelCatalogListParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.catalog == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "模型目录服务未装配")
		}
		// 用服务基上下文而不是请求上下文：分发出在 WS 读循环里，没有请求级
		// ctx 可传，而这里要打网络——没有它，停机时在途的拉取只能干等自己的
		// 超时（与 search.test 同一条理由）。
		list, err := s.catalog.Providers(s.Ctx(), p.Refresh)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, list)

	case protocol.MethodModelCatalogModels:
		var p protocol.ModelCatalogModelsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.catalog == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "模型目录服务未装配")
		}
		list, err := s.catalog.Models(s.Ctx(), p.Provider, p.Refresh)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, list)

	case protocol.MethodModelDiscover:
		var p protocol.ModelDiscoverParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if s.catalog == nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "模型目录服务未装配")
		}
		in, err := s.discoverInput(p)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		res, err := s.catalog.Discover(s.Ctx(), in)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, res)
	}
	return nil
}

// discoverInput 把探测参数解析成目录服务的输入。
//
// ID 优先：已注册条目自带 base_url / key / format——前端不必把 key 再送一遍，
// 也就不存在「前端手里的 key 与注册表里的不一致」这种分叉。
func (s *Server) discoverInput(p protocol.ModelDiscoverParams) (modelcatalog.DiscoverInput, error) {
	if p.ID != "" {
		m, ok := s.reg.Get(p.ID)
		if !ok {
			return modelcatalog.DiscoverInput{}, fmt.Errorf("模型不存在: %s", p.ID)
		}
		return modelcatalog.DiscoverInput{BaseURL: m.BaseURL, APIKey: m.APIKey, Format: m.EffectiveFormat()}, nil
	}
	return modelcatalog.DiscoverInput{BaseURL: p.BaseURL, APIKey: p.APIKey, Format: p.Format}, nil
}

// AttachModelCatalog 装配可选模型目录服务（不装配时目录与探测方法回「未装配」）。
//
// 与 AttachSearch 的差别：目录是**只读查询**——不写配置、不改工具注册表、
// 不广播事件，所以没有变更回调可挂，也没有热加载要接。
func (s *Server) AttachModelCatalog(svc *modelcatalog.Service) {
	s.catalog = svc
}
