// Package api 是站点的 HTTP 接口层：JSON over HTTP（端口 5201）。
//
// 路由一览（细节见 README 与报告）：
//   公开读：GET /api/releases（仅 published，?include=revoked 带撤回历史）
//           GET /api/releases/latest（最新已发布 stable，?channel=beta 可切渠道）
//           GET /api/changelog（仅非草稿版本的条目）
//           GET /manifest.json（按契约生成：最新 stable 的更新包字段 + files 哈希）
//           GET /releases/<file>（静态产物，仅已发布版本可取；草稿/撤回 404）
//   登录会话：POST /api/admin/login（密码 = 现有 token；发 HttpOnly 会话 cookie，24h 续期）
//             POST /api/admin/logout（销毁会话）
//             GET  /api/admin/me（会话有效 → {"authenticated":true}，否则 401）
//   管理写：认证 = **会话 cookie 或 Bearer token 二选一**（cookie 给管理台，Bearer 给脚本/CI）
//           POST   /api/admin/releases                    multipart 上传（状态=draft）
//           POST   /api/admin/releases/check               dry-run 校验（不落盘）
//           POST   /api/admin/releases/{version}/status    publish / revoke
//           GET    /api/admin/releases                    含草稿与撤回
//           POST   /api/admin/changelog                    新增
//           PUT    /api/admin/changelog/{id}                修改
//           DELETE /api/admin/changelog/{id}                 删除
//
// 认证：凭据（密码/token）从 --token 或 LXCODE_SITE_TOKEN 来；**未配置则拒绝所有管理请求**并日志说明
//（默认拒绝，比默认放行安全：忘了配 token 的代价是没人能改，不是谁都能改）。
// 会话是**内存态**（重启即失效，要重新登录），登录失败同 IP 每分钟 ≤10 次否则 429。
package api

import (
	"archive/zip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moyunteng/lxcode-site-backend/internal/domain"
	"github.com/moyunteng/lxcode-site-backend/internal/store"
)

type Config struct {
	Token       string
	CORSOrigins []string
	// SessionTTL 会话有效期（0 用默认 24h）；LoginPerMinute 登录失败限流（0 用默认 10）。
	SessionTTL      time.Duration
	LoginPerMinute  int
	// StaticDir 非空时托管前端构建产物（--static）：未匹配路径回落 index.html。
	StaticDir    string
	MaxUploadMiB int64
}

type Server struct {
	store    *store.Store
	cfg      Config
	logger   *log.Logger
	sessions *sessionStore
	logins   *loginLimiter
}

func New(st *store.Store, cfg Config, logger *log.Logger) *Server {
	if cfg.MaxUploadMiB <= 0 {
		cfg.MaxUploadMiB = 512
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = sessionTTL
	}
	if cfg.LoginPerMinute <= 0 {
		cfg.LoginPerMinute = loginPerMinute
	}
	return &Server{
		store:    st,
		cfg:      cfg,
		logger:   logger,
		sessions: newSessionStore(cfg.SessionTTL),
		logins:   newLoginLimiter(cfg.LoginPerMinute, time.Minute),
	}
}

