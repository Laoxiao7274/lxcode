// API 层测试：上传 → 发布 → 公开可见 → manifest 契约 → 撤回后下载 404。
//
// 为什么把这条链放在 API 层测：这是「后台显示发布成功、客户端却下不到」那类事故的唯一入口——
// 校验、落盘、状态流转、公开可见性分散在 store/api 两层，只有从 HTTP 打进去才能把整条链钉住。
// 正反例都在这里：缺安装包 / 命名不符 / 版本占用 / 撤回后下载 / 未配 token 全部要有断言。
package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode-site-backend/internal/domain"
	"github.com/moyunteng/lxcode-site-backend/internal/seed"
	"github.com/moyunteng/lxcode-site-backend/internal/store"
)

const testToken = "test-token"

type uploadFile struct {
	name    string
	content []byte
}

func newServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	st := newStore(t)
	if err := seed.Run(st, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("导入种子失败: %v", err)
	}
	return httptest.NewServer(New(st, Config{Token: token}, log.New(io.Discard, "", 0)).Handler())
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPublicReleasesAndLatest(t *testing.T) {
	srv := newServer(t, testToken)

	published := decode[[]domain.Release](t, mustGet(t, srv, "/api/releases", ""))
	got := []string{}
	for _, r := range published {
		got = append(got, r.Version)
	}
	if want := "0.1.0,0.0.3,0.0.1"; strings.Join(got, ",") != want {
		t.Errorf("公开版本列表 = %v，want %s（草稿与撤回都不该出现）", got, want)
	}

	withRevoked := decode[[]domain.Release](t, mustGet(t, srv, "/api/releases?include=revoked", ""))
	if len(withRevoked) != 4 {
		t.Errorf("include=revoked 应含 4 条（含撤回），got %d", len(withRevoked))
	}

	latest := decode[domain.Release](t, mustGet(t, srv, "/api/releases/latest", ""))
	if latest.Version != "0.1.0" {
		t.Errorf("最新 stable = %s，want 0.1.0", latest.Version)
	}

	entries := decode[[]domain.ChangelogEntry](t, mustGet(t, srv, "/api/changelog", ""))
	for _, e := range entries {
		if e.Version == "0.2.0" {
			t.Errorf("草稿版本的日志不该出现在公开接口")
		}
	}
	if len(entries) == 0 {
		t.Errorf("公开日志不该为空")
	}
}

func TestManifestContract(t *testing.T) {
	srv := newServer(t, testToken)
	var manifest domain.Manifest
	if err := json.Unmarshal(mustGet(t, srv, "/manifest.json", ""), &manifest); err != nil {
		t.Fatalf("manifest 不是合法 JSON: %v", err)
	}
	if manifest.Version != "0.1.0" {
		t.Errorf("manifest.version = %s，want 0.1.0", manifest.Version)
	}
	// url 相对 manifest 所在目录指向产物路由（/releases/<file>）——客户端按
	// manifest 所在目录解析，反代加前缀（/site）也不炸
	if manifest.URL != "releases/"+domain.UpdatePackageName("0.1.0") {
		t.Errorf("manifest.url = %s，want releases/%s", manifest.URL, domain.UpdatePackageName("0.1.0"))
	}
	if len(manifest.SHA256) != 64 || manifest.Size <= 0 {
		t.Errorf("manifest 哈希/体积不合法：size=%d sha=%q", manifest.Size, manifest.SHA256)
	}
	if len(manifest.Files) != len(domain.RequiredUpdateFiles) {
		t.Fatalf("manifest.files 键数 = %d，want %d", len(manifest.Files), len(domain.RequiredUpdateFiles))
	}

	// 关键：自己下载更新包再算一遍——manifest 的 sha256/size/files 必须与真文件一致。
	// url 就按客户端的真实解析方式用（相对 manifest 的根）。
	res, zipBody := get(t, srv, "/"+manifest.URL, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载更新包状态 %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("更新包 Content-Type = %q，want application/zip", ct)
	}
	if int64(len(zipBody)) != manifest.Size {
		t.Errorf("更新包体积 = %d，manifest.size = %d", len(zipBody), manifest.Size)
	}
	if sha256Hex(zipBody) != manifest.SHA256 {
		t.Errorf("更新包哈希与 manifest.sha256 不一致")
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBody), int64(len(zipBody)))
	if err != nil {
		t.Fatalf("更新包不是合法 zip: %v", err)
	}
	inner := map[string]string{}
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatalf("打开 zip 内 %s 失败: %v", file.Name, err)
		}
		content, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			t.Fatalf("读 zip 内 %s 失败: %v", file.Name, err)
		}
		inner[file.Name] = sha256Hex(content)
	}
	for _, path := range domain.RequiredUpdateFiles {
		if manifest.Files[path] != inner[path] {
			t.Errorf("%s 的 files 哈希不一致：manifest=%q 实际=%q", path, manifest.Files[path], inner[path])
		}
	}
}

