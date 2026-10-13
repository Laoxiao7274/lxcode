// read_image：读一张图片并把内容注入**当前会话下一轮的模型请求**（低危只读）。
//
// 与 read_file 的分工：read_file 拒绝二进制；read_image 专收图片——读到的图
// 作为视觉输入随本条工具结果进入模型请求（llm.ImageRef 通道，与用户发的图
// 同一条编码路径），视觉模型能直接描述/分析它的内容。要**给用户看**这张图，
// 用 share_file（模型已经看过了，用户看图是展示层的事）。
//
// 能力门控：当前模型未声明视觉能力时如实报错（人话），不假装读图——把图硬塞
// 给不识图的端点轻则浪费上下文、重则被严格端点 400 拒收整轮。
//
// 安全边界：与 share_file 同一套——路径相对会话工作目录解析，净化后必须仍在
// 工作目录之内（穿越一律拒绝）；大小上限走 llm.MaxImageBytes（5MB，Anthropic
// 对 base64 源图的上限，用户图片同一条边界）。
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // 注册 DecodeConfig（gif 取首帧信息）
	_ "image/jpeg" // 注册 DecodeConfig
	_ "image/png"  // 注册 DecodeConfig
	"os"
	"path/filepath"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
)

// readImageParams 是 read_image 的参数形状。
type readImageParams struct {
	Path string `json:"path"`
}

// imageExts 是支持的图片扩展名 → mime（与 llm 的视觉白名单同一份清单的路径形态）。
var imageExts = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

func readImageDef() *Def {
	return &Def{
		Name: "read_image",
		Description: "读取一张图片的内容给视觉模型看（低危只读：不修改任何文件）。" +
			"支持的格式：png/jpeg/webp/gif（gif 取首帧）。读到的图片作为视觉输入注入下一轮请求，" +
			"你可以直接描述、分析图里的内容。注意：当前模型必须声明「视觉」能力，否则工具会报错；" +
			"要把图片展示给用户看，用 share_file 而不是本工具。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "图片路径（相对会话工作目录解析；必须在工作目录之内，目录穿越会被拒绝）"}
  },
  "required": ["path"]
}`),
		Risk:    RiskLow,
		Mutates: false,
		Exec:    execReadImage,
	}
}

func execReadImage(ctx context.Context, args json.RawMessage) (string, error) {
	var p readImageParams
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	p.Path = strings.TrimSpace(p.Path)
	if p.Path == "" {
		return "", fmt.Errorf("缺少 path 参数（要读取的图片路径）")
	}
	// 能力门控先行：模型不支持视觉就不读（不浪费一次落盘，错误也更快可见）。
	if !VisionCapable(ctx) {
		return "", fmt.Errorf("当前模型不支持视觉，read_image 无法注入图片内容。" +
			"请改用其他手段：用 share_file 把图共享给用户人工查看，或读图片旁的文本说明，" +
			"或让用户在 设置 → 模型 里为当前模型勾选「视觉」能力后重试")
	}
	// 工作目录基准与穿越防护：与 share_file 同一套判定（resolveSharePath）。
	base := WorkDir(ctx)
	if base == "" {
		return "", fmt.Errorf("会话没有工作目录，无法解析相对路径——请用绝对路径重试，或先确认工作目录")
	}
	src, err := resolveSharePath(base, p.Path)
	if err != nil {
		return "", err
	}
	// 扩展名 → mime（白名单之外的格式两家端点都不收，直接拒绝）。
	mime, ok := imageExts[strings.ToLower(filepath.Ext(src))]
	if !ok {
		return "", fmt.Errorf("不支持的图片格式 %q（read_image 支持 png/jpg/jpeg/webp/gif）", filepath.Ext(src))
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("读取文件信息失败: %v", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录——read_image 只接受图片文件", p.Path)
	}
	if info.Size() > llm.MaxImageBytes {
		return "", fmt.Errorf("图片超过大小上限（%d MB，当前 %d MB）——超出端点对视觉源图的限制",
			llm.MaxImageBytes/(1<<20), info.Size()/(1<<20))
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("读取图片失败: %v", err)
	}
	// 内容嗅探：扩展名只是名字，端点收到的是字节——扩展名与真实格式不符的文件
	// 读进去只会产出畸形的视觉请求，宁可在这里拒绝。
	if !sniffImage(mime, data) {
		return "", fmt.Errorf("文件内容不是有效的 %s 图片（扩展名与内容不符？）", mime)
	}
	// 尺寸（png/jpeg/gif 由标准库解码配置给出；webp 标准库不认——如实说未知）。
	dims := "尺寸未知"
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		dims = fmt.Sprintf("%d×%d", cfg.Width, cfg.Height)
	}
	// 图片引用的落盘：复用附件保存口（会话注入）——图片复制进会话附件目录，
	// 引用（相对 attachments 根的路径）随工具结果流动。请求构造瞬间 llm 层用
	// 同一个根把引用读回来编码成 base64（分层纪律见 llm/images.go：base64 绝不
	// 进历史/内存/日志）。
	save := AttachmentSave(ctx)
	if save == nil {
		return "", fmt.Errorf("会话存储未启用，无法把图片注入视觉请求")
	}
	ref, err := save(filepath.Base(src), data)
	if err != nil {
		return "", err
	}
	if ref.Mime == "" {
		ref.Mime = mime
	}
	// 登记进视觉通道（内核把它挂到这条 tool 结果消息上——下一轮请求就带图了）。
	ImageCollectorFrom(ctx).Add(llm.ImageRef{Path: ref.Path, Mime: ref.Mime})

	var b strings.Builder
	fmt.Fprintf(&b, "已读取图片 %s：%s，%s，%s。", p.Path, mime, dims, humanBytes(info.Size()))
	b.WriteString("\n图片内容已作为视觉输入随本条结果注入下一轮请求——你可以直接描述与分析图里的内容。")
	b.WriteString("\n要给用户看这张图，用 share_file（本工具只喂模型，不渲染给用户）。")
	return b.String(), nil
}

// sniffImage 按真实字节核对图片格式（扩展名对不上内容直接拒绝）。
// webp 的 RIFF 头要同时核对容器标记与 "WEBP" 四字节（jpg/gif/png 首字节即定）。
func sniffImage(mime string, data []byte) bool {
	switch mime {
	case "image/png":
		return len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
	case "image/gif":
		return len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a")))
	case "image/webp":
		return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
	}
	return false
}