// Handler 返回带「日志 + CORS」的处理器。每个请求一行日志：方法 / 路径 / 状态 / 耗时。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /api/releases", s.listReleases)
	mux.HandleFunc("GET /api/releases/latest", s.latestRelease)
	mux.HandleFunc("GET /api/changelog", s.listChangelog)
	mux.HandleFunc("GET /manifest.json", s.manifest)
	mux.HandleFunc("GET /releases/{file}", s.artifact)

	mux.HandleFunc("POST /api/admin/login", s.login)
	mux.HandleFunc("POST /api/admin/logout", s.logout)
	mux.HandleFunc("GET /api/admin/me", s.me)
	mux.HandleFunc("GET /api/admin/releases", s.auth(s.adminListReleases))
	mux.HandleFunc("POST /api/admin/releases", s.auth(s.uploadRelease))
	mux.HandleFunc("POST /api/admin/releases/check", s.auth(s.checkDraft))
	mux.HandleFunc("POST /api/admin/releases/{version}/status", s.auth(s.setStatus))
	mux.HandleFunc("POST /api/admin/changelog", s.auth(s.createChangelog))
	mux.HandleFunc("PUT /api/admin/changelog/{id}", s.auth(s.updateChangelog))
	mux.HandleFunc("DELETE /api/admin/changelog/{id}", s.auth(s.deleteChangelog))

	// 静态托管（--static）：兜底模式优先级最低——上面的精确模式一律先匹配，
	// 于是 /api/*、/releases/*、/manifest.json、/health 不会被站点文件遮蔽。
	if s.cfg.StaticDir != "" {
		mux.Handle("GET /", s.staticFiles())
	}

	return s.withCORS(s.withLogging(mux))
}

/** ---------- 中间件 ---------- */

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.logger.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), status,
			time.Since(start).Round(time.Millisecond))
	})
}

// withCORS dev 模式放开到前端的 origin（走 vite proxy 时其实同源，不需要 CORS；
// 这里保留是为了「前端直连 520主体」这种调试方式也能用）。
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	for _, allowed := range s.cfg.CORSOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// auth 管理接口的鉴权：**会话 cookie 或 Bearer token 二选一**。
// cookie 给管理台（HttpOnly，JS 读不到）；Bearer 给脚本/CI（兼容原有调用方式）。
// 未配置凭据时拒绝一切管理请求（默认拒绝），并日志说明原因。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			if s.cfg.Token == "" {
				s.logger.Printf("拒绝管理请求 %s %s：未配置凭据（--token 或 LXCODE_SITE_TOKEN）",
					r.Method, r.URL.Path)
				writeError(w, http.StatusServiceUnavailable, "服务端未配置管理凭据，已拒绝所有管理请求")
				return
			}
			writeError(w, http.StatusUnauthorized, "未登录（会话 cookie 或 Authorization: Bearer <token>）")
			return
		}
		next(w, r)
	}
}

