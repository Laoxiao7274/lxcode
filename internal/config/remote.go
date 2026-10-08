// remote.go —— 远程访问开关与访问凭证（config/remote.json，与 models.json 同目录）。
//
// 语义：Enabled 时**所有** /rpc 连接都必须携带匹配的 token（含本机回环——
// 公网隧道（樱花frp 等）最终从本机回环进来，回环豁免 = 隧道形同虚设）。
// 壳经后端的 loopback-only HTTP 端点读取/轮换 token（internal/server/remote.go）。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

// RemoteAccess 是 remote.json 的载荷。
type RemoteAccess struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
}

// LoadRemoteAccess 读远程访问配置。文件缺失/损坏 = 未启用（不拒绝启动——
// 远程访问是可选项，坏配置不该拖垮对话主功能）。
func LoadRemoteAccess(path string) RemoteAccess {
	data, err := os.ReadFile(path)
	if err != nil {
		return RemoteAccess{}
	}
	var ra RemoteAccess
	if err := json.Unmarshal(data, &ra); err != nil {
		return RemoteAccess{}
	}
	return ra
}

// SaveRemoteAccess 原子写回（同目录临时文件 + fsync + rename——与 write_file
// 同一份实现，Windows 的 rename 不覆盖语义由 atomicfile 处理）。
func SaveRemoteAccess(path string, ra RemoteAccess) error {
	data, err := json.Marshal(ra)
	if err != nil {
		return fmt.Errorf("序列化远程访问配置失败: %w", err)
	}
	return atomicfile.Write(path, data)
}

// GenerateRemoteToken 生成 256 位随机 token（hex——URL 查询参数友好）。
func GenerateRemoteToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成访问令牌失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
