// Package domain 是站点的发布域模型：**逐字段对齐前端 site/src/shared/release.ts**。
//
// 为什么服务端要有这份 Go 副本：正式形态里 manifest.json 由服务端产出、客户端读，
// 而前端原型里的 release.ts 已经把字段口径与校验规则定下来了（含注释里的理由）。
// 两边各写一套的代价是静默漂移——症状就是「后台显示发布成功、客户端永远说已是最新」，
// 所以这里是那份契约的 Go 实现：命名、版本比较、发布前校验逐条对应；
// domain_test.go 与前端 site/tests/release.test.mjs 钉同一组不变量。
//
// 形状锁定：更新包 zip 内只含 REQUIRED_UPDATE_FILES 两个路径（安装目录相对路径）。
package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Channel 发布渠道。stable = 默认更新链；beta = 需在客户端显式切换才收到。
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// ArtifactKind 产物类别。三者的差别不是「格式」而是「更新路径」：
// installer 是唯一能带 Electron 升级的全量包；portable 是绿色分发；
// update 是两级替换（后端热替换 / asar 冷替换）的增量包。
type ArtifactKind string

const (
	KindInstaller ArtifactKind = "installer"
	KindPortable  ArtifactKind = "portable"
	KindUpdate    ArtifactKind = "update"
)

// ReleaseStatus 发布状态：草稿不进更新链；撤回 = 从更新链摘掉但保留历史。
type ReleaseStatus string

const (
	StatusDraft     ReleaseStatus = "draft"
	StatusPublished ReleaseStatus = "published"
	StatusRevoked   ReleaseStatus = "revoked"
)

// RequiredUpdateFiles 更新包 zip 内必须存在的路径（= 安装目录相对路径，见 scripts/build.mjs）。
var RequiredUpdateFiles = []string{"resources/app.asar", "resources/bin/lxcode.exe"}

// Artifact 一个产物。Name 含扩展名；update 包的名字必须与 manifest.url 一致。
type Artifact struct {
	Kind   ArtifactKind `json:"kind"`
	Name   string       `json:"name"`
	Size   int64        `json:"size"`
	SHA256 string       `json:"sha256"`
	// FilePath 产物在磁盘上的相对路径（data/releases/<name>）。**不出现在 API 里**：
	// 对外只暴露文件名（与 manifest.url 同一口径），落盘位置是实现细节。
	FilePath string `json:"-"`
}

// Manifest 更新清单：与 shell/release/manifest.json 逐字段一致。
type Manifest struct {
	Version string            `json:"version"`
	URL     string            `json:"url"`
	SHA256  string            `json:"sha256"`
	Size    int64             `json:"size"`
	Files   map[string]string `json:"files"`
	// Notes 更新内容（按行切好的发布说明）：读侧 manifest handler 从 release.Notes
	// 填充——ManifestOf 是纯函数不碰 release；omitempty = 没有 notes 的旧数据不输出
	// 该键，客户端旧版本忽略未知字段，向后兼容。
	Notes []string `json:"notes,omitempty"`
}

// Release 一个发布版本。字段名与前端 Release 接口一一对应（camelCase JSON）。
type Release struct {
	Version   string        `json:"version"`
	Channel   Channel       `json:"channel"`
	Status    ReleaseStatus `json:"status"`
	PublishedAt *string      `json:"publishedAt"`
	Notes     []string      `json:"notes"`
	Artifacts []Artifact    `json:"artifacts"`
	Manifest  Manifest      `json:"manifest"`
	MinVersion string        `json:"minVersion"`
	Required  bool          `json:"required"`
	ElectronVersion string  `json:"electronVersion"`
	Downloads int64         `json:"downloads"`
}

// ChangelogKind 更新日志分类。四类与前端 base.css 的 .tag-* 一一对应。
type ChangelogKind string

const (
	KindFeatures ChangelogKind = "features"
	KindFixes    ChangelogKind = "fixes"
	KindBreaking ChangelogKind = "breaking"
	KindDocs     ChangelogKind = "docs"
)

var ChangelogKinds = []ChangelogKind{KindFeatures, KindFixes, KindBreaking, KindDocs}