// authenticated 双通道鉴权：先看会话 cookie，再看 Bearer token。
func (s *Server) authenticated(r *http.Request) bool {
	if s.cfg.Token == "" {
		return false
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && s.sessions.valid(cookie.Value) {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(header[len(prefix):])
	// 常量时间比较：普通 == 会随前缀命中长度提前返回，等于给爆破一个计时侧信道
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) == 1
}

/** ---------- 登录 / 登出 / 会话 ---------- */

// login 用现有凭据换一个会话 cookie。失败 401；同 IP 每分钟失败 ≥10 次 → 429。
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token == "" {
		s.logger.Printf("拒绝登录 %s：未配置凭据（--token 或 LXCODE_SITE_TOKEN）", r.URL.Path)
		writeError(w, http.StatusServiceUnavailable, "服务端未配置管理凭据，无法登录")
		return
	}
	ip := clientIP(r)
	if s.logins.blocked(ip) {
		s.logger.Printf("登录限流：%s 一分钟内失败已达 %d 次", ip, s.cfg.LoginPerMinute)
		writeError(w, http.StatusTooManyRequests, "尝试过多，稍后再试")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.cfg.Token)) != 1 {
		s.logins.recordFailure(ip)
		s.logger.Printf("登录失败：%s", ip)
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	s.logins.clear(ip)
	id, err := s.sessions.create()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成会话失败: "+err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true, // JS 读不到会话 id：XSS 也偷不走
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(s.cfg.SessionTTL / time.Second),
	})
	s.logger.Printf("登录成功：%s（会话数 %d）", ip, s.sessions.count())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// logout 销毁会话（同时清 cookie）。未登录也返回 ok：登出不该因为「本来就没登录」报错。
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.destroy(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// me 会话探测：管理台用它判断「该显示登录页还是管理台」。
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token == "" {
		writeError(w, http.StatusServiceUnavailable, "服务端未配置管理凭据，无法登录")
		return
	}
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "未登录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

// isHTTPS 判断这次请求是不是走 HTTPS（决定 cookie 的 Secure 位）。
// 直接 TLS 或网关转发（X-Forwarded-Proto: https）都算；本地 dev 是 http，不加 Secure，
// 否则浏览器会把 cookie 丢掉、本地登录直接坏掉。
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// clientIP 取客户端 IP 做限流键。网关转发时 RemoteAddr 是网关自己，
// 所以优先 X-Forwarded-For 的第一段（单机站点，不防伪造头——如实说明）。
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, found := strings.Cut(forwarded, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

/** ---------- 公开读 ---------- */

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	includeRevoked := r.URL.Query().Get("include") == "revoked"
	releases, err := s.store.ListReleases(false, includeRevoked)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (s *Server) latestRelease(w http.ResponseWriter, r *http.Request) {
	channel := domain.Channel(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = domain.ChannelStable
	}
	release, err := s.store.LatestPublished(channel)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有已发布的 "+string(channel)+" 版本")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) listChangelog(w http.ResponseWriter, _ *http.Request) {
	entries, err := s.store.ListChangelog(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// manifest 按契约生成：最新 stable 已发布版本的更新包字段 + files 哈希。
// URL 在读侧按产物路由归一（releases/<file>）：老库存的清单是裸文件名
// （解析到 SPA 回落上），这里自愈——URL 本来就是产物名的纯函数，不配持久化。
func (s *Server) manifest(w http.ResponseWriter, _ *http.Request) {
	release, err := s.store.LatestPublished(domain.ChannelStable)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有已发布的 stable 版本")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if update := findArtifact(release.Artifacts, domain.KindUpdate); update != nil {
		release.Manifest.URL = "releases/" + update.Name
	}
	// notes 一并下发（渲染层更新 UI 显示更新内容）：release.Notes 是发布时按行
	// 切好的说明，Manifest 本体不落库这个字段，读侧补上；旧数据 Notes 为空则不输出。
	manifest := release.Manifest
	manifest.Notes = release.Notes
	writeJSON(w, http.StatusOK, manifest)
}

// artifact 静态产物：只有「已发布」版本的产物可取；草稿与已撤回一律 404。
func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "非法文件名")
		return
	}
	release, err := s.store.ReleaseOfArtifact(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "没有这个产物")
		return
	}
	if release.Status != domain.StatusPublished {
		// 撤回 = 从更新链摘掉、不提供下载；草稿没发布过，也不给
		writeError(w, http.StatusNotFound,
			"该版本状态为 "+string(release.Status)+"，不提供下载")
		return
	}
	path := filepath.Join(s.store.ReleasesDir(), name)
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "产物文件不在磁盘上："+name)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.IncrementDownloads(release.Version); err != nil {
		s.logger.Printf("下载计数失败 %s: %v", release.Version, err)
	}
	w.Header().Set("Content-Type", contentTypeOf(name))
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// staticFiles 托管前端构建产物（--static 指向 dist 目录）。
//
// 语义：存在的静态文件按扩展名给 Content-Type 直接发；其余一律回落 index.html——
// 站点用哈希路由（#/admin/releases），真文件其实只有 /，但回落对将来的 path 路由无害。
// 路径先经 path.Clean 归一、再校验落在 dist 之内：`..` 逃逸与目录穿越都到不了磁盘。
func (s *Server) staticFiles() http.Handler {
	root := s.cfg.StaticDir
	index := filepath.Join(root, "index.html")
	adminIndex := filepath.Join(root, "admin.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// 管理站是**独立入口**（admin.html）：/admin、/admin/ 与 /admin/<任意> 都发它。
		// 管理站内部是 hash 路由（#/releases），所以服务端只要把入口 HTML 发出去即可；
		// 静态资源仍走 /assets/*（同一份构建产物，两个入口共用）。
		if name == "admin" || strings.HasPrefix(name, "admin/") {
			if _, err := os.Stat(adminIndex); err != nil {
				writeError(w, http.StatusNotFound, "管理站入口文件不存在："+adminIndex)
				return
			}
			s.serveStatic(w, r, adminIndex, "admin.html")
			return
		}
		if name != "" {
			target := filepath.Join(root, filepath.FromSlash(name))
			if within(root, target) {
				if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
					s.serveStatic(w, r, target, name)
					return
				}
			}
		}
		if _, err := os.Stat(index); err != nil {
			writeError(w, http.StatusNotFound, "站点入口文件不存在："+index)
			return
		}
		s.serveStatic(w, r, index, "index.html")
	})
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, file string, name string) {
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		// mime 的内置表不含字体格式（woff2 会落到 octet-stream），
		// 而站点自带 Inter / JetBrains Mono——补一张小表，浏览器才按字体处理。
		contentType = fontContentTypes[strings.ToLower(filepath.Ext(name))]
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if strings.EqualFold(path.Base(name), "index.html") {
		// 入口 HTML 不缓存：发新版本后旧 HTML 会指向已删除的哈希资源
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, path.Base(name), info.ModTime(), f)
}

