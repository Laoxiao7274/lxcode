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
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// validateChatImages 校验 chat.send 携带的图片（**纯校验，不落盘**）：
// 一条消息 ≤4 张、mime 白名单、base64 解码后单张 ≤5MB。
// 返回解码后的字节（调用方紧接着落盘）——避免二次解码。
// 任何不合法都返回人话错误（JSON-RPC CodeInvalidParams 的文案）。
func validateChatImages(imgs []protocol.ChatSendImage) ([][]byte, error) {
	if len(imgs) == 0 {
		return nil, nil
	}
	if len(imgs) > llm.MaxImagesPerMessage {
		return nil, fmt.Errorf("一条消息最多带 %d 张图片（收到 %d 张）", llm.MaxImagesPerMessage, len(imgs))
	}
	out := make([][]byte, 0, len(imgs))
	for i, im := range imgs {
		if !llm.ValidImageMime(im.Mime) {
			return nil, fmt.Errorf("第 %d 张图片类型不支持: %q（支持 png/jpeg/webp/gif）", i+1, im.Mime)
		}
		data, err := base64.StdEncoding.DecodeString(im.Data)
		if err != nil {
			return nil, fmt.Errorf("第 %d 张图片的 base64 解码失败（Data 不带 data: 前缀）: %v", i+1, err)
		}
		if len(data) > llm.MaxImageBytes {
			return nil, fmt.Errorf("第 %d 张图片超过大小上限（%d MB，base64 解码后）", i+1, llm.MaxImageBytes/(1<<20))
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
// ≤4 个、base64 可解码、单个 ≤20MB（store.MaxFileAttachmentBytes）、
// 名字净化后非空。返回解码后的字节与净化后的名字（调用方紧接着落盘）。
// 任何不合法都返回人话错误（JSON-RPC CodeInvalidParams 的文案）。
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
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 个文件的 base64 解码失败（Data 不带 data: 前缀）: %v", i+1, err)
		}
		if len(data) > store.MaxFileAttachmentBytes {
			return nil, nil, fmt.Errorf("第 %d 个文件超过大小上限（%d MB，base64 解码后）", i+1, store.MaxFileAttachmentBytes/(1<<20))
		}
		name := store.SanitizeFileName(f.Name)
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

// handleAttachments 是 GET /attachments/<sessionID>/<file>：只读地回一张附件
// 原始字节（前端显示缩略图用，图片批次 B 接 UI；本批先实现并测试）。
//
// 安全：sessionID 白名单、文件名白名单（十六进制 + 已知扩展名）、解析后路径
// 必须仍在附件根内（store.ResolveAttachmentPath 双保险）——穿越一律 404，
// 不泄露"存在但越权"与"不存在"的区别（都是 404）。
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
	// 文件名白名单：<16 字节 hex>.<白名单扩展名>——含 / \ .. 空白等一切杂质的
	// 名字直接 404（我们生成的名字恒满足，满足不了就不是我们写的）。
	ext := strings.ToLower(filepathExt(file))
	mime, known := extMime[ext]
	if !known || len(file) != 32+len(ext) || !isHex(file[:32]) {
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
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(data)
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