// ChangelogEntry 一条更新日志。ID 是服务端主键（前端编辑/删除按 id 定位）。
type ChangelogEntry struct {
	ID      int64         `json:"id"`
	Version string        `json:"version"`
	Date    string        `json:"date"`
	Kind    ChangelogKind `json:"kind"`
	Text    string        `json:"text"`
}

// UpdatePackageName 更新包文件名（manifest.url 与产物名同源）。
func UpdatePackageName(version string) string {
	return "update-" + version + ".zip"
}

// InstallerName electron-builder 的 NSIS 产物名（productName 含空格）。
func InstallerName(version string) string {
	return "Lxcode Setup " + version + ".exe"
}

// PortableName 免安装版（win-unpacked 打包名）。
func PortableName(version string) string {
	return "Lxcode-" + version + "-win-x64.zip"
}

// IsValidVersion 版本号只认三段数字（与 shell/package.json 的 version 同形）。
func IsValidVersion(v string) bool {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// versionPattern 版本号：只认三段数字（与 shell/package.json 的 version 同形）。
var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// ParseVersionFromName 从文件名里解析版本号（发布页自动填版本，避免手抄错）。
func ParseVersionFromName(name string) (string, bool) {
	found := versionPattern.FindString(name)
	return found, found != ""
}

// CompareVersions 语义化版本比较：a > b 返回正数。只比较三段数字。
func CompareVersions(a, b string) int {
	pa := versionParts(a)
	pb := versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out[i] = n
	}
	return out
}

// SortByVersionDesc 按版本号降序（新版本在前）。不修改入参。
func SortByVersionDesc(list []Release) []Release {
	out := make([]Release, len(list))
	copy(out, list)
	sort.SliceStable(out, func(i, j int) bool {
		return CompareVersions(out[i].Version, out[j].Version) > 0
	})
	return out
}

