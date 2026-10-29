// workspace_publish 的复制封装：把会话工作树里的产物（构建产物/生成的文件/
// 目录）同步到项目主检出的对应相对路径。
//
// 与 status.go 同一套纪律：一律带 ctx + 超时；本层只做纯复制与路径净化，
// 不做策略——确认门（要不要覆盖）、规模校验后的人话摘要都归 server 层。
// 三条硬约束：
//  1. 相对路径净化：拒绝绝对路径、`..` 穿越、`.`（整个工作树级别的发布
//     不提供——主检出的写入必须落在明确的相对路径内）；
//  2. 规模保护：文件数与总字节数双上限，超限报「产物过大」——防止一次
//     误发布把 node_modules 这类目录灌进主检出；
//  3. `.git` 永远排除（exclude 名单之外无条件跳过）。
package project

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// publishTimeout 是单次发布的硬超时（复制可能发生在协议请求路径上，不能无限等）。
const publishTimeout = 120 * time.Second

const (
	// PublishMaxFiles 是单次发布的文件数上限：超出报「产物过大」，
	// 提示分批或指定更精确的路径。
	PublishMaxFiles = 2000
	// PublishMaxBytes 是单次发布的总字节数软上限（1GB）：同样报「产物过大」。
	PublishMaxBytes = 1 << 30
)

// PublishPlan 是发布前的只读计划（确认门文案与规模预判共用它）。
type PublishPlan struct {
	Files      []string // 将发布的文件（相对 target 的路径，已按 exclude 过滤）
	Overwrites []string // 目标已存在、将被覆盖的文件（相对 target；Files 的子集）
	TotalBytes int64    // 将发布的总字节数
}

// PlanPublish 生成发布计划（只读，不写任何文件）：统计将发布的文件清单、
// 其中哪些会覆盖主检出已有文件、总字节数。srcRel/dstRel 都必须是净化后的
// 相对路径（本函数内部再做一次净化，双保险）。
func PlanPublish(ctx context.Context, srcRoot, dstRoot, srcRel, dstRel string, exclude []string) (*PublishPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	srcRel, err := CleanPublishRel(srcRel)
	if err != nil {
		return nil, err
	}
	dstRel, err = CleanPublishRel(dstRel)
	if err != nil {
		return nil, err
	}
	src := filepath.Join(srcRoot, srcRel)
	info, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("会话工作树里不存在 %s: %w", srcRel, err)
	}
	plan := &PublishPlan{}
	names := excludeSet(exclude)
	if !info.IsDir() {
		// 单文件：清单就一项；覆盖判定看目标位置。
		if err := checkDirTarget(filepath.Join(dstRoot, dstRel)); err != nil {
			return nil, err
		}
		plan.Files = []string{filepath.Base(srcRel)}
		plan.TotalBytes = info.Size()
		if _, err := os.Stat(filepath.Join(dstRoot, dstRel)); err == nil {
			plan.Overwrites = plan.Files
		}
		return plan, nil
	}
	// 目录：目标位置若已存在同名**文件**，无法把目录发布进去（冲突）。
	if err := checkFileTarget(filepath.Join(dstRoot, dstRel)); err != nil {
		return nil, err
	}
	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sub, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if sub != "." && excludedByNames(sub, names) {
			// 目录级排除：整棵子树跳过（WalkDir 返回 SkipDir）。
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // 非普通文件（符号链接/套接字等）不发布
		}
		dst := filepath.Join(dstRoot, dstRel, sub)
		if _, err := os.Stat(dst); err == nil {
			plan.Overwrites = append(plan.Overwrites, sub)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		plan.TotalBytes += fi.Size()
		plan.Files = append(plan.Files, sub)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("统计发布清单失败: %w", walkErr)
	}
	return plan, nil
}

// PublishPaths 把会话工作树 srcRoot 里的 rel 复制到主检出 dstRoot 的 rel
// （target 缺省 = 与 source 相同的形态）。返回发布的文件数与因 exclude
// 跳过的文件数。拒绝 rel 中的穿越（`..`、绝对路径、`.`）。
func PublishPaths(ctx context.Context, srcRoot, dstRoot, rel string, exclude []string) (published, skipped int, err error) {
	return PublishPathsTo(ctx, srcRoot, dstRoot, rel, rel, exclude)
}