// fontContentTypes 补 mime 内置表缺的字体格式（站点自带 Inter / JetBrains Mono）。
var fontContentTypes = map[string]string{
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
}

// within 判断 target 是否落在 root 之内（拦截 Join 归一后仍逃出 root 的路径）。
func within(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func contentTypeOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe":
		return "application/vnd.microsoft.portable-executable"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

/** ---------- 管理读 ---------- */

func (s *Server) adminListReleases(w http.ResponseWriter, _ *http.Request) {
	releases, err := s.store.ListReleases(true, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

/** ---------- 管理写 ---------- */

type uploadResult struct {
	Kind   domain.ArtifactKind
	Name   string
	Size   int64
	SHA256 string
	// TempPath 上传落到的临时文件（校验通过后 rename 到 data/releases/）
	TempPath string
}

// parseUpload 解析 multipart（元数据字段 + 三类产物文件）并落临时文件 + 算真哈希。
// upload 与 dry-run 共用它：**同一条解析与校验路径**，预览与真上传不会漂移。
func (s *Server) parseUpload(w http.ResponseWriter, r *http.Request) (map[string]string, map[domain.ArtifactKind]*uploadResult, string, bool) {
	limit := s.cfg.MaxUploadMiB << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "不是 multipart/form-data: "+err.Error())
		return nil, nil, "", false
	}
	tempDir, err := os.MkdirTemp("", "lxcode-upload-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, "", false
	}
	meta := map[string]string{}
	uploads := map[domain.ArtifactKind]*uploadResult{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			os.RemoveAll(tempDir)
			writeError(w, http.StatusBadRequest, "读 multipart 失败: "+err.Error())
			return nil, nil, "", false
		}
		name := part.FormName()
		kind, isFile := artifactKindOf(name)
		if !isFile {
			value, err := io.ReadAll(io.LimitReader(part, 1<<20))
			part.Close()
			if err != nil {
				os.RemoveAll(tempDir)
				writeError(w, http.StatusBadRequest, "读字段 "+name+" 失败: "+err.Error())
				return nil, nil, "", false
			}
			meta[name] = string(value)
			continue
		}
		result, err := writeUploadedFile(tempDir, kind, part)
		part.Close()
		if err != nil {
			os.RemoveAll(tempDir)
			writeError(w, http.StatusBadRequest, "接收 "+name+" 失败: "+err.Error())
			return nil, nil, "", false
		}
		uploads[kind] = result
	}
	return meta, uploads, tempDir, true
}

