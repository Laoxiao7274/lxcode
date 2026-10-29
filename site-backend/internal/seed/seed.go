// Package seed 把前端的 mock 数据（site/src/shared/mock/releases.ts 的 5 版本 + 5 日志）
// 导入后端库：这是原型阶段让「公开接口一开就有东西」的最短路径。
//
// 与 mock 的差别（如实说明）：**产物文件是后端生成的 dummy 内容**，所以
// sha256 与 size 都是这些真文件算出来的真值——它们不会等于 mock 里手写的
// 90MB / 那串假哈希。契约要求 size/sha256 必须来自真实文件（上传时计算），
// 所以这里宁可让数字变小，也不写一个不存在的体积。
//
// 更新包是**真 zip**（含 resources/app.asar 与 resources/bin/lxcode.exe 两个路径），
// 于是 GET /manifest.json 的 files 哈希就是这两个内部文件的真哈希。
package seed

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moyunteng/lxcode-site-backend/internal/domain"
	"github.com/moyunteng/lxcode-site-backend/internal/store"
)

// releaseSpec 种子版本（与前端 mock 的 5 个版本一一对应）。
type releaseSpec struct {
	version         string
	channel         domain.Channel
	status          domain.ReleaseStatus
	publishedAt     string // 空 = 草稿
	notes           []string
	minVersion      string
	required        bool
	electronVersion string
	downloads       int64
	// portable 是否提供免安装版（mock 里 0.2.0 草稿只有 installer + update）
	portable bool
}

var releaseSpecs = []releaseSpec{
	{
		version: "0.2.0", channel: domain.ChannelBeta, status: domain.StatusDraft,
		notes: []string{
			"自建 zip 更新器：后端热替换（壳不退）+ asar 冷替换（退出时换）",
			"侧键改走真实浏览器历史（History API），不再自造前进后退栈",
		},
		minVersion: "0.1.0", electronVersion: "40.10.2", portable: false,
	},
	{
		version: "0.1.0", channel: domain.ChannelStable, status: domain.StatusPublished,
		publishedAt: "2026-09-29T10:09:00Z",
		notes: []string{
			"会话存储换成 SQLite（WAL），为压缩与多会话并发铺路",
			"上下文计账与压缩：真实用量优先、按字节估算只做回落",
			"官网原型上线：安装包与更新包同一套版本源",
		},
		minVersion: "0.0.1", electronVersion: "40.10.2", downloads: 1284, portable: true,
	},
	{
		version: "0.0.3", channel: domain.ChannelStable, status: domain.StatusPublished,
		publishedAt: "2026-09-16T09:20:00Z",
		notes: []string{
			"桌面壳定案 Electron + Go sidecar，单实例锁 + Job Object 兜底不留孤儿后端",
			"项目会话首次发送时从 HEAD 建独立 Git worktree",
		},
		minVersion: "0.0.1", electronVersion: "39.1.0", downloads: 2641, portable: true,
	},
	{
		version: "0.0.2", channel: domain.ChannelStable, status: domain.StatusRevoked,
		publishedAt: "2026-09-11T14:30:00Z",
		notes: []string{
			"工具面补 read_skill 渐进披露：提示词只注入技能索引",
			"命名：myt-harness → lxcode（module / 目录 / 环境变量 LXCODE_* 全仓重命名）",
		},
		minVersion: "0.0.1", electronVersion: "38.2.0", downloads: 3892, portable: true,
	},
	{
		version: "0.0.1", channel: domain.ChannelStable, status: domain.StatusPublished,
		publishedAt: "2026-09-10T08:00:00Z",
		notes: []string{
			"首个可安装版本：前后台分离，后端 --serve 独立进程（WS JSON-RPC 7789）",
			"内置 14 个工具，含风险分级与确认门",
		},
		minVersion: "0.0.1", electronVersion: "37.4.0", downloads: 9137, portable: true,
	},
}

// changelogSpec 种子日志（与前端 mock 的 5 条一一对应）。
type changelogSpec struct {
	version string
	date    string
	kind    domain.ChangelogKind
	text    string
}

var changelogSpecs = []changelogSpec{
	{"0.2.0", "2026-10-08T06:00:00Z", domain.KindFeatures,
		"更新器改为自建 zip 两级替换：后端 exe 热替换（壳不退），app.asar 退出时冷替换。"},
	{"0.2.0", "2026-10-08T06:00:00Z", domain.KindFixes,
		"侧键前进/后退改走真实浏览器历史（History API），不再自造前进后退栈。"},
	{"0.1.0", "2026-09-29T10:09:00Z", domain.KindFeatures,
		"会话存储换成 SQLite（WAL），支持压缩检查点、语义记忆与多会话并发。"},
	{"0.0.3", "2026-09-16T09:20:00Z", domain.KindBreaking,
		"项目会话首次发送时从 HEAD 建独立 Git worktree，未分组会话仍共用后端默认工作目录。"},
	{"0.0.1", "2026-09-10T08:00:00Z", domain.KindDocs,
		"发布流程文档：版本唯一源 = shell/package.json，安装包 / 二进制 / 更新清单三方同源。"},
}

// dummy 尺寸（字节）。产物是占位内容，所以体积比 mock 里手写的小得多（如实）。
const (
	installerSize = 256 << 10
	portableSize  = 192 << 10
	appAsarSize   = 96 << 10
	binarySize    = 32 << 10
)

