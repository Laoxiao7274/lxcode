// jobs.go —— 后台任务运行配置（config/jobs.json，与 models.json 同目录）。
//
// 目前只有一项：寿命护栏（B4）阈值——running 任务无输出超过 idle_ttl_hours
// 被 Manager 自动 Kill（EndedBy=backend），防死循环轮询无限占资源。文件缺失/
// 损坏 = 用默认 24h（坏配置不该拖垮任务功能——与 remote.json 同一条纪律）。
package config

import (
	"encoding/json"
	"os"
	"time"
)

// JobsConfig 是 jobs.json 的载荷。
type JobsConfig struct {
	// IdleTTLHours 是寿命护栏阈值（小时；<=0 = 用内置默认 24）。
	IdleTTLHours float64 `json:"idle_ttl_hours"`
}

// IdleTTL 把阈值翻成时长（<=0 回内置默认——Load 失败的零值走同一条路）。
func (c JobsConfig) IdleTTL() time.Duration {
	if c.IdleTTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(c.IdleTTLHours * float64(time.Hour))
}

// LoadJobs 读 jobs 配置。文件缺失/损坏 = 默认值（不拒绝启动）。
func LoadJobs(path string) JobsConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return JobsConfig{}
	}
	var c JobsConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return JobsConfig{}
	}
	return c
}
