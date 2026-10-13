// 图片附件（视觉请求）的 server 侧：chat.send 的接收与落盘、LLM 层的图片
// 读取注入、HTTP 只读端点。
//
// 分层：base64 只出现在两个瞬间——①chat.send 参数里（客户端 → 服务端，落盘
// 后即弃）；②LLM 请求构造瞬间（llm 包内部经 loader 读文件产生）。历史、内存、
// 日志、事件广播恒存文件引用。

package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// readLocalAttachment 读一个客户端声明的本地路径（chat.send 的 path 形态——
// 前后端同机的 Electron 客户端，2026-12 用户拍板：路径优先、base64 兜底）。
//
// **信任边界**：本机单用户模型——后端与客户端同机同用户，path 形态与
// share_file/read_file 工具的信任级别一致（客户端本就能经工具读同机的文件），
// 因此**不做目录白名单**；但大小上限与存在性校验照做（与 base64 形态同一套
// 上限），且错误一律带路径——前端以「本地文件读取失败」这个特征串识别
// 「后端读不到 path」，自动降级为 base64 重发一次。
func readLocalAttachment(path string, max int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("本地文件读取失败：%s（文件不存在或无法访问）", path)
	}
	if st.Size() > max {
		return nil, fmt.Errorf("本地文件 %s 超过大小上限（%d MB，当前 %d MB）", path, max/(1<<20), st.Size()/(1<<20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("本地文件读取失败：%s（%v）", path, err)
	}
	return data, nil
}

// validateChatImages 校验 chat.send 携带的图片（**纯校验，不落盘**）：
// 一条消息 ≤4 张、mime 白名单、base64 解码后（或 path 形态读盘后）单张 ≤5MB。
// 返回字节（调用方紧接着落盘）——base64 形态避免二次解码，path 形态复用读出的
// 字节。任何不合法都返回人话错误（JSON-RPC CodeInvalidParams 的文案）。
func validateChatImages(imgs []protocol.ChatSendImage) ([][]byte, error) {
	if len(imgs) == 0 {
		return nil, nil
	}
	if len(imgs) > llm.MaxImagesPerMessage {
		return nil, fmt.Errorf("一条消息最多带 %d 张图片（收到 %d 张）", llm.MaxImagesPerMessage, len(imgs))
	}
	out := make([][]byte, 0, len(imgs))
	for i, im := range imgs {
		mime := im.Mime
		if im.Path != "" {
			if im.Data != "" {
				return nil, fmt.Errorf("第 %d 张图片不能同时携带 path 与 data（path 形态由服务端按路径读盘）", i+1)
			}
			// path 形态的 mime 可空：按扩展名推导（与附件端点 extMime 同一份映射）。
			// 推导结果**写回元素**——落盘（saveChatImages → SaveAttachment 的 mime
			// 白名单）与展示元数据（session_ops 的 AttachmentRef.Mime）读同一份。
			if mime == "" {
				mime = extMime[strings.ToLower(filepathExt(im.Path))]
				imgs[i].Mime = mime
			}
		}
		if !llm.ValidImageMime(mime) {
			return nil, fmt.Errorf("第 %d 张图片类型不支持: %q（支持 png/jpeg/webp/gif）", i+1, im.Mime)
		}
		var data []byte
		var err error
		if im.Path != "" {
			if data, err = readLocalAttachment(im.Path, llm.MaxImageBytes); err != nil {
				return nil, fmt.Errorf("第 %d 张图片: %w", i+1, err)
			}
		} else {
			data, err = base64.StdEncoding.DecodeString(im.Data)
			if err != nil {
				return nil, fmt.Errorf("第 %d 张图片的 base64 解码失败（Data 不带 data: 前缀）: %v", i+1, err)
			}
			if len(data) > llm.MaxImageBytes {
				return nil, fmt.Errorf("第 %d 张图片超过大小上限（%d MB，base64 解码后）", i+1, llm.MaxImageBytes/(1<<20))
			}
		}
		out = append(out, data)
	}
	return out, nil
}

// saveChatImages 把校验过的图片逐张落盘，返回给内核的消息引用列表。
// 每张独立落盘：部分成功时已落盘的不回滚（附件是幂等无害的孤儿文件，
// 报错让客户端重发即可）。
func (s *Server) saveChatImages(sessionID string, imgs []protocol.ChatSendImage, decoded [][]byte) ([]llm.ImageRef, error) {
	var refs []llm.ImageRef
	for i, im := range imgs {
		rel, err := s.st.SaveAttachment(sessionID, im.Mime, decoded[i])
		if err != nil {
			return nil, fmt.Errorf("第 %d 张图片保存失败: %w", i+1, err)
		}
		refs = append(refs, llm.ImageRef{Path: rel, Mime: im.Mime})
	}
	return refs, nil
}

// MaxFilesPerMessage 一条消息最多带的文件附件个数（与图片的 4 张同一量级——
// 消息文本里的「[附件]」行与落盘都按这个数钳）。
const MaxFilesPerMessage = 4

// validateChatFiles 校验 chat.send 携带的文件附件（**纯校验，不落盘**）：
// ≤4 个、base64 可解码或 path 形态可读盘、单个 ≤20MB（store.MaxFileAttachmentBytes）、
// 名字净化后非空（path 形态没带名字 → 回落路径 basename）。返回字节与净化后的
// 名字（调用方紧接着落盘）。任何不合法都返回人话错误（JSON-RPC CodeInvalidParams 的文案）。
func validateChatFiles(files []protocol.ChatSendFile) ([][]byte, []string, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	if len(files) > MaxFilesPerMessage {
		return nil, nil, fmt.Errorf("一条消息最多带 %d 个文件（收到 %d 个）", MaxFilesPerMessage, len(files))
	}
	out := make([][]byte, 0, len(files))
	names := make([]string, 0, len(files))
	for i, f := range files {
		var data []byte
		var err error
		if f.Path != "" {
			if f.Data != "" {
				return nil, nil, fmt.Errorf("第 %d 个文件不能同时携带 path 与 data（path 形态由服务端按路径读盘）", i+1)
			}
			if data, err = readLocalAttachment(f.Path, store.MaxFileAttachmentBytes); err != nil {
				return nil, nil, fmt.Errorf("第 %d 个文件: %w", i+1, err)
			}
		} else {
			data, err = base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				return nil, nil, fmt.Errorf("第 %d 个文件的 base64 解码失败（Data 不带 data: 前缀）: %v", i+1, err)
			}
			if len(data) > store.MaxFileAttachmentBytes {
				return nil, nil, fmt.Errorf("第 %d 个文件超过大小上限（%d MB，base64 解码后）", i+1, store.MaxFileAttachmentBytes/(1<<20))
			}
		}
		name := f.Name
		if name == "" && f.Path != "" {
			name = filepath.Base(f.Path)
		}
		name = store.SanitizeFileName(name)
		if name == "" {
			return nil, nil, fmt.Errorf("第 %d 个文件缺少文件名", i+1)
		}
		out = append(out, data)
		names = append(names, name)
	}
	return out, names, nil
}

