package domain

import "testing"

func TestNaming(t *testing.T) {
	cases := []struct{ got, want string }{
		{InstallerName("0.1.0"), "Lxcode Setup 0.1.0.exe"},
		{PortableName("0.1.0"), "Lxcode-0.1.0-win-x64.zip"},
		{UpdatePackageName("0.1.0"), "update-0.1.0.zip"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("命名不符：got %q want %q", c.got, c.want)
		}
	}
	if len(RequiredUpdateFiles) != 2 ||
		RequiredUpdateFiles[0] != "resources/app.asar" ||
		RequiredUpdateFiles[1] != "resources/bin/lxcode.exe" {
		t.Errorf("更新包内路径口径变了：%v", RequiredUpdateFiles)
	}
}

func TestIsValidVersion(t *testing.T) {
	valid := []string{"0.1.0", "1.2.3", "10.0.99"}
	invalid := []string{"", "1.0", "1.0.0.0", "1.0.x", "v1.0.0", "1..0"}
	for _, v := range valid {
		if !IsValidVersion(v) {
			t.Errorf("%q 应该合法", v)
		}
	}
	for _, v := range invalid {
		if IsValidVersion(v) {
			t.Errorf("%q 应该不合法", v)
		}
	}
}

func TestParseVersionFromName(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		found bool
	}{
		{"Lxcode Setup 0.0.3.exe", "0.0.3", true},
		{"update-0.1.0.zip", "0.1.0", true},
		{"Lxcode-0.0.2-win-x64.zip", "0.0.2", true},
		{"setup.exe", "", false},
	}
	for _, c := range cases {
		got, found := ParseVersionFromName(c.name)
		if got != c.want || found != c.found {
			t.Errorf("%q：got (%q,%v) want (%q,%v)", c.name, got, found, c.want, c.found)
		}
	}
}

func TestCompareAndSort(t *testing.T) {
	if CompareVersions("0.1.0", "0.0.9") <= 0 {
		t.Error("0.1.0 应大于 0.0.9")
	}
	if CompareVersions("0.1.0", "0.1.0") != 0 {
		t.Error("同版本应相等")
	}
	if CompareVersions("1.9.9", "1.9.10") >= 0 {
		t.Error("1.9.9 应小于 1.9.10")
	}
	sorted := SortByVersionDesc([]Release{
		{Version: "0.0.1"}, {Version: "0.2.0"}, {Version: "0.1.0"},
	})
	want := []string{"0.2.0", "0.1.0", "0.0.1"}
	for i, r := range sorted {
		if r.Version != want[i] {
			t.Errorf("排序第 %d 位：got %s want %s", i, r.Version, want[i])
		}
	}
}

func TestLatestPublished(t *testing.T) {
	list := []Release{
		{Version: "0.2.0", Channel: ChannelBeta, Status: StatusDraft},
		{Version: "0.1.0", Channel: ChannelStable, Status: StatusPublished},
		{Version: "0.0.3", Channel: ChannelStable, Status: StatusPublished},
		{Version: "0.0.2", Channel: ChannelStable, Status: StatusRevoked},
	}
	if got := LatestPublished(list, ChannelStable); got == nil || got.Version != "0.1.0" {
		t.Errorf("最新 stable 应为 0.1.0，got %v", got)
	}
	if got := LatestPublished(list, ChannelBeta); got != nil {
		t.Errorf("beta 只有草稿，不该进更新链，got %v", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		got  int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024 * 12, "12.0 MB"},
		{150 * 1024 * 1024, "150 MB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.got); got != c.want {
			t.Errorf("FormatBytes(%d)：got %q want %q", c.got, got, c.want)
		}
	}
}

func draft(version string, artifacts []Artifact, sha string) DraftCheckInput {
	previous := "40.10.2"
	return DraftCheckInput{
		Version:                version,
		Channel:                ChannelStable,
		Artifacts:              artifacts,
		ElectronVersion:        "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate:         sha,
	}
}

func goodArtifacts(version, sha string) []Artifact {
	return []Artifact{
		{Kind: KindInstaller, Name: InstallerName(version), Size: 1024, SHA256: sha},
		{Kind: KindPortable, Name: PortableName(version), Size: 1024, SHA256: sha},
		{Kind: KindUpdate, Name: UpdatePackageName(version), Size: 2048, SHA256: sha},
	}
}

