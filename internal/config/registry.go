package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fileVersion 是注册表磁盘格式版本；修改磁盘格式时递增并在 Load 里做迁移。
const fileVersion = 1

// 哨兵错误：调用方按类别处理，具体上下文经 fmt.Errorf("%w") 附带。
var (
	ErrNotFound    = errors.New("模型不存在")
	ErrDuplicateID = errors.New("模型 id 已存在")
	ErrRoleInUse   = errors.New("模型被角色引用")
	ErrUnknownRole = errors.New("未知角色")
)

// registryFile 是 models.json 的磁盘格式。
type registryFile struct {
	Version int               `json:"version"`
	Models  []ModelConfig     `json:"models"`
	Roles   map[string]string `json:"roles"`
}

// Registry 是模型注册表：内存态 CRUD + 角色绑定 + 原子文件持久化。
// 每个变更方法内部立即落盘（CLI 场景下避免"忘记保存"丢配置）；
// 并发安全（RWMutex 保护全部状态）。零值不可用，经 Load 构造。
type Registry struct {
	path string

	mu     sync.RWMutex
	models map[string]*ModelConfig
	order  []string // 保持添加顺序，List() 输出稳定
	roles  map[string]string
}

// Load 从 path 读入注册表。文件不存在（含父目录不存在）时返回空注册表——
// 首次运行是正常路径；文件存在但损坏时返回明确错误，不静默重建，
// 避免覆盖手改过的配置。
func Load(path string) (*Registry, error) {
	r := &Registry{
		path:   path,
		models: make(map[string]*ModelConfig),
		roles:  make(map[string]string),
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, nil
		}
		return nil, fmt.Errorf("读取注册表 %s: %w", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取注册表 %s: %w", path, err)
	}
	var f registryFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("注册表 %s 不是合法 JSON: %w", path, err)
	}
	if f.Version != fileVersion {
		return nil, fmt.Errorf("注册表 %s 版本为 %d，本程序只支持版本 %d", path, f.Version, fileVersion)
	}
	for i := range f.Models {
		m := f.Models[i]
		if _, dup := r.models[m.ID]; dup {
			return nil, fmt.Errorf("注册表 %s 存在重复 id %q", path, m.ID)
		}
		if err := m.validate(); err != nil {
			return nil, fmt.Errorf("注册表 %s 条目 %s 校验失败: %w", path, m.ID, err)
		}
		r.models[m.ID] = &m
		r.order = append(r.order, m.ID)
	}
	for role, id := range f.Roles {
		if !validRole(role) {
			return nil, fmt.Errorf("注册表 %s 含未知角色 %q", path, role)
		}
		if id == "" {
			continue
		}
		if _, ok := r.models[id]; !ok {
			return nil, fmt.Errorf("注册表 %s 中角色 %q 指向不存在的模型 %q", path, role, id)
		}
		r.roles[role] = id
	}
	return r, nil
}

// Add 添加模型条目并立即落盘；id 重复或字段非法时拒绝且不改动状态。
func (r *Registry) Add(m ModelConfig) error {
	if err := m.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.models[m.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateID, m.ID)
	}
	m.UpdatedAt = time.Now().UTC()
	m.Format = m.EffectiveFormat()
	r.models[m.ID] = &m
	r.order = append(r.order, m.ID)
	return r.saveLocked()
}

// Update 覆盖更新已存在条目（以 ID 定位）并落盘；校验同 Add。
func (r *Registry) Update(m ModelConfig) error {
	if err := m.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.models[m.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, m.ID)
	}
	m.UpdatedAt = time.Now().UTC()
	m.Format = m.EffectiveFormat()
	r.models[m.ID] = &m
	return r.saveLocked()
}