// saveChatFiles 把校验过的文件逐个落盘（SaveFileAttachment——无 mime 白名单的
// 文件通道），返回相对附件根的路径列表。部分成功不回滚（与图片同款：附件是
// 幂等无害的孤儿文件，报错让客户端重发即可）。
func (s *Server) saveChatFiles(sessionID string, decoded [][]byte, names []string) ([]string, error) {
	var rels []string
	for i, name := range names {
		rel, err := s.st.SaveFileAttachment(sessionID, name, decoded[i])
		if err != nil {
			return nil, fmt.Errorf("第 %d 个文件保存失败: %w", i+1, err)
		}
		rels = append(rels, rel)
	}
	return rels, nil
}

// bindAttachmentLoader 把图片读取函数注入 llm 层（进程级一次）：给一个文件
// 引用，从附件根目录读出原始字节。路径穿越防护在 store.ResolveAttachmentPath
// （llm 层不知道磁盘布局，也不该知道）。loader 失败走 llm 层的 fail-open
// （跳过该图 + 文本提示），不中断请求。
func (s *Server) bindAttachmentLoader(st *store.Store) {
	root := st.AttachmentsRoot()
	llm.SetImageLoader(func(ref llm.ImageRef) (string, []byte, error) {
		p, err := store.ResolveAttachmentPath(root, ref.Path)
		if err != nil {
			return "", nil, err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", nil, fmt.Errorf("读取附件失败: %w", err)
		}
		return ref.Mime, data, nil
	})
}

