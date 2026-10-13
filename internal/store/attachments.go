// 图片附件的落盘与路径解析。
//
// 位置约定（2026-10 用户拍板）：`<sessions>/attachments/<session-id>/<随机id>.<ext>`
// ——与 worktree 无关（worktree 释放/删除不影响附件；sessions 根 = store 的 dir，
// 同 WorktreeRoot() 的先例）。消息恒存**相对 attachments 根的路径**引用，
// base64 只在构造 LLM 请求的瞬间存在（分层纪律见 internal/llm/images.go）。

package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
)

// AttachmentsRoot 返回附件根目录（<sessions>/attachments）。
func (s *Store) AttachmentsRoot() string { return filepath.Join(s.dir, "attachments") }

// mimeExt 按 mime 推扩展名（白名单内的四种——mime 校验在 SaveAttachment 里做）。
var mimeExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// ExtForMime 返回 mime 对应的扩展名（未知 mime 返回空串）。导出给 HTTP 端点
// 反向使用：按扩展名回 Content-Type。
func ExtForMime(m string) string { return mimeExt[m] }

// ClassifyAttachment 按文件名的扩展名给附件分类（share_file 共享的文件用）：
// 扩展名命中图片白名单 → kind=image + mime（前端渲染内联缩略图）；否则
// kind=file + 空 mime（文件卡 + 下载）。共享通道不做内容嗅探——扩展名是
// 共享者（agent）对它刚生成的文件最可靠的判断，嗅探出错反而把 png 当成
// 下载卡。没有扩展名也是 file。
func ClassifyAttachment(name string) (kind, mime string) {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return llm.AttachmentKindFile, ""
	}
	if m, ok := extToMime[strings.ToLower(name[dot+1:])]; ok {
		return llm.AttachmentKindImage, m
	}
	return llm.AttachmentKindFile, ""
}

// extToMime 是 mimeExt 的反向映射（扩展名 → mime），包级初始化一次。
var extToMime = func() map[string]string {
	m := make(map[string]string, len(mimeExt))
	for mime, ext := range mimeExt {
		m[strings.TrimPrefix(ext, ".")] = mime
	}
	return m
}()

// validSessionID 报告 id 是否只含白名单字符（小写字母/数字/连字符）。
// session id 是服务端生成的（时间戳 + 随机 hex），但它是**外部输入**能到达的
// 路径成分（chat.send 参数 / HTTP URL），白名单校验是路径穿越的第一道闸。
func validSessionID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// ValidSessionID 导出版（server 的 HTTP 端点与 chat.send 校验共用同一份判定——
// 各写一遍必然漂移）。
func ValidSessionID(id string) bool { return validSessionID(id) }

// ResolveAttachmentPath 把附件相对路径解析成绝对路径，并做**穿越防护**：
// 净化后的路径必须仍在 attachments 根之内（Clean 掉 .. 之后再比对前缀）。
// 不通过即报错——绝不返回根外路径。
func ResolveAttachmentPath(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("附件路径为空")
	}
	clean := filepath.Clean(rel)
	// 含 ".." 路径元素的原始输入一律拒（按元素判断——我们的引用恒由
	// randomAttachmentName 生成，任何形式的 .. 都不是我们写的名字）。
	for _, seg := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("附件路径不合法: %q", rel)
		}
	}
	// 前导分隔符（/abs、\abs）也拒：Windows 上它不是 IsAbs，但那是"我们没
	// 生成过的名字"——引用恒为相对路径，形态不对就不是我们的附件。
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "\\") ||
		strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("附件路径不合法: %q", rel)
	}
	abs := filepath.Join(root, clean)
	// 双保险：Join 之后前缀必须仍是根（防 Clean 语义外的边界情形）
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("附件路径越出附件根目录: %q", rel)
	}
	return abs, nil
}

// SaveAttachment 把一张图片落盘到 `<root>/<sessionID>/<随机id>.<ext>`，
// 返回**相对 attachments 根**的路径（消息里存的就是它）。
//
// 防御性校验（调用方——server 的 chat.send——已按同一套规则校验过一遍，这里
// 再挡一次是因为 store 是写入的最终边界，不能假设上游永远记得校验）：
//   - sessionID 白名单（[a-z0-9-]）；
//   - mime 白名单（png/jpeg/webp/gif）；
//   - 单张 ≤5MB（base64 解码后的字节数——Anthropic 对 base64 源图片的上限）。
func (s *Store) SaveAttachment(sessionID, mime string, data []byte) (string, error) {
	if !validSessionID(sessionID) {
		return "", fmt.Errorf("会话 id 不合法（只允许小写字母/数字/连字符）: %q", sessionID)
	}
	if !llm.ValidImageMime(mime) {
		return "", fmt.Errorf("不支持的图片类型: %q（支持 png/jpeg/webp/gif）", mime)
	}
	if len(data) > llm.MaxImageBytes {
		return "", fmt.Errorf("图片超过大小上限（%d MB）", llm.MaxImageBytes/(1<<20))
	}
	ext := mimeExt[mime]
	if ext == "" {
		return "", fmt.Errorf("图片类型缺少扩展名映射: %q", mime)
	}
	dir := filepath.Join(s.AttachmentsRoot(), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建附件目录失败: %w", err)
	}
	name, err := randomAttachmentName(ext)
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", fmt.Errorf("写附件失败: %w", err)
	}
	// 相对路径用斜杠（跨平台稳定，HTTP 端点与前端都按它寻址）
	return filepath.ToSlash(filepath.Join(sessionID, name)), nil
}

