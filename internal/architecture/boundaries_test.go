// Package architecture 验证运行时代码的单向依赖；编排脚本与测试可装配两端。
package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// skipDir 是与 Go 源无关的目录（node_modules 一个就能让遍历慢一个量级——
// 冷缓存下实测风险；.git 同理）。
var skipDir = map[string]bool{
	"node_modules": true,
	".git":         true,
	"release":      true,
	"dist":         true,
	"bin":          true,
}

const prefix = "github.com/moyunteng/lxcode/"

// internalPkg 把仓库内 import 路径归一成 internal/<name> 的 <name>（非 internal
// 或仓库外路径返回空串）。
func internalPkg(dep string) string {
	if !strings.HasPrefix(dep, prefix+"internal/") {
		return ""
	}
	rest := strings.TrimPrefix(dep, prefix+"internal/")
	return strings.Split(rest, "/")[0]
}

// walkGoFiles 从 root 遍历 .go 非测试文件，把（相对 root 的顶层包名、internal
// 依赖列表）交给 fn。root 传 ".."（= internal/，测试包所在目录的上一级）。
func walkGoFiles(t *testing.T, root string, fn func(path, pkg string, deps []string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir[entry.Name()] && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		local, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(local), "/")
		pkg := parts[0]
		var deps []string
		for _, spec := range file.Imports {
			dep, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if ip := internalPkg(dep); ip != "" {
				deps = append(deps, ip)
			}
		}
		fn(path, pkg, deps)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// depsWhitelist 是「调用方 → 允许引用的 internal 包」白名单（不在表里的调用方
// 不受限制——只对边界敏感的包逐个钉）。
var depsWhitelist = map[string]map[string]bool{
	// chatmsg 是叶包：零内部依赖（消息事实模型不依赖任何运行时/存储/传输）。
	"chatmsg": {},
	// llm 是传输客户端：消息模型走 chatmsg（方向 llm→chatmsg，不许反向），
	// 除此之外只允许 jsonrepair（通用 JSON 修复叶包，wire 参数修复用）——
	// 传输层不得依赖 store/agent/server 等运行时。
	"llm": {"chatmsg": true, "jsonrepair": true},
}

// assertAllowed 校验 pkg→dep 是否在白名单内（白名单外调用方不检查）。
func assertAllowed(t *testing.T, path, pkg, dep string) {
	t.Helper()
	allowed, ok := depsWhitelist[pkg]
	if !ok {
		return
	}
	if !allowed[dep] {
		t.Errorf("%s: %s 不得依赖 internal/%s（依赖方向见 AGENTS.md §2 表）", path, pkg, dep)
	}
}

func TestBackendDependencyBoundaries(t *testing.T) {
	walkGoFiles(t, "..", func(path, pkg string, deps []string) {
		for _, dep := range deps {
			depPath := prefix + "internal/" + dep
			if pkg == "agent" && (dep == "store" || dep == "server" || dep == "protocol") {
				t.Errorf("%s: agent 应依赖业务接口而不是存储或传输实现 %s", path, depPath)
			}
			if pkg == "store" && (dep == "agent" || dep == "server" || dep == "protocol") {
				t.Errorf("%s: 存储不得反向依赖运行时或传输层 %s", path, depPath)
			}
			assertAllowed(t, path, pkg, dep)
		}
	})
}

// TestLeafPackagesDoNotImportRuntime：叶包（tools/chatmsg/store）不 import
// server/agent——它们是被装配的底层，反向依赖会让装配环死锁。
func TestLeafPackagesDoNotImportRuntime(t *testing.T) {
	leafNoRuntime := map[string]bool{
		"tools":   true,
		"chatmsg": true,
		"store":   true,
	}
	walkGoFiles(t, "..", func(path, pkg string, deps []string) {
		if !leafNoRuntime[pkg] {
			return
		}
		for _, dep := range deps {
			if dep == "server" || dep == "agent" {
				t.Errorf("%s: 叶包 %s 不得依赖运行时 internal/%s", path, pkg, dep)
			}
		}
	})
}

// TestProtocolIsTransportOnly：protocol 只做 wire——不 import server/agent/store
//（协议层被客户端与服务端共享，依赖运行时会把它拖进后端实现细节）。
func TestProtocolIsTransportOnly(t *testing.T) {
	walkGoFiles(t, "..", func(path, pkg string, deps []string) {
		if pkg != "protocol" {
			return
		}
		for _, dep := range deps {
			if dep == "server" || dep == "agent" || dep == "store" {
				t.Errorf("%s: protocol 不得依赖 internal/%s", path, dep)
			}
		}
	})
}

// TestToolsDoNotImportStore：tools 是工具执行层，依赖业务接口（经注入的回调）
// 而不是存储实现——与 agent 的同一条纪律。
func TestToolsDoNotImportStore(t *testing.T) {
	walkGoFiles(t, "..", func(path, pkg string, deps []string) {
		if pkg != "tools" {
			return
		}
		for _, dep := range deps {
			if dep == "store" {
				t.Errorf("%s: tools 不得依赖存储实现 internal/store（经注入回调访问）", path)
			}
		}
	})
}

// TestCmdIsBackendSide：cmd/* 是后端入口，不得 import frontend/shell（客户端
// 实现；前端另一路并行，后端二进制独立构建）。cmd 在 internal 之外，单独遍历。
func TestCmdIsBackendSide(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", "..", "cmd"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			dep, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(dep, prefix+"frontend") || strings.HasPrefix(dep, prefix+"shell") {
				t.Errorf("%s: cmd 不得依赖客户端实现 %s", path, dep)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestWholeBackendNotClient：全仓后端代码（internal + cmd）不 import frontend/shell。
func TestWholeBackendNotClient(t *testing.T) {
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		walkGoFiles(t, root, func(path, _ string, _ []string) {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range file.Imports {
				dep, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return
				}
				if strings.HasPrefix(dep, prefix+"frontend") || strings.HasPrefix(dep, prefix+"shell") {
					t.Errorf("%s: 后端不得依赖客户端实现 %s", path, dep)
				}
			}
		})
	}
}
