// 斜杠命令处理（/new /sessions /resume /compact /model /think /help）。

package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func (r *REPL) runCommand(line string) bool {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))

	switch cmd {
	case "quit", "exit", "q":
		return true
	case "new":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result protocol.SessionResult
		if err := r.be.Call(ctx, protocol.MethodSessionNew, protocol.SessionNewParams{}, &result); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		r.setCurrentSession(result.SessionID)
		if err := r.syncHistory(); err != nil {
			fmt.Printf("[同步会话失败] %v\n", err)
		}
		fmt.Println("[新会话已开始]")
	case "sessions":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var list []protocol.SessionMeta
		if err := r.be.Call(ctx, protocol.MethodSessionList, nil, &list); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		if len(list) == 0 {
			fmt.Println("[没有历史会话]")
			return false
		}
		r.mu.Lock()
		cur := r.session
		r.mu.Unlock()
		for i, m := range list {
			current := ""
			if m.ID == cur {
				current = " ←当前"
			}
			fmt.Printf("  %d. %s  %s  %d条  %s%s\n", i+1, m.ID, m.UpdatedAt, m.Messages, m.Title, current)
		}
		fmt.Println("[/resume <序号或id> 恢复]")
	case "resume":
		if rest == "" {
			fmt.Println("[用法: /resume <序号或会话id>；先用 /sessions 查看]")
			return false
		}
		id := rest
		// 序号 → id（重拉列表做映射）
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var list []protocol.SessionMeta
		if err := r.be.Call(ctx, protocol.MethodSessionList, nil, &list); err == nil {
			if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= len(list) {
				id = list[n-1].ID
			}
		}
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.be.Call(ctx, protocol.MethodSessionResume, protocol.SessionResumeParams{ID: id}, nil); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		r.setCurrentSession(id)
		if err := r.syncHistory(); err != nil {
			fmt.Printf("[同步会话失败] %v\n", err)
		}
	case "compact":
		// 手动压缩：不受阈值约束（空闲即可）——长会话撞窗口前的主动手段
		if err := r.requireIdle(); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var res protocol.CompactResult
		if err := r.be.Call(ctx, protocol.MethodChatCompact, protocol.CompactParams{SessionID: r.currentSessionID()}, &res); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		if !res.Compacted {
			fmt.Printf("[%s]\n", orDash(res.Reason))
			return false
		}
		fmt.Printf("[已压缩 %d 条历史：%d → %d tokens]\n", res.Shadowed, res.Before, res.After)
	case "model":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ml protocol.ModelListResult
		if err := r.be.Call(ctx, protocol.MethodModelList, nil, &ml); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		fmt.Printf("default → %s\n", orDash(ml.Roles["default"]))
		fmt.Printf("vision  → %s\n", orDash(ml.Roles["vision"]))
		for _, m := range ml.Models {
			mark := " "
			if ml.Roles["default"] == m.ID {
				mark = "*"
			}
			fmt.Printf(" %s %s  %s  %s  %s\n", mark, m.ID, m.Model, m.EffectiveFormat(), enabledText(m.Enabled))
		}
	case "think":
		r.showThink = !r.showThink
		fmt.Printf("[思考链显示: %v]\n", r.showThink)
	case "help", "":
		fmt.Print(`命令：
  /new            开新会话（旧的保留可 resume）
  /sessions       列出历史会话
  /resume <n|id>  恢复历史会话
  /compact        压缩早期历史（长会话撞窗口前主动压一次）
  /model          查看模型与角色绑定
  /think          切换思考链显示（默认隐藏）
  /quit           退出
生成中 Ctrl+C 取消当前轮；空闲 Ctrl+C 退出。
`)
	default:
		fmt.Printf("[未知命令 /%s；/help 查看可用命令]\n", cmd)
	}
	return false
}
