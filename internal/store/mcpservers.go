// MCP 服务器条目的 CRUD：服务器是接入单元，能力以工具形式进工具目录
// （source=mcp + server 指回）。停用 = 能力挂起（撤下目录条目）。
package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func (s *Store) ListMcServers() ([]sessiondata.McServerSpec, error) {
	rows, err := s.db.Query(`SELECT id, desc, transport, command, args, env, url, enabled, custom FROM mcp_servers ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出 MCP 服务器失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.McServerSpec
	for rows.Next() {
		var m sessiondata.McServerSpec
		var args, env string
		var enabled, custom int
		if err := rows.Scan(&m.ID, &m.Desc, &m.Transport, &m.Command, &args, &env, &m.URL, &enabled, &custom); err != nil {
			return nil, fmt.Errorf("读 MCP 服务器行失败: %w", err)
		}
		m.Enabled = enabled == 1
		m.Custom = custom == 1
		m.Args = decodeStrList(args)
		if env != "" && env != "{}" {
			if err := json.Unmarshal([]byte(env), &m.Env); err != nil {
				return nil, fmt.Errorf("解析 MCP 环境变量失败: %w", err)
			}
		}
		if m.Env == nil {
			m.Env = map[string]string{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddMcServer(m sessiondata.McServerSpec) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("服务器名不能为空")
	}
	if m.Transport != "stdio" && m.Transport != "sse" {
		return fmt.Errorf("transport 必须是 stdio/sse 之一")
	}
	if m.Transport == "stdio" && strings.TrimSpace(m.Command) == "" {
		return fmt.Errorf("stdio 传输需要 command（可执行文件）")
	}
	if m.Transport == "sse" && strings.TrimSpace(m.URL) == "" {
		return fmt.Errorf("sse 传输需要端点 URL")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mcp_servers WHERE id = ?`, m.ID).Scan(&n); err != nil {
		return fmt.Errorf("查重失败: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("服务器名已存在: %s", m.ID)
	}
	args, _ := json.Marshal(m.Args)
	env, err := json.Marshal(m.Env)
	if err != nil {
		return fmt.Errorf("序列化环境变量失败: %w", err)
	}
	enabled, custom := 0, 1
	if m.Enabled {
		enabled = 1
	}
	if !m.Custom {
		custom = 0
	}
	_, err = s.db.Exec(`INSERT INTO mcp_servers (id, desc, transport, command, args, env, url, enabled, custom, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Desc, m.Transport, m.Command, string(args), string(env), m.URL, enabled, custom, nowNano(), nowNano())
	if err != nil {
		return fmt.Errorf("写入 MCP 服务器失败: %w", err)
	}
	return nil
}

func (s *Store) UpdateMcServer(m sessiondata.McServerSpec) error {
	args, _ := json.Marshal(m.Args)
	env, err := json.Marshal(m.Env)
	if err != nil {
		return fmt.Errorf("序列化环境变量失败: %w", err)
	}
	enabled, custom := 0, 1
	if m.Enabled {
		enabled = 1
	}
	if !m.Custom {
		custom = 0
	}
	res, err := s.db.Exec(`UPDATE mcp_servers SET desc=?, transport=?, command=?, args=?, env=?, url=?, enabled=?, custom=?, updated_at=? WHERE id=?`,
		m.Desc, m.Transport, m.Command, string(args), string(env), m.URL, enabled, custom, nowNano(), m.ID)
	if err != nil {
		return fmt.Errorf("更新 MCP 服务器失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("MCP 服务器 %s 不存在", m.ID)
	}
	return nil
}

func (s *Store) RemoveMcServer(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback()
	// 先删该服务器的工具（若被引用，删除前显式校验——把引用错误报给用户）
	rows, err := tx.Query(`SELECT id FROM tools WHERE server = ?`, id)
	if err != nil {
		return fmt.Errorf("查服务器工具失败: %w", err)
	}
	var toolIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			rows.Close()
			return fmt.Errorf("读工具 id 失败: %w", err)
		}
		toolIDs = append(toolIDs, tid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// 逐个走 RemoveTool 的引用校验（事务外——校验只读，事务里删）
	for _, tid := range toolIDs {
		if err := s.checkToolRefs(tid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM tools WHERE server = ?`, id); err != nil {
		return fmt.Errorf("级联删工具失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM mcp_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 MCP 服务器失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("MCP 服务器 %s 不存在", id)
	}
	return tx.Commit()
}

func (s *Store) checkToolRefs(id string) error {
	rows, err := s.db.Query(`SELECT id, tools FROM agents WHERE tools LIKE ?`, "%"+`"`+id+`"`+"%")
	if err != nil {
		return fmt.Errorf("查引用失败: %w", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var aid, tools string
		if err := rows.Scan(&aid, &tools); err != nil {
			return fmt.Errorf("读引用行失败: %w", err)
		}
		for _, x := range decodeStrList(tools) {
			if x == id {
				refs = append(refs, aid)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(refs) > 0 {
		return fmt.Errorf("工具 %s 被以下 Agent 的白名单引用，先解除引用: %s", id, strings.Join(refs, "、"))
	}
	return nil
}
