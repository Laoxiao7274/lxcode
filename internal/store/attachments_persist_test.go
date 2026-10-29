// 附件展示通道（messages.attachments 列 + SaveSharedAttachment）的持久化测试：
// user 消息与 tool 消息的附件元数据随消息落库，Load 读回同一份（实时与回放
// 同一形状的存储侧根基）；老行（列默认 ''）读回无附件、零影响。
package store

import (
	"slices"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func attRef(name, kind string) llm.AttachmentRef {
	return llm.AttachmentRef{Path: "s-1/abc.png", Name: name, Size: 12, Mime: "image/png", Kind: kind}
}

// TestSaveSharedAttachment：登记 → 磁盘在附件根下、元数据齐全（净化名/大小/分类）。
func TestSaveSharedAttachment(t *testing.T) {
	st := newTestStore(t)
	ref, err := st.SaveSharedAttachment("20260101-000000-ab", "chart.png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "chart.png" || ref.Size != 9 {
		t.Fatalf("元数据不符: %+v", ref)
	}
	if ref.Kind != llm.AttachmentKindImage || ref.Mime != "image/png" {
		t.Fatalf("图片应按扩展名分类: %+v", ref)
	}
	if !slices.Contains([]string{".png"}, extOf(ref.Path)) {
		t.Fatalf("路径形态不对: %q", ref.Path)
	}
	// 非图片 → file 卡
	ref2, err := st.SaveSharedAttachment("20260101-000000-ab", "数据 v2.csv", []byte("a,b"))
	if err != nil {
		t.Fatal(err)
	}
	if ref2.Kind != llm.AttachmentKindFile || ref2.Mime != "" {
		t.Fatalf("非图片应是 file: %+v", ref2)
	}
	if ref2.Name != "数据 v2.csv" {
		t.Fatalf("展示名应保留（净化后）: %+v", ref2)
	}
}

// TestAttachmentsPersistRoundTrip：带附件的 user/tool 消息落库 → Load 读回同一份；
// 不带附件的消息不受影响（老行语义：列默认 '' 读回零附件）。
func TestAttachmentsPersistRoundTrip(t *testing.T) {
	st := newTestStore(t)
	id, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	userAtts := []llm.AttachmentRef{
		attRef("chart.png", llm.AttachmentKindImage),
		{Name: "数据.csv", Path: "s-1/def.csv", Size: 4, Kind: llm.AttachmentKindFile},
	}
	toolAtts := []llm.AttachmentRef{
		{Name: "report.txt", Path: "s-1/ghi.txt", Size: 7, Kind: llm.AttachmentKindFile, Caption: "验收报告"},
	}
	if _, err := st.AppendMsg(id, llm.Message{Role: "user", Content: "看图", Attachments: userAtts}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMsg(id, llm.Message{Role: "assistant", Content: "好的"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMsg(id, llm.Message{Role: "tool", ToolCallID: "c1", Content: "已共享", Attachments: toolAtts}); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("应读回 3 条: %d", len(msgs))
	}
	if len(msgs[0].Attachments) != 2 || msgs[0].Attachments[0].Name != "chart.png" ||
		msgs[0].Attachments[1].Caption != "" {
		t.Fatalf("user 消息附件读回不一致: %+v", msgs[0].Attachments)
	}
	if len(msgs[1].Attachments) != 0 {
		t.Fatalf("无附件消息读回应为空: %+v", msgs[1].Attachments)
	}
	if len(msgs[2].Attachments) != 1 || msgs[2].Attachments[0].Caption != "验收报告" {
		t.Fatalf("tool 消息附件读回不一致: %+v", msgs[2].Attachments)
	}
}

// extOf 取路径的扩展名（含点）。
func extOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}
