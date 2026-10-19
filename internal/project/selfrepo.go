// 「这是不是 lxcode 本体仓库」的判据（推送闸门用）。
//
// 为什么需要：lxcode 本体的本地 master 上全是**逐轮检查点提交**（每轮对话一个
// `[checkpoint]`），发版走 `scripts/push-release.mjs` 把改动聚合成一条
// `release: <version>` 快照提交推 origin/master——本地 master 一根毫毛都不该直推
// 到远端（推出去等于把逐轮检查点公开）。workspace_sync 的 push 是给**用户自己的
// 项目**用的，所以在工具层拦下本仓库的直推、只放行提交。
//
// 判据用**两个特征文件同时在场**（而不是单看一个）：
//   - `scripts/push-release.mjs`：发版聚合脚本（本仓库独有）；
//   - `docs/git-history.md`：检查点/发布线的完整契约文档（本仓库独有）。
//
// 双重特征的理由是误判代价不对称：**假阳性**（把用户项目当成 lxcode 本体）会让
// 用户的推送被无理由拒绝；**假阴性**（漏判）会把检查点推出去。两个只有本仓库才有的
// 文件同时在场才认定，误判面最小；判据写在这里（project 包）而不是 server 内联，
// 是为了让它能被单测直接覆盖（server 侧只负责把结论翻译成人话错误）。
package project

import (
	"os"
	"path/filepath"
)

// 本仓库的指纹文件（相对仓库根）。
const (
	selfRepoSyncScript = "scripts/push-release.mjs"
	selfRepoHistoryDoc = "docs/git-history.md"
)

// SelfRepoReleaseCommand 是本仓库的唯一发版通道（错误文案里的指引）。
const SelfRepoReleaseCommand = "node scripts/push-release.mjs [version]"

// IsSelfRepo 报告 repoDir 是否是 lxcode 本体仓库（两个指纹文件同时在场）。
// 只读；目录不存在/不可读一律返回 false（判不了就当普通项目——闸门的兜底是
// 提示词纪律，而不是让所有推送都失败）。
func IsSelfRepo(repoDir string) bool {
	if repoDir == "" {
		return false
	}
	return fileExists(filepath.Join(repoDir, selfRepoSyncScript)) &&
		fileExists(filepath.Join(repoDir, selfRepoHistoryDoc))
}

// fileExists 报告路径是否存在且是普通文件（目录同名不算）。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