// extMime 是扩展名 → Content-Type 的反向映射（与 store.mimeExt 同一份白名单，
// 派生自它，保证「存的时候推扩展名」与「读的时候回 mime」不漂移）。
var extMime = func() map[string]string {
	m := map[string]string{}
	for mime, ext := range map[string]string{
		"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif",
	} {
		m[ext] = mime
	}
	return m
}()

// attachmentsGate 给附件端点套远程访问的 token 门（与 /rpc 同一份
// checkRemoteToken——query token 或 Authorization: Bearer，同源逻辑）。
// 监听按 remote.json 切到 0.0.0.0 后（listener.go），附件 URL 与 /rpc 一样
// 是局域网可达面：门必须同宽，否则开了远程附件对外裸奔。
func (s *Server) attachmentsGate(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.checkRemoteToken(r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

// handleAttachments 是 GET /attachments/<sessionID>/<file>：只读地回一个附件
// 的原始字节（前端渲染附件卡用：图片内联缩略图，其余文件走下载）。
//
// 安全：sessionID 白名单、文件名白名单（十六进制 + 扩展名形态校验）、解析后
// 路径必须仍在附件根内（store.ResolveAttachmentPath 双保险）——穿越一律 404，
// 不泄露"存在但越权"与"不存在"的区别（都是 404）。
//
// Content-Type 分流（与附件卡两种形态一致）：
//   - 图片扩展名（store 的白名单）→ 对应 image/* mime，inline（浏览器直接显示）；
//   - 其余白名单内扩展名（saveFileAttachment 通道：字母/数字 ≤16 字符）→
//     application/octet-stream + Content-Disposition: attachment（点击即下载）。
//     不做内容嗅探：磁盘名是我们生成的随机 hex，扩展名就是写入时的分类。
func (s *Server) handleAttachments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}
	if s.st == nil {
		http.Error(w, "附件存储未启用", http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/attachments/")
	sessionID, file, ok := strings.Cut(rest, "/")
	if !ok || sessionID == "" || file == "" {
		http.Error(w, "路径形态应为 /attachments/<sessionID>/<file>", http.StatusNotFound)
		return
	}
	if !store.ValidSessionID(sessionID) {
		http.Error(w, "会话 id 不合法", http.StatusNotFound)
		return
	}
	// 文件名白名单：<16 字节 hex>.<扩展名>——含 / \ .. 空白等一切杂质的名字
	// 直接 404（我们生成的名字恒满足，满足不了就不是我们写的）。扩展名上限
	// 16 字符字母/数字（store.attachmentExt 的写入口径），超了/带杂质就 404。
	ext := strings.ToLower(filepathExt(file))
	if ext == "" || len(ext) > 17 || !isAlnum(ext[1:]) {
		http.Error(w, "附件不存在", http.StatusNotFound)
		return
	}
	if len(file) != 32+len(ext) || !isHex(file[:32]) {
		http.Error(w, "附件不存在", http.StatusNotFound)
		return
	}
	p, err := store.ResolveAttachmentPath(s.st.AttachmentsRoot(), sessionID+"/"+file)
	if err != nil {
		http.Error(w, "附件不存在", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, "附件不存在", http.StatusNotFound)
		return
	}
	// 内容不可变（文件名含随机 id）：缓存头让前端缩略图零重复传输
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if mime, known := extMime[ext]; known {
		w.Header().Set("Content-Type", mime)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
	}
	_, _ = w.Write(data)
}

// isAlnum 报告 s 是否只含 ASCII 字母/数字（扩展名白名单）。
func isAlnum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

// filepathExt 是 path.Ext 的本地小包装（避免仅为一个函数 import path）。
func filepathExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			return ""
		}
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

// isHex 报告 s 是否只含十六进制字符。
func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}