// SaveSharedAttachment 实现 agent.Persistence 的共享通道（share_file）：把
// agent 工作目录里的一个文件复制登记进会话附件目录，返回展示元数据。
//
// 与 SaveFileAttachment（用户上传通道）的分工：那条通道的名字来自客户端
//（不可信，只用于展示与扩展名推导）；这条通道的名字来自 agent 读到的磁盘
// base 名（同样净化后只做展示与扩展名——磁盘名恒为随机 hex，两条通道一致）。
// 分类按扩展名（ClassifyAttachment）：图片渲染缩略图卡，其余渲染文件卡。
func (s *Store) SaveSharedAttachment(sessionID, name string, data []byte) (llm.AttachmentRef, error) {
	clean := SanitizeFileName(name)
	rel, err := s.SaveFileAttachment(sessionID, clean, data)
	if err != nil {
		return llm.AttachmentRef{}, err
	}
	kind, mime := ClassifyAttachment(clean)
	return llm.AttachmentRef{
		Path: rel, Name: clean, Size: int64(len(data)),
		Mime: mime, Kind: kind,
	}, nil
}

// randomAttachmentName 生成随机文件名（16 字节 hex + 扩展名）：内容不可预测、
// 无碰撞窗口；不使用原文件名（用户上传的文件名不可信，不做路径成分）。
func randomAttachmentName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成附件文件名失败: %w", err)
	}
	return hex.EncodeToString(b) + ext, nil
}

// MaxFileAttachmentBytes 单个文件附件的大小上限（base64 解码后）：20MB。
// 与图片的 5MB（Anthropic 对 base64 源图的上限）分开——文件只落盘给
// read_file 读，不受端点限制，给到「日常文档」够用的量级。
// 实值归 llm 层（tools/server/store 三层共用的同一份常量，见 llm 的注释）。
const MaxFileAttachmentBytes = llm.MaxFileAttachmentBytes

// SanitizeFileName 净化客户端提供的文件名（文件附件通道，图片批次 B）：
//   - 只取 base 名（按 / 与 \ 切——两种分隔符都挡，Windows 客户端传全路径也不漏）；
//   - 去控制字符（含换行——名字要进消息文本，换行会伪造出第二条「[附件]」行）；
//   - 去首尾空白；空/全被净化掉 → "file"（不拒绝——名字只是展示与识别用）；
//   - 长度钳到 120 字符（消息文本里一行放得下，也防极端长名撑爆 UI）。
//
// 净化后的名字**不做磁盘文件名**（磁盘名恒为随机 hex，见 randomAttachmentName），
// 它只出现在消息文本与扩展名推导里。
func SanitizeFileName(name string) string {
	// base 名：按两种分隔符取最后一段
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue // 控制字符（含 \n \r \t）一律去掉
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	runes := []rune(clean)
	if len(runes) > 120 {
		clean = string(runes[:120])
	}
	if clean == "" {
		clean = "file"
	}
	return clean
}

// attachmentExt 从净化后的文件名推扩展名（保存时拼在随机名后面，便于 Agent 与
// 人识别类型）：只保留最后一个 '.' 段里的字母/数字，≤16 字符；其余杂质（冒号、
// 空格、多级点）一律丢弃——扩展名要拼进磁盘文件名，Windows 文件名里合法字符
// 才行。推不出干净的扩展名就返回空串（无扩展名保存，不猜）。
func attachmentExt(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 || dot == len(name)-1 {
		return ""
	}
	var b strings.Builder
	for _, r := range name[dot+1:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			if b.Len() >= 16 {
				return "." + b.String()
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "." + b.String()
}

// SaveFileAttachment 把一个文件附件落盘到 `<root>/<sessionID>/<随机id>.<ext>`，
// 返回**相对 attachments 根**的路径（消息文本的「[附件]」行存的就是它）。
//
// 与 SaveAttachment（图片）的分工：文件**不做 mime 白名单**（Agent 要能收任意
// 文档/代码/数据文件），校验只剩会话 id 白名单与大小上限；扩展名从净化后的
// 原名推导（脏字符丢弃），磁盘名仍是随机 hex——原名永不成为路径成分。
func (s *Store) SaveFileAttachment(sessionID, name string, data []byte) (string, error) {
	if !validSessionID(sessionID) {
		return "", fmt.Errorf("会话 id 不合法（只允许小写字母/数字/连字符）: %q", sessionID)
	}
	if len(data) > MaxFileAttachmentBytes {
		return "", fmt.Errorf("文件超过大小上限（%d MB）", MaxFileAttachmentBytes/(1<<20))
	}
	dir := filepath.Join(s.AttachmentsRoot(), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建附件目录失败: %w", err)
	}
	fname, err := randomAttachmentName(attachmentExt(SanitizeFileName(name)))
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, fname)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", fmt.Errorf("写附件失败: %w", err)
	}
	return filepath.ToSlash(filepath.Join(sessionID, fname)), nil
}