// Run 导入种子。**幂等**：先把库与产物目录清空再写，重复执行结果一致。
func Run(st *store.Store, logger *log.Logger) error {
	if err := st.Reset(); err != nil {
		return err
	}
	for _, spec := range releaseSpecs {
		release, err := build(st, spec)
		if err != nil {
			return fmt.Errorf("种子 %s 失败: %w", spec.version, err)
		}
		if err := st.UpsertRelease(release); err != nil {
			return err
		}
		logger.Printf("种子版本 %s（%s/%s，安装包 %s）", spec.version, spec.channel, spec.status,
			domain.FormatBytes(findSize(release, domain.KindInstaller)))
	}
	for _, entry := range changelogSpecs {
		if _, err := st.AddChangelog(domain.ChangelogEntry{
			Version: entry.version, Date: entry.date, Kind: entry.kind, Text: entry.text,
		}); err != nil {
			return err
		}
	}
	logger.Printf("种子更新日志 %d 条", len(changelogSpecs))
	return st.SetMeta("seeded_at", nowStamp())
}

func build(st *store.Store, spec releaseSpec) (domain.Release, error) {
	artifacts := []domain.Artifact{}
	kinds := []domain.ArtifactKind{domain.KindInstaller}
	if spec.portable {
		kinds = append(kinds, domain.KindPortable)
	}
	kinds = append(kinds, domain.KindUpdate)
	for _, kind := range kinds {
		artifact, err := writeArtifact(st, spec.version, kind)
		if err != nil {
			return domain.Release{}, err
		}
		artifacts = append(artifacts, artifact)
	}

	var update *domain.Artifact
	files := map[string]string{}
	for i := range artifacts {
		if artifacts[i].Kind == domain.KindUpdate {
			update = &artifacts[i]
		}
	}
	if update != nil {
		hashes, err := innerHashes(filepath.Join(st.ReleasesDir(), update.Name))
		if err != nil {
			return domain.Release{}, err
		}
		files = hashes
	}

	release := domain.Release{
		Version:   spec.version,
		Channel:   spec.channel,
		Status:    spec.status,
		Notes:     spec.notes,
		Artifacts: artifacts,
		Manifest:  domain.ManifestOf(spec.version, update, files),
		MinVersion: spec.minVersion,
		Required:   spec.required,
		ElectronVersion: spec.electronVersion,
		Downloads:  spec.downloads,
	}
	if spec.publishedAt != "" {
		published := spec.publishedAt
		release.PublishedAt = &published
	}
	return release, nil
}

// writeArtifact 生成占位产物（内容由版本+类别决定，重复 seed 得到同样的哈希），
// 并把**真实算出来的** sha256/size 记进 Artifact。
func writeArtifact(st *store.Store, version string, kind domain.ArtifactKind) (domain.Artifact, error) {
	name := nameOf(version, kind)
	path := filepath.Join(st.ReleasesDir(), name)
	var content []byte
	switch kind {
	case domain.KindInstaller:
		content = dummy("installer/"+version, installerSize)
	case domain.KindPortable:
		content = dummy("portable/"+version, portableSize)
	default:
		var err error
		content, err = updateZip(version)
		if err != nil {
			return domain.Artifact{}, err
		}
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	sum := sha256.Sum256(content)
	return domain.Artifact{
		Kind:   kind,
		Name:   name,
		Size:   int64(len(content)),
		SHA256: hex.EncodeToString(sum[:]),
		FilePath: filepath.Join("releases", name),
	}, nil
}

func nameOf(version string, kind domain.ArtifactKind) string {
	switch kind {
	case domain.KindInstaller:
		return domain.InstallerName(version)
	case domain.KindPortable:
		return domain.PortableName(version)
	default:
		return domain.UpdatePackageName(version)
	}
}

// dummy 造一段可压缩的占位内容：长度确定、内容由 seed 决定 → 哈希可复现。
func dummy(seed string, size int) []byte {
	pattern := []byte("lxcode-dummy-artifact " + seed + "\n")
	out := make([]byte, 0, size)
	for len(out) < size {
		out = append(out, pattern...)
	}
	return out[:size]
}

// updateZip 造一个真 zip：**只含契约要求的两个路径**（安装目录相对路径）。
func updateZip(version string) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entries := []struct {
		path string
		size int
	}{
		{domain.RequiredUpdateFiles[0], appAsarSize},
		{domain.RequiredUpdateFiles[1], binarySize},
	}
	for _, entry := range entries {
		// 时间戳固定：同样输入得到同样 zip 字节 → 哈希可复现（--seed 幂等）
		header := &zip.FileHeader{Name: entry.path, Method: zip.Deflate}
		header.Modified = time.Unix(0, 0).UTC()
		part, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, strings.NewReader(string(dummy(version+"/"+entry.path, entry.size)))); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// innerHashes 取 zip 内两个必需路径的真哈希（与 api 层的同名函数同口径）。
func innerHashes(zipPath string) (map[string]string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	out := map[string]string{}
	for _, file := range reader.File {
		required := false
		for _, want := range domain.RequiredUpdateFiles {
			if file.Name == want {
				required = true
			}
		}
		if !required {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, opened); err != nil {
			opened.Close()
			return nil, err
		}
		opened.Close()
		out[file.Name] = hex.EncodeToString(hash.Sum(nil))
	}
	return out, nil
}

// nowStamp 当前 UTC 时间（RFC3339），种子标记与发布时间用同一格式。
func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

func findSize(release domain.Release, kind domain.ArtifactKind) int64 {
	if a := domain.FindArtifact(release, kind); a != nil {
		return a.Size
	}
	return 0
}