func TestArtifactDownloadStatusRules(t *testing.T) {
	srv := newServer(t, testToken)
	cases := []struct {
		file string
		want int
	}{
		{"Lxcode Setup 0.1.0.exe", http.StatusOK},
		{"Lxcode-0.1.0-win-x64.zip", http.StatusOK},
		{"update-0.1.0.zip", http.StatusOK},
		{"Lxcode Setup 0.0.2.exe", http.StatusNotFound}, // 已撤回：不提供下载
		{"update-0.0.2.zip", http.StatusNotFound},        // 已撤回
		{"Lxcode Setup 0.2.0.exe", http.StatusNotFound},  // 草稿：没发布过
		{"no-such-file.exe", http.StatusNotFound},
		{"site.db", http.StatusNotFound}, // 不是产物
	}
	for _, c := range cases {
		res, _ := get(t, srv, "/releases/"+c.file, "")
		if res.StatusCode != c.want {
			t.Errorf("GET /releases/%s = %d，want %d", c.file, res.StatusCode, c.want)
		}
	}
	if res, _ := get(t, srv, "/releases/Lxcode%20Setup%200.1.0.exe", ""); res.StatusCode != http.StatusOK {
		t.Errorf("URL 转义过的文件名应也能下（前端就是拼原始文件名）：%d", res.StatusCode)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	srv := newServer(t, testToken)
	if res, _ := get(t, srv, "/api/admin/releases", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("无 token 应 401，got %d", res.StatusCode)
	}
	if res, _ := get(t, srv, "/api/admin/releases", "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("错 token 应 401，got %d", res.StatusCode)
	}
	all := decode[[]domain.Release](t, mustGet(t, srv, "/api/admin/releases", testToken))
	if len(all) != 5 {
		t.Errorf("管理列表应含草稿与撤回（5 条），got %d", len(all))
	}
}

func TestUploadPublishRevoke(t *testing.T) {
	srv := newServer(t, testToken)
	const version = "0.1.1"
	files := uploadFiles(t, version)
	updateSHA := sha256Hex(files["update"].content)

	res, body := postMultipart(t, srv, "/api/admin/releases", testToken, map[string]string{
		"version": version, "channel": "stable", "electronVersion": "40.10.2",
		"minVersion": "0.0.1", "required": "false", "notes": "第一条\n\n第二条",
	}, files)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("上传状态 %d，body=%s", res.StatusCode, string(body))
	}
	created := decode[struct {
		Release     domain.Release `json:"release"`
		FilesSource string         `json:"filesSource"`
	}](t, body)
	if created.Release.Status != domain.StatusDraft {
		t.Errorf("上传后状态 = %s，want draft", created.Release.Status)
	}
	if created.FilesSource != "zip" {
		t.Errorf("更新包是真 zip，filesSource 应为 zip，got %q", created.FilesSource)
	}
	if created.Release.Manifest.SHA256 != updateSHA {
		t.Errorf("入库的 manifest.sha256 = %s，want 上传 zip 的真哈希 %s",
			created.Release.Manifest.SHA256, updateSHA)
	}
	if len(created.Release.Notes) != 2 {
		t.Errorf("notes 应按行切开并丢掉空行，got %v", created.Release.Notes)
	}

	for _, r := range decode[[]domain.Release](t, mustGet(t, srv, "/api/releases", "")) {
		if r.Version == version {
			t.Fatalf("草稿不该出现在公开列表")
		}
	}

	res, body = sendJSON(t, http.MethodPost, srv, "/api/admin/releases/"+version+"/status", testToken,
		`{"status":"publish"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("发布状态 %d，body=%s", res.StatusCode, string(body))
	}
	published := decode[domain.Release](t, mustGet(t, srv, "/api/releases/latest", ""))
	if published.Version != version {
		t.Errorf("发布后 latest = %s，want %s", published.Version, version)
	}
	found := false
	for _, r := range decode[[]domain.Release](t, mustGet(t, srv, "/api/releases", "")) {
		if r.Version == version {
			found = true
		}
	}
	if !found {
		t.Errorf("发布后公开列表应出现 %s", version)
	}
	if res, _ := get(t, srv, "/releases/"+domain.UpdatePackageName(version), ""); res.StatusCode != http.StatusOK {
		t.Errorf("发布后更新包应 200，got %d", res.StatusCode)
	}

	if res, body = sendJSON(t, http.MethodPost, srv, "/api/admin/releases/"+version+"/status", testToken,
		`{"status":"revoke"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("撤回状态 %d，body=%s", res.StatusCode, string(body))
	}
	if res, _ := get(t, srv, "/releases/"+domain.UpdatePackageName(version), ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("撤回后更新包应 404，got %d", res.StatusCode)
	}
	revoked := decode[[]domain.Release](t, mustGet(t, srv, "/api/releases?include=revoked", ""))
	for _, r := range revoked {
		if r.Version == version {
			if r.Status != domain.StatusRevoked || r.PublishedAt == nil {
				t.Errorf("撤回应保留历史与发布时间：%+v", r)
			}
		}
	}
}

func TestUploadRejections(t *testing.T) {
	srv := newServer(t, testToken)
	fields := func(version string) map[string]string {
		return map[string]string{
			"version": version, "channel": "stable", "electronVersion": "40.10.2", "minVersion": "0.0.1",
		}
	}

	// 版本占用
	res, body := postMultipart(t, srv, "/api/admin/releases", testToken, fields("0.1.0"), uploadFiles(t, "0.1.0"))
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "版本号唯一性") {
		t.Errorf("版本占用应 400 + 版本号唯一性，got %d %s", res.StatusCode, string(body))
	}

	// 命名不符
	files := uploadFiles(t, "0.2.0")
	files["installer"] = uploadFile{name: "setup.exe", content: files["installer"].content}
	res, body = postMultipart(t, srv, "/api/admin/releases", testToken, fields("0.2.0"), files)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "安装包命名") {
		t.Errorf("命名不符应 400 + 安装包命名，got %d %s", res.StatusCode, string(body))
	}

	// 缺更新包
	files = uploadFiles(t, "0.2.0")
	delete(files, "update")
	res, body = postMultipart(t, srv, "/api/admin/releases", testToken, fields("0.2.0"), files)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "更新包") {
		t.Errorf("缺更新包应 400 + 更新包，got %d %s", res.StatusCode, string(body))
	}

	// 哈希不符：清单声明的更新包哈希与上传 zip 的真哈希不一致
	files = uploadFiles(t, "0.2.0")
	res, body = postMultipart(t, srv, "/api/admin/releases", testToken, map[string]string{
		"version": "0.2.0", "channel": "stable", "electronVersion": "40.10.2",
		"minVersion": "0.0.1", "manifestSha256": strings.Repeat("0", 64),
	}, files)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "更新包哈希") {
		t.Errorf("哈希不符应 400 + 更新包哈希，got %d %s", res.StatusCode, string(body))
	}
}