// artifactsFromUpload 把上传结果整理成产物列表（顺序固定：installer / portable / update）。
func artifactsFromUpload(uploads map[domain.ArtifactKind]*uploadResult) []domain.Artifact {
	artifacts := make([]domain.Artifact, 0, len(uploads))
	for _, kind := range []domain.ArtifactKind{domain.KindInstaller, domain.KindPortable, domain.KindUpdate} {
		uploaded, ok := uploads[kind]
		if !ok {
			continue
		}
		artifacts = append(artifacts, domain.Artifact{
			Kind:     kind,
			Name:     uploaded.Name,
			Size:     uploaded.Size,
			SHA256:   uploaded.SHA256,
			FilePath: filepath.Join("releases", uploaded.Name),
		})
	}
	return artifacts
}

// checkDraft dry-run：与真上传同一条解析/校验路径，但**不落盘、不入库**。
// 上传页用它做实时预览与阻断项（所以预览里的 manifest.files 也是更新包 zip 里的真哈希）。
func (s *Server) checkDraft(w http.ResponseWriter, r *http.Request) {
	meta, uploads, tempDir, ok := s.parseUpload(w, r)
	if !ok {
		return
	}
	defer os.RemoveAll(tempDir)

	existing, err := s.store.ListReleases(true, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	version := strings.TrimSpace(meta["version"])
	channel, ok := channelOf(meta["channel"])
	if !ok {
		writeError(w, http.StatusBadRequest, "渠道只能是 stable 或 beta")
		return
	}
	artifacts := artifactsFromUpload(uploads)
	files, filesSource := s.manifestFiles(uploads, meta)
	declared := strings.TrimSpace(meta["manifestSha256"])
	if declared == "" {
		if update := findArtifact(artifacts, domain.KindUpdate); update != nil {
			declared = update.SHA256
		}
	}
	checks := domain.CheckReleaseDraft(domain.DraftCheckInput{
		Version:                 version,
		Channel:                 channel,
		Artifacts:               artifacts,
		ElectronVersion:         strings.TrimSpace(meta["electronVersion"]),
		PreviousElectronVersion:  s.previousElectronVersion(existing, channel),
		SHA256OfUpdate:          declared,
	}, existing)
	writeJSON(w, http.StatusOK, map[string]any{
		"checks":      checks,
		"canPublish":  domain.CanPublish(checks),
		"manifest":    domain.ManifestOf(version, findArtifact(artifacts, domain.KindUpdate), files),
		"filesSource": filesSource,
	})
}

// previousElectronVersion 「上一版」的 Electron 版本 = 同渠道最新已发布版本（不含自己）。
func (s *Server) previousElectronVersion(existing []domain.Release, channel domain.Channel) *string {
	latest := domain.LatestPublished(existing, channel)
	if latest == nil {
		return nil
	}
	return &latest.ElectronVersion
}

func (s *Server) uploadRelease(w http.ResponseWriter, r *http.Request) {
	meta, uploads, tempDir, ok := s.parseUpload(w, r)
	if !ok {
		return
	}
	defer os.RemoveAll(tempDir)

	existing, err := s.store.ListReleases(true, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	version := strings.TrimSpace(meta["version"])
	channel, ok := channelOf(meta["channel"])
	if !ok {
		writeError(w, http.StatusBadRequest, "渠道只能是 stable 或 beta")
		return
	}
	artifacts := artifactsFromUpload(uploads)

	declared := strings.TrimSpace(meta["manifestSha256"])
	if declared == "" {
		if update := findArtifact(artifacts, domain.KindUpdate); update != nil {
			declared = update.SHA256
		}
	}
	checks := domain.CheckReleaseDraft(domain.DraftCheckInput{
		Version:                 version,
		Channel:                 channel,
		Artifacts:               artifacts,
		ElectronVersion:         strings.TrimSpace(meta["electronVersion"]),
		PreviousElectronVersion:  s.previousElectronVersion(existing, channel),
		SHA256OfUpdate:          declared,
	}, existing)
	if !domain.CanPublish(checks) {
		writeErrorChecks(w, http.StatusBadRequest, "发布前校验未通过，已拒绝入库", checks)
		return
	}

	files, filesSource := s.manifestFiles(uploads, meta)
	release := domain.Release{
		Version:         version,
		Channel:         channel,
		Status:          domain.StatusDraft,
		Notes:           splitLines(meta["notes"]),
		Artifacts:       artifacts,
		Manifest:        domain.ManifestOf(version, findArtifact(artifacts, domain.KindUpdate), files),
		MinVersion:      strings.TrimSpace(meta["minVersion"]),
		Required:        meta["required"] == "true",
		ElectronVersion: strings.TrimSpace(meta["electronVersion"]),
		Downloads:       0,
	}
	// 校验通过才落盘：把临时文件 rename 到 data/releases/（同盘 rename 是原子的）
	for i := range artifacts {
		target := filepath.Join(s.store.ReleasesDir(), artifacts[i].Name)
		if err := os.Rename(uploads[artifacts[i].Kind].TempPath, target); err != nil {
			writeError(w, http.StatusInternalServerError, "落盘失败: "+err.Error())
			return
		}
	}
	if err := s.store.UpsertRelease(release); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"release":     release,
		"checks":      checks,
		"filesSource": filesSource,
	})
}