func labels(checks []PublishCheck) []string {
	out := []string{}
	for _, c := range checks {
		if c.Level == LevelFail {
			out = append(out, c.Label)
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestCheckReleaseDraftHappyPath(t *testing.T) {
	sha := "aa"
	checks := CheckReleaseDraft(draft("0.2.0", goodArtifacts("0.2.0", sha), sha), nil)
	if !CanPublish(checks) {
		t.Fatalf("齐备的草稿应可发布，阻断项：%v", labels(checks))
	}
}

func TestCheckReleaseDraftBlockers(t *testing.T) {
	sha := "aa"
	existing := []Release{{Version: "0.1.0", Status: StatusPublished}}

	cases := []struct {
		name  string
		draft DraftCheckInput
		want  string
	}{
		{"版本号格式", draft("0.2", goodArtifacts("0.2", sha), sha), "版本号格式"},
		{"版本占用", draft("0.1.0", goodArtifacts("0.1.0", sha), sha), "版本号唯一性"},
		{"缺安装包", draft("0.2.0", goodArtifacts("0.2.0", sha)[1:], sha), "安装包"},
		{"命名不符", draft("0.2.0", []Artifact{
			{Kind: KindInstaller, Name: InstallerName("0.2.0"), Size: 1, SHA256: sha},
			{Kind: KindUpdate, Name: "update-0.1.9.zip", Size: 1, SHA256: sha},
		}, sha), "更新包命名"},
		{"哈希不符", draft("0.2.0", goodArtifacts("0.2.0", sha), "bb"), "更新包哈希"},
		{"缺更新包", draft("0.2.0", []Artifact{
			{Kind: KindInstaller, Name: InstallerName("0.2.0"), Size: 1, SHA256: sha},
		}, sha), "更新包"},
	}
	for _, c := range cases {
		checks := CheckReleaseDraft(c.draft, existing)
		if CanPublish(checks) {
			t.Errorf("%s：应该被拦下", c.name)
			continue
		}
		if !has(labels(checks), c.want) {
			t.Errorf("%s：缺少阻断项 %q，实际 %v", c.name, c.want, labels(checks))
		}
	}
}

func TestCheckReleaseDraftWarnOnly(t *testing.T) {
	sha := "aa"
	// 缺免安装版 + Electron 换代：只是提醒，不该阻断发布
	previous := "39.0.0"
	checks := CheckReleaseDraft(DraftCheckInput{
		Version:         "0.2.0",
		Channel:         ChannelStable,
		Artifacts:       goodArtifacts("0.2.0", sha)[:1],
		ElectronVersion: "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate: sha,
	}, nil)
	// 上面的 Artifacts 只有安装包，所以会同时缺更新包（阻断）；这里改成只缺免安装版
	artifacts := []Artifact{
		{Kind: KindInstaller, Name: InstallerName("0.2.0"), Size: 1, SHA256: sha},
		{Kind: KindUpdate, Name: UpdatePackageName("0.2.0"), Size: 1, SHA256: sha},
	}
	checks = CheckReleaseDraft(DraftCheckInput{
		Version:         "0.2.0",
		Channel:         ChannelStable,
		Artifacts:       artifacts,
		ElectronVersion: "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate:  sha,
	}, nil)
	if !CanPublish(checks) {
		t.Fatalf("只缺免安装版不该阻断，实际阻断项：%v", labels(checks))
	}
	wantWarn := map[string]bool{"免安装版": false, "Electron 版本变化": false}
	for _, c := range checks {
		if c.Level == LevelWarn {
			if _, ok := wantWarn[c.Label]; ok {
				wantWarn[c.Label] = true
			}
		}
	}
	for label, found := range wantWarn {
		if !found {
			t.Errorf("缺少提醒项 %q", label)
		}
	}
}

func TestManifestOf(t *testing.T) {
	update := Artifact{Kind: KindUpdate, Name: UpdatePackageName("0.1.0"), Size: 2048, SHA256: "cc"}
	files := map[string]string{
		"resources/app.asar":     "aa",
		"resources/bin/lxcode.exe": "bb",
	}
	m := ManifestOf("0.1.0", &update, files)
	// url 相对 manifest 所在目录必须指向产物路由（/releases/<file>）——
	// 裸文件名会解析到 SPA 的 index.html 回落（部署在 /site 前缀下必炸）
	if m.URL != "releases/"+UpdatePackageName("0.1.0") {
		t.Errorf("manifest.url 必须指向产物路由 releases/<file>，got %q", m.URL)
	}
	if m.SHA256 != update.SHA256 || m.Size != update.Size {
		t.Errorf("manifest 必须与更新包同哈希同体积：%+v", m)
	}
	if len(m.Files) != 2 || m.Files["resources/app.asar"] != "aa" {
		t.Errorf("manifest.files 应是两个安装目录相对路径：%+v", m.Files)
	}
}