func TestPublishRejectsIncompleteDraft(t *testing.T) {
	// 直接往库里塞一个「缺更新包」的草稿（绕过上传校验），发布时必须被拦住
	st := newStore(t)
	if err := seed.Run(st, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
	if err := st.UpsertRelease(domain.Release{
		Version: "0.9.9", Channel: domain.ChannelStable, Status: domain.StatusDraft,
		Artifacts: []domain.Artifact{{
			Kind: domain.KindInstaller, Name: domain.InstallerName("0.9.9"), Size: 1, SHA256: "aa",
		}},
		Manifest: domain.Manifest{Version: "0.9.9", URL: domain.UpdatePackageName("0.9.9")},
		MinVersion: "0.0.1", ElectronVersion: "40.10.2",
	}); err != nil {
		t.Fatalf("塞草稿失败: %v", err)
	}
	srv := httptest.NewServer(New(st, Config{Token: testToken}, log.New(io.Discard, "", 0)).Handler())
	defer srv.Close()

	res, body := sendJSON(t, http.MethodPost, srv, "/api/admin/releases/0.9.9/status", testToken,
		`{"status":"publish"}`)
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), "更新包") {
		t.Errorf("发布缺清单的草稿应 409 + 阻断项，got %d %s", res.StatusCode, string(body))
	}
}