func channelOf(raw string) (domain.Channel, bool) {
	channel := domain.Channel(strings.TrimSpace(raw))
	if channel != domain.ChannelStable && channel != domain.ChannelBeta {
		return "", false
	}
	return channel, true
}

// manifestFiles 取更新包 zip 内两个必需路径的哈希（清单齐备）；zip 读不出来或缺路径时，
// 回落到表单提供的哈希，并如实回报来源（filesSource），不假装是从 zip 里算的。
func (s *Server) manifestFiles(uploads map[domain.ArtifactKind]*uploadResult, meta map[string]string) (map[string]string, string) {
	if update, ok := uploads[domain.KindUpdate]; ok {
		if hashes, err := innerHashes(update.TempPath); err == nil {
			complete := true
			for _, path := range domain.RequiredUpdateFiles {
				if hashes[path] == "" {
					complete = false
				}
			}
			if complete {
				return hashes, "zip"
			}
		}
	}
	files := map[string]string{}
	for _, path := range domain.RequiredUpdateFiles {
		files[path] = strings.TrimSpace(meta[fieldNameOf(path)])
	}
	return files, "form"
}

func fieldNameOf(path string) string {
	if path == domain.RequiredUpdateFiles[0] {
		return "appAsarSha256"
	}
	return "binarySha256"
}

