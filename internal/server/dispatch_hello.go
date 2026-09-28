// 握手：单独一个文件而不是并进某个方法域——它是唯一「命中后可能合法地返回 nil」的方法

package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) handleHello(req *protocol.Request, params json.RawMessage, isNotification bool) *protocol.Response {
	switch req.Method {
	case protocol.MethodHello:
		var p protocol.HelloParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		log.Printf("客户端接入: %s (协议 %s)", p.Client, p.Version)
		if isNotification {
			return nil
		}
		if p.Version != protocol.Version {
			return protocol.NewError(req.ID, protocol.CodeVersionMismatch,
				fmt.Sprintf("协议版本不兼容：客户端 %q，服务端 %q", p.Version, protocol.Version))
		}
		return protocol.NewResult(req.ID, protocol.HelloResult{
			Server: "lxcode", Version: protocol.Version, Busy: false,
		})
	}
	return nil
}
