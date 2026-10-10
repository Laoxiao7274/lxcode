// Package store 是站点的持久化层：SQLite（modernc.org/sqlite，WAL）单文件 data/site.db，
// 产物文件落在 data/releases/。
//
// 为什么用 SQLite 单文件：站点是单机部署的小服务，没有 DBA 也没有集群；
// 一个文件 + WAL 就能拿到事务与并发读，备份 = 复制文件。
//
// 表结构：releases（版本/渠道/状态/发布时间/下载计数/min_version/required/
// electron_version/notes/清单字段）+ artifacts（kind/name/size/sha256/文件路径，
// 每个版本最多三条）+ changelog_entries（版本/分类/文案/日期）+ meta（seed 标记等）。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/moyunteng/lxcode-site-backend/internal/domain"
)

// ErrNotFound 记录不存在（API 层据此返回 404）。
var ErrNotFound = errors.New("记录不存在")

type Store struct {
	db          *sql.DB
	dataDir     string
	releasesDir string
}

// Open 打开（必要时创建）数据目录与库文件，并建表。
func Open(dataDir string) (*Store, error) {
	releasesDir := filepath.Join(dataDir, "releases")
	if err := os.MkdirAll(releasesDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(dataDir, "site.db")) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	s := &Store{db: db, dataDir: dataDir, releasesDir: releasesDir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DataDir 数据根目录（API 层拼产物路径要用）。
func (s *Store) DataDir() string { return s.dataDir }

// ReleasesDir 产物目录（data/releases）。
func (s *Store) ReleasesDir() string { return s.releasesDir }

const schema = `
CREATE TABLE IF NOT EXISTS releases (
  version          TEXT PRIMARY KEY,
  channel          TEXT NOT NULL,
  status           TEXT NOT NULL,
  published_at     TEXT,
  min_version      TEXT NOT NULL,
  required         INTEGER NOT NULL,
  electron_version TEXT NOT NULL,
  downloads        INTEGER NOT NULL DEFAULT 0,
  notes            TEXT NOT NULL,
  manifest_url     TEXT NOT NULL,
  manifest_sha256  TEXT NOT NULL,
  manifest_size    INTEGER NOT NULL,
  manifest_files   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS artifacts (
  version   TEXT NOT NULL,
  kind      TEXT NOT NULL,
  name      TEXT NOT NULL,
  size      INTEGER NOT NULL,
  sha256    TEXT NOT NULL,
  file_path TEXT NOT NULL,
  PRIMARY KEY (version, kind)
);
CREATE TABLE IF NOT EXISTS changelog_entries (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  version TEXT NOT NULL,
  date    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  text    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	return nil
}

// ---------- releases ----------

type releaseRow struct {
	version, channel, status, minVersion, electronVersion string
	publishedAt                                          sql.NullString
	required                                             bool
	downloads                                            int64
	notes                                                string
	manifestURL, manifestSHA256                           string
	manifestSize                                         int64
	manifestFiles                                        string
}

// ListReleases 列版本。includeDrafts=false 时草稿不出现（公开接口）；
// includeRevoked=true 时连已撤回一起给（下载页的历史版本要用，撤回版保留历史）。
func (s *Store) ListReleases(includeDrafts, includeRevoked bool) ([]domain.Release, error) {
	rows, err := s.db.Query(`SELECT version, channel, status, published_at, min_version, required,
		electron_version, downloads, notes, manifest_url, manifest_sha256, manifest_size, manifest_files
		FROM releases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Release
	for rows.Next() {
		var r releaseRow
		if err := rows.Scan(&r.version, &r.channel, &r.status, &r.publishedAt, &r.minVersion,
			&r.required, &r.electronVersion, &r.downloads, &r.notes, &r.manifestURL,
			&r.manifestSHA256, &r.manifestSize, &r.manifestFiles); err != nil {
			return nil, err
		}
		release, err := s.assemble(r)
		if err != nil {
			return nil, err
		}
		if !includeDrafts && release.Status == domain.StatusDraft {
			continue
		}
		if !includeRevoked && release.Status == domain.StatusRevoked {
			continue
		}
		out = append(out, release)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return domain.SortByVersionDesc(out), nil
}

// GetRelease 取单个版本（含草稿与已撤回；调用方自己决定要不要对外）。
func (s *Store) GetRelease(version string) (*domain.Release, error) {
	var r releaseRow
	err := s.db.QueryRow(`SELECT version, channel, status, published_at, min_version, required,
		electron_version, downloads, notes, manifest_url, manifest_sha256, manifest_size, manifest_files
		FROM releases WHERE version = ?`, version).Scan(&r.version, &r.channel, &r.status,
		&r.publishedAt, &r.minVersion, &r.required, &r.electronVersion, &r.downloads,
		&r.notes, &r.manifestURL, &r.manifestSHA256, &r.manifestSize, &r.manifestFiles)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	release, err := s.assemble(r)
	if err != nil {
		return nil, err
	}
	return &release, nil
}

// LatestPublished 最新已发布版本；没有则 ErrNotFound。
func (s *Store) LatestPublished(channel domain.Channel) (*domain.Release, error) {
	all, err := s.ListReleases(false, false)
	if err != nil {
		return nil, err
	}
	latest := domain.LatestPublished(all, channel)
	if latest == nil {
		return nil, ErrNotFound
	}
	return latest, nil
}

func (s *Store) assemble(r releaseRow) (domain.Release, error) {
	var notes []string
	if err := json.Unmarshal([]byte(r.notes), &notes); err != nil {
		return domain.Release{}, fmt.Errorf("%s 的 notes 不是合法 JSON: %w", r.version, err)
	}
	var files map[string]string
	if err := json.Unmarshal([]byte(r.manifestFiles), &files); err != nil {
		return domain.Release{}, fmt.Errorf("%s 的 manifest.files 不是合法 JSON: %w", r.version, err)
	}
	artifacts, err := s.artifactsOf(r.version)
	if err != nil {
		return domain.Release{}, err
	}
	release := domain.Release{
		Version:         r.version,
		Channel:         domain.Channel(r.channel),
		Status:          domain.ReleaseStatus(r.status),
		Notes:           notes,
		Artifacts:       artifacts,
		MinVersion:      r.minVersion,
		Required:        r.required,
		ElectronVersion: r.electronVersion,
		Downloads:       r.downloads,
		Manifest: domain.Manifest{
			Version: r.version,
			URL:     r.manifestURL,
			SHA256:  r.manifestSHA256,
			Size:    r.manifestSize,
			Files:   files,
		},
	}
	if r.publishedAt.Valid {
		published := r.publishedAt.String
		release.PublishedAt = &published
	}
	return release, nil
}

func (s *Store) artifactsOf(version string) ([]domain.Artifact, error) {
	rows, err := s.db.Query(`SELECT kind, name, size, sha256, file_path FROM artifacts
		WHERE version = ? ORDER BY CASE kind WHEN 'installer' THEN 0 WHEN 'portable' THEN 1 ELSE 2 END`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Artifact
	for rows.Next() {
		var a domain.Artifact
		if err := rows.Scan(&a.Kind, &a.Name, &a.Size, &a.SHA256, &a.FilePath); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertRelease 写一个版本（含产物），整体替换该版本的产物集合。
func (s *Store) UpsertRelease(r domain.Release) error {
	notes, err := json.Marshal(r.Notes)
	if err != nil {
		return err
	}
	files, err := json.Marshal(r.Manifest.Files)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var publishedAt any
	if r.PublishedAt != nil {
		publishedAt = *r.PublishedAt
	}
	if _, err := tx.Exec(`INSERT INTO releases (version, channel, status, published_at, min_version,
		required, electron_version, downloads, notes, manifest_url, manifest_sha256, manifest_size, manifest_files)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(version) DO UPDATE SET channel=excluded.channel, status=excluded.status,
		published_at=excluded.published_at, min_version=excluded.min_version, required=excluded.required,
		electron_version=excluded.electron_version, notes=excluded.notes, manifest_url=excluded.manifest_url,
		manifest_sha256=excluded.manifest_sha256, manifest_size=excluded.manifest_size,
		manifest_files=excluded.manifest_files`,
		r.Version, string(r.Channel), string(r.Status), publishedAt, r.MinVersion, r.Required,
		r.ElectronVersion, r.Downloads, string(notes), r.Manifest.URL, r.Manifest.SHA256,
		r.Manifest.Size, string(files)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM artifacts WHERE version = ?`, r.Version); err != nil {
		return err
	}
	for _, a := range r.Artifacts {
		if _, err := tx.Exec(`INSERT INTO artifacts (version, kind, name, size, sha256, file_path)
			VALUES (?, ?, ?, ?, ?, ?)`, r.Version, string(a.Kind), a.Name, a.Size, a.SHA256, a.FilePath); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetStatus 改状态。发布 = 进更新链（草稿没有发布时间，这时补上）；
// 撤回 = 从更新链摘掉但保留 published_at（历史事实不改写）。
func (s *Store) SetStatus(version string, status domain.ReleaseStatus, now string) error {
	var publishedAt sql.NullString
	if err := s.db.QueryRow(`SELECT published_at FROM releases WHERE version = ?`, version).
		Scan(&publishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var value any
	switch {
	case status == domain.StatusPublished && !publishedAt.Valid:
		value = now // 草稿发布：补发布时间
	case publishedAt.Valid:
		value = publishedAt.String // 撤回：保留历史发布时间
	}
	res, err := s.db.Exec(`UPDATE releases SET status = ?, published_at = ? WHERE version = ?`,
		string(status), value, version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- changelog ----------

// ListChangelog 列日志。includeDrafts=false 时，草稿版本的条目不出现在公开接口。
func (s *Store) ListChangelog(includeDrafts bool) ([]domain.ChangelogEntry, error) {
	query := `SELECT c.id, c.version, c.date, c.kind, c.text FROM changelog_entries c`
	if !includeDrafts {
		query += ` JOIN releases r ON r.version = c.version WHERE r.status != 'draft'`
	}
	query += ` ORDER BY c.date DESC, c.id DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChangelogEntry{}
	for rows.Next() {
		var e domain.ChangelogEntry
		if err := rows.Scan(&e.ID, &e.Version, &e.Date, &e.Kind, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AddChangelog(e domain.ChangelogEntry) (domain.ChangelogEntry, error) {
	res, err := s.db.Exec(`INSERT INTO changelog_entries (version, date, kind, text) VALUES (?, ?, ?, ?)`,
		e.Version, e.Date, string(e.Kind), e.Text)
	if err != nil {
		return domain.ChangelogEntry{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.ChangelogEntry{}, err
	}
	e.ID = id
	return e, nil
}

func (s *Store) UpdateChangelog(e domain.ChangelogEntry) (domain.ChangelogEntry, error) {
	res, err := s.db.Exec(`UPDATE changelog_entries SET version = ?, date = ?, kind = ?, text = ? WHERE id = ?`,
		e.Version, e.Date, string(e.Kind), e.Text, e.ID)
	if err != nil {
		return domain.ChangelogEntry{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ChangelogEntry{}, ErrNotFound
	}
	return e, nil
}

func (s *Store) DeleteChangelog(id int64) error {
	res, err := s.db.Exec(`DELETE FROM changelog_entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- meta ----------

func (s *Store) Meta(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ReleaseOfArtifact 按产物文件名找它所属的版本（静态服务要用它判状态：撤回/草稿 → 404）。
func (s *Store) ReleaseOfArtifact(name string) (*domain.Release, error) {
	var version string
	err := s.db.QueryRow(`SELECT version FROM artifacts WHERE name = ?`, name).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetRelease(version)
}

// IncrementDownloads 下载计数 +1（产物被取走一次算一次；计数器失败不该挡下载）。
func (s *Store) IncrementDownloads(version string) error {
	_, err := s.db.Exec(`UPDATE releases SET downloads = downloads + 1 WHERE version = ?`, version)
	return err
}

// Reset 清空库与产物目录（种子导入用；**幂等**：重复 --seed 结果一致）。
func (s *Store) Reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"releases", "artifacts", "changelog_entries", "meta"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.releasesDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(s.releasesDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