// Remove 删除模型并落盘；被角色引用时拒绝并列出引用角色（先解绑再删，
// 避免留下悬空绑定导致下次 Load 直接失败）。
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.models[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	var refs []string
	for role, mid := range r.roles {
		if mid == id {
			refs = append(refs, role)
		}
	}
	if len(refs) > 0 {
		sort.Strings(refs)
		return fmt.Errorf("%w: %s（先解除角色绑定再删除）", ErrRoleInUse, strings.Join(refs, ", "))
	}
	delete(r.models, id)
	r.order = removeFromSlice(r.order, id)
	return r.saveLocked()
}

// List 返回全部条目（按添加顺序）。
func (r *Registry) List() []ModelConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelConfig, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.models[id])
	}
	return out
}

// Get 返回指定条目。
func (r *Registry) Get(id string) (ModelConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return ModelConfig{}, false
	}
	return *m, true
}

// SetRole 把角色绑定到模型（须存在且 enabled）并落盘；modelID 为空表示解绑。
func (r *Registry) SetRole(role, modelID string) error {
	if !validRole(role) {
		return fmt.Errorf("%w: %s（合法角色: %s）", ErrUnknownRole, role, strings.Join(Roles, ", "))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if modelID != "" {
		m, ok := r.models[modelID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, modelID)
		}
		if !m.Enabled {
			return fmt.Errorf("模型 %s 已禁用，不能绑定角色 %s", modelID, role)
		}
		r.roles[role] = modelID
	} else {
		delete(r.roles, role)
	}
	return r.saveLocked()
}

// ModelForRole 返回角色当前绑定的模型；未绑定时报错——
// 角色是必选配置，不静默回退到别的模型（绑定错了比报错更糟）。
func (r *Registry) ModelForRole(role string) (ModelConfig, error) {
	if !validRole(role) {
		return ModelConfig{}, fmt.Errorf("%w: %s", ErrUnknownRole, role)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.roles[role]
	if !ok || id == "" {
		return ModelConfig{}, fmt.Errorf("角色 %s 未绑定模型", role)
	}
	m, ok := r.models[id]
	if !ok {
		return ModelConfig{}, fmt.Errorf("角色 %s 指向不存在的模型 %s", role, id)
	}
	return *m, nil
}

// RoleBindings 返回角色 → 模型 id 的快照（未绑定的槽位为空串）。
func (r *Registry) RoleBindings() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(Roles))
	for _, role := range Roles {
		out[role] = r.roles[role]
	}
	return out
}

// Save 显式落盘。变更方法内部已自动保存，此方法留给"加载后仅程序化修改
// 再统一保存"的场景与修复用途。
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

// Reload 从磁盘重新读入注册表并整体替换内存状态（保留实例身份与 path）。
// 供长驻后端周期热加载（手改 models.json 后无需重启服务）。
// 读入或校验失败时保留旧状态并返回错误——降级可用优先于清空。
func (r *Registry) Reload() error {
	nr, err := Load(r.path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models = nr.models
	r.order = nr.order
	r.roles = nr.roles
	return nil
}

// saveLocked 序列化 → 同目录临时文件 → rename 原子替换。调用方须持有写锁。
// CreateTemp 默认 0600：文件含 api_key，仅属主可读（Linux 生效，Windows 忽略）。
func (r *Registry) saveLocked() error {
	if r.path == "" {
		return errors.New("注册表未关联文件路径，无法保存")
	}
	f := registryFile{
		Version: fileVersion,
		Models:  make([]ModelConfig, 0, len(r.order)),
		Roles:   make(map[string]string, len(Roles)),
	}
	for _, id := range r.order {
		f.Models = append(f.Models, *r.models[id])
	}
	for _, role := range Roles {
		f.Roles[role] = r.roles[role]
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化注册表: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".models-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	// rename 成功后这里是 no-op，失败时清理残骸
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写临时文件: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		return fmt.Errorf("原子替换 %s: %w", r.path, err)
	}
	return nil
}

func validRole(role string) bool {
	for _, r := range Roles {
		if r == role {
			return true
		}
	}
	return false
}

func removeFromSlice(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
