// 管理站会话与登录限流（内存态）。
//
// 为什么会话不落库：站点是单机小服务，会话是「谁在这台机器上登录了管理台」这种短命状态；
// 落库会引入会话表迁移/清理这类与发布域无关的复杂度。代价是**服务重启后要重新登录**，
// 这一点在报告里如实写明。
//
// 为什么限流也放这里：登录是最容易被在线爆破的入口（单用户 + 固定密码），
// 而站点没有 WAF/网关限流可依赖，所以失败次数必须在进程内自己数。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// sessionCookieName 会话 cookie 名（HttpOnly + SameSite=Lax + Path=/）。
	sessionCookieName = "lxcode_site_session"
	// sessionTTL 会话有效期 24h；每次校验通过都续期（用一次续 24h）。
	sessionTTL = 24 * time.Hour
	// loginPerMinute 同一 IP 每分钟允许的**失败**次数；超出 429。
	loginPerMinute = 10
)

// sessionStore 内存会话表：id → 过期时刻。
type sessionStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	byID map[string]time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, byID: map[string]time.Time{}}
}

// create 生成 32 字节随机会话 id（crypto/rand，不是时间戳——可猜的 id 等于没有会话）。
func (s *sessionStore) create() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[id] = time.Now().Add(s.ttl)
	s.sweepLocked(time.Now())
	return id, nil
}

// valid 校验会话；有效则**续期**（管理台里连续操作不该被 24h 到点踢掉）。
func (s *sessionStore) valid(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	expireAt, ok := s.byID[id]
	if !ok || now.After(expireAt) {
		delete(s.byID, id)
		s.sweepLocked(now)
		return false
	}
	s.byID[id] = now.Add(s.ttl)
	s.sweepLocked(now)
	return true
}

func (s *sessionStore) destroy(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}

// sweepLocked 顺手清过期会话：单用户站点会话很少，请求时扫一遍就够，不需要后台定时器。
func (s *sessionStore) sweepLocked(now time.Time) {
	for id, expireAt := range s.byID {
		if now.After(expireAt) {
			delete(s.byID, id)
		}
	}
}

func (s *sessionStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// loginLimiter 登录**失败**限流：同 IP 每分钟 ≤limit 次失败，超出 429。
type loginLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	failures map[string][]time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{limit: limit, window: window, failures: map[string][]time.Time{}}
}

// blocked 该 IP 是否已被限流（只数失败；密码对了不会被自己之前的失败挡住，成功即清零）。
func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recentLocked(ip)) >= l.limit
}

func (l *loginLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.recentLocked(ip), time.Now())
}

func (l *loginLimiter) clear(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

func (l *loginLimiter) recentLocked(ip string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	kept := l.failures[ip][:0]
	for _, at := range l.failures[ip] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, ip)
		return nil
	}
	l.failures[ip] = kept
	return kept
}