func TestWritesRejectedWhenTokenUnconfigured(t *testing.T) {
	srv := newServer(t, "")
	res, body := postMultipart(t, srv, "/api/admin/releases", "any",
		map[string]string{"version": "0.3.0", "channel": "stable"}, nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("未配 token 应 503（默认拒绝所有写），got %d %s", res.StatusCode, string(body))
	}
}

func TestCheckDraftDryRun(t *testing.T) {
	srv := newServer(t, testToken)
	// dry-run 与真上传同一条路径：带同一份三个文件，所以校验与预览都与真上传一致
	files := uploadFiles(t, "0.1.1")
	res, out := postMultipart(t, srv, "/api/admin/releases/check", testToken, map[string]string{
		"version": "0.1.1", "channel": "stable", "electronVersion": "40.10.2", "minVersion": "0.0.1",
	}, files)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("dry-run 状态 %d，body=%s", res.StatusCode, string(out))
	}
	result := decode[struct {
		CanPublish  bool                 `json:"canPublish"`
		Checks      []domain.PublishCheck `json:"checks"`
		Manifest    domain.Manifest      `json:"manifest"`
		FilesSource string               `json:"filesSource"`
	}](t, out)
	if !result.CanPublish {
		t.Errorf("齐备的 dry-run 应可发布：%+v", result.Checks)
	}
	if result.Manifest.URL != "releases/"+domain.UpdatePackageName("0.1.1") {
		t.Errorf("dry-run manifest.url = %s，want releases/%s", result.Manifest.URL, domain.UpdatePackageName("0.1.1"))
	}
	if result.FilesSource != "zip" {
		t.Errorf("dry-run 带了真 zip，filesSource 应为 zip，got %q", result.FilesSource)
	}
	// 预览里的 files 必须是 zip 内两个路径的真哈希
	zipBytes := files["update"].content
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("测试 zip 不合法: %v", err)
	}
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", file.Name, err)
		}
		content, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			t.Fatalf("读 %s 失败: %v", file.Name, err)
		}
		if result.Manifest.Files[file.Name] != sha256Hex(content) {
			t.Errorf("%s 预览哈希不对：%q vs %q", file.Name,
				result.Manifest.Files[file.Name], sha256Hex(content))
		}
	}
	// dry-run 不落盘、不入库
	releases := decode[[]domain.Release](t, mustGet(t, srv, "/api/releases", ""))
	for _, r := range releases {
		if r.Version == "0.1.1" {
			t.Errorf("dry-run 不该入库")
		}
	}
}