func writeErrorChecks(w http.ResponseWriter, status int, message string, checks []domain.PublishCheck) {
	writeJSON(w, status, map[string]any{"error": message, "checks": checks})
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	var status domain.ReleaseStatus
	switch body.Status {
	case "publish", "published":
		status = domain.StatusPublished
	case "revoke", "revoked":
		status = domain.StatusRevoked
	default:
		writeError(w, http.StatusBadRequest, "status 只能是 publish 或 revoke")
		return
	}

	release, err := s.store.GetRelease(version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有版本 "+version)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if status == domain.StatusPublished {
		existing, err := s.store.ListReleases(true, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		others := make([]domain.Release, 0, len(existing))
		for _, r := range existing {
			if r.Version != version {
				others = append(others, r)
			}
		}
		previous := s.previousElectronVersion(others, release.Channel)
		checks := domain.CheckReleaseDraft(domain.DraftCheckInput{
			Version:                release.Version,
			Channel:                release.Channel,
			Artifacts:              release.Artifacts,
			ElectronVersion:        release.ElectronVersion,
			PreviousElectronVersion: previous,
			SHA256OfUpdate:         release.Manifest.SHA256,
		}, others)
		if !domain.CanPublish(checks) {
			// publish 校验清单齐备：不齐备就不许发布
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":  "发布前校验未通过（清单不齐备）",
				"checks": checks,
			})
			return
		}
	}

	if err := s.store.SetStatus(version, status, time.Now().UTC().Format(time.RFC3339)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.store.GetRelease(version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

/** ---------- 管理写：更新日志 ---------- */

func (s *Server) createChangelog(w http.ResponseWriter, r *http.Request) {
	entry, ok := decodeChangelog(w, r)
	if !ok {
		return
	}
	created, err := s.store.AddChangelog(entry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateChangelog(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id 不是整数")
		return
	}
	entry, ok := decodeChangelog(w, r)
	if !ok {
		return
	}
	entry.ID = id
	updated, err := s.store.UpdateChangelog(entry)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有这条日志")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteChangelog(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id 不是整数")
		return
	}
	if err := s.store.DeleteChangelog(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有这条日志")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeChangelog(w http.ResponseWriter, r *http.Request) (domain.ChangelogEntry, bool) {
	var entry domain.ChangelogEntry
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&entry); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return domain.ChangelogEntry{}, false
	}
	entry.Version = strings.TrimSpace(entry.Version)
	entry.Text = strings.TrimSpace(entry.Text)
	entry.Date = strings.TrimSpace(entry.Date)
	switch entry.Kind {
	case domain.KindFeatures, domain.KindFixes, domain.KindBreaking, domain.KindDocs:
	default:
		writeError(w, http.StatusBadRequest, "分类只能是 features/fixes/breaking/docs")
		return domain.ChangelogEntry{}, false
	}
	if entry.Version == "" || entry.Text == "" || entry.Date == "" {
		writeError(w, http.StatusBadRequest, "版本 / 日期 / 文案都不能为空")
		return domain.ChangelogEntry{}, false
	}
	if _, err := time.Parse(time.RFC3339, entry.Date); err != nil {
		writeError(w, http.StatusBadRequest, "日期必须是 ISO 8601（UTC）")
		return domain.ChangelogEntry{}, false
	}
	return entry, true
}

/** ---------- 小工具 ---------- */

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func splitLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func findArtifact(list []domain.Artifact, kind domain.ArtifactKind) *domain.Artifact {
	for i := range list {
		if list[i].Kind == kind {
			return &list[i]
		}
	}
	return nil
}

// writeUploadedFile 流式落临时文件并算 sha256/size（90MB 的安装包不该整个读进内存）。
func writeUploadedFile(dir string, kind domain.ArtifactKind, part *multipart.Part) (*uploadResult, error) {
	name := filepath.Base(part.FileName())
	if name == "" || name == "." {
		return nil, fmt.Errorf("文件名缺失")
	}
	file, err := os.CreateTemp(dir, string(kind)+"-*.part")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), part)
	if err != nil {
		os.Remove(file.Name())
		return nil, err
	}
	return &uploadResult{
		Kind:     kind,
		Name:     name,
		Size:     size,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
		TempPath: file.Name(),
	}, nil
}

func artifactKindOf(field string) (domain.ArtifactKind, bool) {
	switch field {
	case "installer":
		return domain.KindInstaller, true
	case "portable":
		return domain.KindPortable, true
	case "update":
		return domain.KindUpdate, true
	default:
		return "", false
	}
}

// innerHashes 从更新包 zip 里取两个必需路径的真实哈希（清单齐备：zip 内路径必须是安装目录相对路径）。
func innerHashes(zipPath string) (map[string]string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	out := map[string]string{}
	for _, file := range reader.File {
		wanted := false
		for _, required := range domain.RequiredUpdateFiles {
			if file.Name == required {
				wanted = true
			}
		}
		if !wanted {
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
