// 模型注册表域：model.*（列表 / 增删改 / 启停 / 角色绑定）。

package server

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/config"
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
	}
	return nil
}