func TestChangelogCRUD(t *testing.T) {
	srv := newServer(t, testToken)
	before := decode[[]domain.ChangelogEntry](t, mustGet(t, srv, "/api/changelog", ""))

	res, body := sendJSON(t, http.MethodPost, srv, "/api/admin/changelog", testToken,
		`{"version":"0.1.0","date":"2026-10-09T00:00:00Z","kind":"fixes","text":"新增一条测试日志"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("新增日志状态 %d，body=%s", res.StatusCode, string(body))
	}
	created := decode[domain.ChangelogEntry](t, body)

	res, body = sendJSON(t, http.MethodPost, srv, "/api/admin/changelog", testToken,
		`{"version":"0.1.0","date":"2026-10-09T00:00:00Z","kind":"nope","text":"分类不合法"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("非法分类应 400，got %d %s", res.StatusCode, string(body))
	}

	after := decode[[]domain.ChangelogEntry](t, mustGet(t, srv, "/api/changelog", ""))
	if len(after) != len(before)+1 {
		t.Errorf("公开日志条数应 +1，got %d → %d", len(before), len(after))
	}

	res, body = sendJSON(t, http.MethodPut, srv, "/api/admin/changelog/"+strconv.FormatInt(created.ID, 10),
		testToken, `{"version":"0.1.0","date":"2026-10-10T00:00:00Z","kind":"docs","text":"改过的日志"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("改日志状态 %d，body=%s", res.StatusCode, string(body))
	}
	updated := decode[domain.ChangelogEntry](t, body)
	if updated.Text != "改过的日志" || updated.Kind != domain.KindDocs {
		t.Errorf("改日志结果不对：%+v", updated)
	}

	res, _ = sendJSON(t, http.MethodDelete, srv, "/api/admin/changelog/"+strconv.FormatInt(created.ID, 10),
		testToken, "")
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("删日志状态 %d", res.StatusCode)
	}
	final := decode[[]domain.ChangelogEntry](t, mustGet(t, srv, "/api/changelog", ""))
	if len(final) != len(before) {
		t.Errorf("删完应回到原条数：%d → %d", len(before), len(final))
	}

	if res, _ = sendJSON(t, http.MethodPut, srv, "/api/admin/changelog/999999", testToken,
		`{"version":"0.1.0","date":"2026-10-10T00:00:00Z","kind":"docs","text":"不存在"}`); res.StatusCode != http.StatusNotFound {
		t.Errorf("改不存在的日志应 404，got %d", res.StatusCode)
	}
}

/** ---------- 测试小工具 ---------- */

func uploadFiles(t *testing.T, version string) map[string]uploadFile {
	t.Helper()
	return map[string]uploadFile{
		"installer": {name: domain.InstallerName(version), content: bytes.Repeat([]byte("i"), 1024)},
		"portable":  {name: domain.PortableName(version), content: bytes.Repeat([]byte("p"), 1024)},
		"update":    {name: domain.UpdatePackageName(version), content: makeUpdateZip(t, version)},
	}
}

// makeUpdateZip 造一个**真 zip**（只含契约要求的两个路径），好让后端能算出内部文件哈希。
// 时间戳固定成 0：同样的输入得到同样的字节 → 哈希可复现（否则 zip 头里带当前时间，每次都不一样）。
func makeUpdateZip(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, path := range domain.RequiredUpdateFiles {
		header := &zip.FileHeader{Name: path, Method: zip.Deflate}
		header.Modified = time.Unix(0, 0).UTC()
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("造 zip 条目失败: %v", err)
		}
		if _, err := part.Write([]byte("dummy " + version + " " + path)); err != nil {
			t.Fatalf("写 zip 条目失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关 zip 失败: %v", err)
	}
	return buf.Bytes()
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func get(t *testing.T, srv *httptest.Server, path, token string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("造请求失败: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	return res, body
}

func mustGet(t *testing.T, srv *httptest.Server, path, token string) []byte {
	t.Helper()
	res, body := get(t, srv, path, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态 %d，body=%s", path, res.StatusCode, string(body))
	}
	return body
}

func sendJSON(t *testing.T, method string, srv *httptest.Server, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	return res, out
}

func postMultipart(t *testing.T, srv *httptest.Server, path, token string,
	fields map[string]string, files map[string]uploadFile) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("写字段失败: %v", err)
		}
	}
	for field, file := range files {
		part, err := writer.CreateFormFile(field, file.name)
		if err != nil {
			t.Fatalf("写文件字段失败: %v", err)
		}
		if _, err := part.Write(file.content); err != nil {
			t.Fatalf("写文件内容失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关 multipart 失败: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	return res, out
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（%.200s）", err, string(body))
	}
	return out
}