// PublishPathsTo 同 PublishPaths，但 target 可以与 source 不同（都净化后：
// srcRel 是 srcRoot 内的相对路径，dstRel 是 dstRoot 内的相对路径）。
func PublishPathsTo(ctx context.Context, srcRoot, dstRoot, srcRel, dstRel string, exclude []string) (published, skipped int, err error) {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	srcRel, err = CleanPublishRel(srcRel)
	if err != nil {
		return 0, 0, err
	}
	dstRel, err = CleanPublishRel(dstRel)
	if err != nil {
		return 0, 0, err
	}
	src := filepath.Join(srcRoot, srcRel)
	info, err := os.Stat(src)
	if err != nil {
		return 0, 0, fmt.Errorf("会话工作树里不存在 %s: %w", srcRel, err)
	}
	names := excludeSet(exclude)
	if !info.IsDir() {
		if err := checkDirTarget(filepath.Join(dstRoot, dstRel)); err != nil {
			return 0, 0, err
		}
		if err := copyFile(src, filepath.Join(dstRoot, dstRel), info); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	if err := checkFileTarget(filepath.Join(dstRoot, dstRel)); err != nil {
		return 0, 0, err
	}
	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sub, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if sub != "." && excludedByNames(sub, names) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			skipped++
			return nil
		}
		if !d.Type().IsRegular() {
			skipped++ // 非普通文件（符号链接/套接字等）不发布，计入跳过
			return nil
		}
		if published+1 > PublishMaxFiles {
			return fmt.Errorf("产物过大：超过单次发布上限 %d 个文件，请分批或指定更精确的路径", PublishMaxFiles)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if err := copyFile(path, filepath.Join(dstRoot, dstRel, sub), fi); err != nil {
			return err
		}
		published++
		return nil
	})
	if walkErr != nil {
		return published, skipped, walkErr
	}
	return published, skipped, nil
}

// CleanPublishRel 净化并校验发布用的相对路径：拒绝空路径、绝对路径
// （含盘符/卷标与 `/`、`\` 开头）、`..` 穿越、`.`（整个工作树级别的
// 发布不提供）。返回净化后的路径（filepath.Clean）。
func CleanPublishRel(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空（给出工作树内的相对路径，如 dist/app.exe）")
	}
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" ||
		strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", fmt.Errorf("%s 是绝对路径，拒绝发布（只允许会话工作树内的相对路径）", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." {
		return "", fmt.Errorf("不支持发布整个工作树（给出更精确的相对路径，如 dist）")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s 越出了相对路径范围（含 ..），拒绝发布", p)
	}
	return cleaned, nil
}

// copyFile 把单个文件复制到 dst（父目录自动创建，保留文件权限）。
// 目标位置若是已存在的目录，报自解释错误（不能把文件覆盖成目录）。
func copyFile(src, dst string, info fs.FileInfo) error {
	if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		return fmt.Errorf("目标位置 %s 是一个目录，无法覆盖为文件", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建目标目录失败: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("写入 %s 失败: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("复制 %s → %s 失败: %w", src, dst, err)
	}
	return nil
}

// excludeSet 组排除名集合：`.git` 无条件加入（工作树元数据永不发布）；
// 其余取调用方给的名单（按路径段匹配，见 excludedByNames）。
func excludeSet(exclude []string) map[string]bool {
	names := map[string]bool{".git": true}
	for _, n := range exclude {
		if n = strings.TrimSpace(n); n != "" {
			names[n] = true
		}
	}
	return names
}

// excludedByNames 判断相对路径 sub 是否命中排除名：**按路径段匹配**——
// 排除名 `node_modules` 命中 `node_modules/react/index.js`，但不命中
// `my_node_modules/x`。比较不区分大小写（Windows 路径不区分大小写）。
func excludedByNames(sub string, names map[string]bool) bool {
	for _, seg := range strings.FieldsFunc(sub, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	}) {
		for name := range names {
			if strings.EqualFold(seg, name) {
				return true
			}
		}
	}
	return false
}

// checkDirTarget 校验「目标是文件」场景：目标位置已存在且是目录时报错
// （文件不能覆盖成目录）。
func checkDirTarget(dst string) error {
	if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		return fmt.Errorf("主检出的目标位置 %s 是一个目录，无法把文件发布到这里（换一个 target 或先清理）", dst)
	}
	return nil
}

// checkFileTarget 校验「目标是目录」场景：目标位置已存在且是普通文件时报错
// （目录不能覆盖成文件）。
func checkFileTarget(dst string) error {
	if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() {
		return fmt.Errorf("主检出已存在同名文件 %s，无法把目录发布到这里（换一个 target 或先清理）", dst)
	}
	return nil
}