// LatestPublished 某个渠道下、处于「已发布」状态的最新版本；没有则 nil。
func LatestPublished(list []Release, channel Channel) *Release {
	var found []Release
	for _, r := range list {
		if r.Status == StatusPublished && r.Channel == channel {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sorted := SortByVersionDesc(found)
	return &sorted[0]
}

// FindArtifact 取某类产物；没有则 nil。
func FindArtifact(release Release, kind ArtifactKind) *Artifact {
	for i := range release.Artifacts {
		if release.Artifacts[i].Kind == kind {
			return &release.Artifacts[i]
		}
	}
	return nil
}

// FormatBytes 字节数人性化。口径与前端 release.ts 的 formatBytes 一致：
// 1024 进制、四档单位、≥100 取整、其余一位小数——两处显示同一份产物的大小必须同形。
func FormatBytes(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(bytes)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 100 {
		return strconv.FormatFloat(v, 'f', 0, 64) + " " + units[i]
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

// CheckLevel 校验等级。pass 通过 / warn 提醒 / fail 阻断。
type CheckLevel string

const (
	LevelPass CheckLevel = "pass"
	LevelWarn CheckLevel = "warn"
	LevelFail CheckLevel = "fail"
)

// PublishCheck 一条发布前校验（后台清单里显示的一行）。
type PublishCheck struct {
	Level  CheckLevel `json:"level"`
	Label  string     `json:"label"`
	Detail string     `json:"detail"`
}

// DraftCheckInput 校验入参：与前端 checkReleaseDraft 的 draft 参数一一对应。
type DraftCheckInput struct {
	Version                string
	Channel                Channel
	Artifacts              []Artifact
	ElectronVersion        string
	PreviousElectronVersion *string
	SHA256OfUpdate         string
}

// CheckReleaseDraft 发布前校验：与前端 release.ts 的 checkReleaseDraft 同规则同文案。
// 规则（以 release.ts 注释为准）：
//   版本号格式（fail）· 版本号唯一性（fail）· 安装包缺失（fail）/ 命名（pass|warn）
//   更新包缺失（fail）/ 命名不符（fail）/ 哈希不符（fail）/ 齐备（pass）
//   免安装版缺失（warn）· Electron 版本变化（warn）· 可发布性（pass|warn）
func CheckReleaseDraft(draft DraftCheckInput, existing []Release) []PublishCheck {
	out := []PublishCheck{}

	if !IsValidVersion(draft.Version) {
		out = append(out, PublishCheck{LevelFail, "版本号格式", "必须形如 0.2.0（三段数字）"})
	} else {
		var dup *Release
		for i := range existing {
			if existing[i].Version == draft.Version {
				dup = &existing[i]
				break
			}
		}
		if dup != nil {
			out = append(out, PublishCheck{LevelFail, "版本号唯一性",
				fmt.Sprintf("%s 已存在（%s），发布前请先撤回或删除", draft.Version, statusText(dup.Status))})
		} else {
			out = append(out, PublishCheck{LevelPass, "版本号唯一性", draft.Version + " 未被占用"})
		}
	}

	byKind := func(k ArtifactKind) []Artifact {
		var out []Artifact
		for _, a := range draft.Artifacts {
			if a.Kind == k {
				out = append(out, a)
			}
		}
		return out
	}
	first := func(k ArtifactKind) *Artifact {
		got := byKind(k)
		if len(got) == 0 {
			return nil
		}
		return &got[0]
	}

	installer := first(KindInstaller)
	portable := first(KindPortable)
	update := first(KindUpdate)

	if installer == nil {
		out = append(out, PublishCheck{LevelFail, "安装包", "缺少 NSIS 安装包：首次安装与 Electron 升级都走它"})
	} else if installer.Name == InstallerName(draft.Version) {
		out = append(out, PublishCheck{LevelPass, "安装包命名", "文件名与版本一致：" + installer.Name})
	} else {
		out = append(out, PublishCheck{LevelWarn, "安装包命名",
			fmt.Sprintf("建议命名为 %s（electron-builder 默认产物名），当前 %s", InstallerName(draft.Version), installer.Name)})
	}

	switch {
	case update == nil:
		out = append(out, PublishCheck{LevelFail, "更新包", "缺少 update-<version>.zip：客户端增量更新链断掉"})
	case update.Name != UpdatePackageName(draft.Version):
		out = append(out, PublishCheck{LevelFail, "更新包命名",
			fmt.Sprintf("必须叫 %s，清单里的 url 直接指向它", UpdatePackageName(draft.Version))})
	case update.SHA256 != draft.SHA256OfUpdate:
		out = append(out, PublishCheck{LevelFail, "更新包哈希", "manifest.sha256 与更新包实际哈希不一致"})
	default:
		out = append(out, PublishCheck{LevelPass, "更新包",
			fmt.Sprintf("%s（%s，哈希已核对）", update.Name, FormatBytes(update.Size))})
	}

	if portable == nil {
		out = append(out, PublishCheck{LevelWarn, "免安装版", "缺少 win-unpacked 全量包：绿色分发与排障需要它"})
	}

	if draft.PreviousElectronVersion != nil && draft.ElectronVersion != *draft.PreviousElectronVersion {
		out = append(out, PublishCheck{LevelWarn, "Electron 版本变化",
			fmt.Sprintf("Electron %s → %s：更新包只能换 app.asar 与后端，壳必须发全量安装包",
				*draft.PreviousElectronVersion, draft.ElectronVersion)})
	}

	if CanPublish(out) {
		out = append(out, PublishCheck{LevelPass, "可发布性", "清单齐备，可以发布"})
	} else {
		out = append(out, PublishCheck{LevelWarn, "可发布性", "存在阻断项，发布按钮保持禁用"})
	}

	return out
}

// CanPublish 有阻断项就不能发布（与前端 canPublish 同规则）。
func CanPublish(checks []PublishCheck) bool {
	for _, c := range checks {
		if c.Level == LevelFail {
			return false
		}
	}
	return true
}

func statusText(s ReleaseStatus) string {
	switch s {
	case StatusPublished:
		return "已发布"
	case StatusDraft:
		return "草稿"
	default:
		return "已撤回"
	}
}

// ManifestOf 按契约生成更新清单：url 相对 **manifest 所在目录**指向产物路由
// （/releases/<file>——部署在任意前缀（如反代的 /site）下都成立）；files 的键
// 是安装目录相对路径。裸文件名是错的：那会解析到 SPA 的 index.html 回落上，
// 客户端拿到 6MB 的 HTML 当更新包（sha256 校验兜底会拦住，但更新永远失败）。
func ManifestOf(version string, update *Artifact, files map[string]string) Manifest {
	m := Manifest{Version: version, URL: "releases/" + UpdatePackageName(version), Files: files}
	if update != nil {
		m.SHA256 = update.SHA256
		m.Size = update.Size
	}
	return m
}
