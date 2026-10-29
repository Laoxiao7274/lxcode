package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

// readObservationHint 是 edit 失败时附加的「你改之前读过这个文件吗」提示。
//
// 为什么只做提示、不硬拦（2026-12 用户拍板，保守取向）：DSH 的
// fs-observation-policy 用 FS_NOT_OBSERVED 硬拒绝「没读过就 edit」，它防的是
// 「凭记忆改 → 失败 → 去读 → 再改」那一串多余 step。但 lxcode 的 edit 本来
// 就要求 old_string 逐字符唯一匹配——凭记忆写出来的 old_string 几乎必然不匹配，
// 于是「先读再改」已经是工具语义自带的约束，硬拦只是把同一条约束提前一道，
// 代价是打断合法流程：刚 write_file 写完的文件（内容就在上下文里，根本没读过）、
// 以及本会话之前读过但进程重启过的会话。硬拦在那些场景里会逼模型做一次纯
// 仪式性的 read_file。
//
// 所以这里做的是「在失败信息里补上关键事实」：本会话读到过没有、读的是不是
// 同一版本、读到第几步。模型据此判断该去读，还是该换 old_string。
func readObservationHint(ctx context.Context, path string, st os.FileInfo) string {
	state := readStateFrom(ctx)
	obs, seen := state.Peek(path)
	if !seen {
		return "；本会话还没有读过这个文件——先用 read_file 看一遍原文"
	}
	if st != nil && (obs.Size != st.Size() || obs.ModUnixNano != st.ModTime().UnixNano()) {
		return fmt.Sprintf("；本会话在第 %d 步读过它，但文件此后被改过——重新 read_file 拿最新内容",
			obs.Step)
	}
	return fmt.Sprintf("；本会话在第 %d 步读过它（offset=%d limit=%d），内容此后未变——"+
		"old_string 请直接从那次 read_file 的输出里复制", obs.Step, obs.Offset, obs.Limit)
}

// editDef：edit，风险等级 低危——编程 agent 的主编辑通道，每次改动都是
// 有界 diff（old_string 精确匹配），不像 write_file 是全量覆盖，破坏面天然
// 受控；给 edit 挂确认门会让"改一处代码"都要按一次 y/n，agent 基本不可用
// （主流 coding agent 的 edit 都是自动执行的，写面安全靠原子写与版本控制兜底）。
//
// 匹配语义（故意收紧）：
//   - old_string 在文件中必须恰好出现一次；0 次 = 报错并提示先 read_file，
//     >1 次 = 报错并给出次数，要求带上更多上下文让 old_string 唯一，或显式
//     replace_all。放水成"替换第一处"会静默改错位置——那是数据损坏不是编辑。
//   - new_string 与 old_string 相同时报错：多半是模型手滑，真要 no-op 没有意义。
func editDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "要修改的文件路径（绝对或相对工作目录；文件必须已存在，新建用 write_file）"},
			"old_string": {"type": "string", "description": "要替换的原文——必须与文件内容逐字符一致（包括缩进与空行），且在文件中唯一"},
			"new_string": {"type": "string", "description": "替换后的新文本（完整给出，包括要保留的部分）"},
			"replace_all": {"type": "boolean", "description": "old_string 出现多处时是否全部替换（默认 false：多处匹配会报错而不是猜）"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
	return &Def{
		Name: "edit",
		Description: "精确修改文件：把 old_string 匹配的片段替换为 new_string（原子写）。" +
			"old_string 必须与文件内容完全一致且唯一（不一致就先 read_file 对照；多处匹配会报错，" +
			"要么补充上下文使其唯一，要么显式 replace_all=true）。修改文件优先用本工具，别用 write_file 重写全文。",
		Parameters: schema,
		Risk:       RiskLow,
		Mutates:    true, // 低危（唯一匹配+原子写）但变更文件——strict 只读模式按此拒绝
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Path       string `json:"path"`
				OldString  string `json:"old_string"`
				NewString  string `json:"new_string"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if a.Path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			if a.OldString == "" {
				return "", fmt.Errorf("old_string 不能为空（删除整段也要给出要删的原文）")
			}
			if a.OldString == a.NewString {
				return "", fmt.Errorf("new_string 与 old_string 相同——这次编辑不会产生任何改动")
			}

			// 相对路径按会话工作目录解析（项目会话 = 项目根）
			path := resolveToolPath(ctx, a.Path)
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("读取 %s 失败: %w", a.Path, err)
			}
			st, err := os.Stat(path)
			if err == nil && st.IsDir() {
				return "", fmt.Errorf("%s 是目录，edit 只能改文件", a.Path)
			}

			n := strings.Count(string(data), a.OldString)
			switch {
			case n == 0:
				return "", fmt.Errorf("old_string 在 %s 中没有匹配。请先 read_file 核对原文"+
					"（注意缩进、空行、全半角必须逐字符一致）后再试%s", a.Path, readObservationHint(ctx, path, st))
			case n > 1 && !a.ReplaceAll:
				return "", fmt.Errorf("old_string 在 %s 中匹配了 %d 处。要么扩大 old_string 的范围"+
					"（带上前后行使其唯一），要么确认全部都要改并用 replace_all=true%s", a.Path, n, readObservationHint(ctx, path, st))
			}

			var out string
			if a.ReplaceAll {
				out = strings.ReplaceAll(string(data), a.OldString, a.NewString)
			} else {
				out = strings.Replace(string(data), a.OldString, a.NewString, 1)
			}
			// 文件快照（docs/session-tree.md §C.2）：**在 atomicfile.Write 之前**抓
			// before（这里 data 已经是全文，抓取器自己再读一次也就 1MB 以内，可忽略）。
			// 句柄窄到只有 Commit/Abort——工具层不知道快照落在哪张表、哪个目录。
			// 未注入（纯内存模式/单测）= 不抓，静默降级（那一轮的改动回滚不了）。
			snap := beginSnapshot(ctx, path)
			if err := atomicfile.Write(path, []byte(out)); err != nil {
				snap.Abort()
				return "", fmt.Errorf("写入 %s 失败: %w", a.Path, err)
			}
			// Commit 失败**不让工具失败**：快照是旁路记录，写文件本身已经成功。
			// 反过来（为了快照失败而回滚用户的写）是本末倒置。
			snap.Commit(HashBytes([]byte(out)), int64(len(out)))
			// 没抓到（大文件/二进制/落盘失败）时如实告知：这个改动回滚不到。
			// 这句提示也是 agent 侧计 external 的识别依据（同一句常量）。
			note := skipNote(snap)
			if n > 1 && a.ReplaceAll {
				return fmt.Sprintf("已修改 %s（%d 处替换，%d 字节 → %d 字节）%s",
					a.Path, n, len(data), len(out), note), nil
			}
			return fmt.Sprintf("已修改 %s（1 处替换，%d 字节 → %d 字节）%s",
				a.Path, len(data), len(out), note), nil
		},
	}
}
