package domain

import "testing"

func TestNaming(t *testing.T) {
	cases := []struct{ got, want string }{
		{InstallerName("0.1.0"), "Lxcode Setup 0.1.0.exe"},
		{PortableName("0.1.0"), "Lxcode-0.1.0-win-x64.zip"},
		{UpdatePackageName("0.1.0"), "update-0.1.0.zip"},
		// mac（2026-12）：与 scripts/build.mjs --mac 产物名一致
		{MacUpdatePackageName("0.1.0"), "update-mac-0.1.0.zip"},
		{MacInstallerName("0.1.0", "arm64"), "Lxcode-0.1.0-arm64.dmg"},
		{MacInstallerName("0.1.0", "x64"), "Lxcode-0.1.0-x64.dmg"},
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
	// mac 更新包内路径：后端无 .exe（darwin 二进制叫 lxcode）
	if len(RequiredMacUpdateFiles) != 2 ||
		RequiredMacUpdateFiles[0] != "resources/app.asar" ||
		RequiredMacUpdateFiles[1] != "resources/bin/lxcode" {
		t.Errorf("mac 更新包内路径口径变了：%v", RequiredMacUpdateFiles)
	}
	if !IsMacInstallerName("0.1.0", "Lxcode-0.1.0-arm64.dmg") ||
		!IsMacInstallerName("0.1.0", "Lxcode-0.1.0-x64.dmg") ||
		IsMacInstallerName("0.1.0", "Lxcode-0.1.0.dmg") ||
		IsMacInstallerName("0.1.0", "Lxcode-0.1.0-arm64.zip") {
		t.Error("IsMacInstallerName 的匹配范围不对")
	}
	if kind, arch, ok := MacInstallerKindAndArch("macInstallerArm64"); !ok || kind != KindMacInstallerArm64 || arch != "arm64" {
		t.Errorf("macInstallerArm64 解析不对：%v %v %v", kind, arch, ok)
	}
	if _, _, ok := MacInstallerKindAndArch("installer"); ok {
		t.Error("Windows 字段不该被当成 mac dmg 字段")
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
		Version:                 version,
		Channel:                 ChannelStable,
		Artifacts:               artifacts,
		ElectronVersion:         "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate:          sha,
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
		Version:                 "0.2.0",
		Channel:                 ChannelStable,
		Artifacts:               goodArtifacts("0.2.0", sha)[:1],
		ElectronVersion:         "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate:          sha,
	}, nil)
	// 上面的 Artifacts 只有安装包，所以会同时缺更新包（阻断）；这里改成只缺免安装版
	artifacts := []Artifact{
		{Kind: KindInstaller, Name: InstallerName("0.2.0"), Size: 1, SHA256: sha},
		{Kind: KindUpdate, Name: UpdatePackageName("0.2.0"), Size: 1, SHA256: sha},
	}
	checks = CheckReleaseDraft(DraftCheckInput{
		Version:                 "0.2.0",
		Channel:                 ChannelStable,
		Artifacts:               artifacts,
		ElectronVersion:         "40.10.2",
		PreviousElectronVersion: &previous,
		SHA256OfUpdate:          sha,
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
		"resources/app.asar":       "aa",
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

func TestManifestOfMac(t *testing.T) {
	update := Artifact{Kind: KindMacUpdate, Name: MacUpdatePackageName("0.1.0"), Size: 4096, SHA256: "dd"}
	files := map[string]string{
		"resources/app.asar":   "aa",
		"resources/bin/lxcode": "bb",
	}
	m := ManifestOfMac("0.1.0", &update, files)
	// 与 Windows 同构：url 同为相对文件名（按 manifest 所在目录解析），指向产物路由
	if m.URL != "releases/"+MacUpdatePackageName("0.1.0") {
		t.Errorf("manifest-mac.url 必须指向产物路由 releases/<file>，got %q", m.URL)
	}
	if m.Version != "0.1.0" || m.SHA256 != "dd" || m.Size != 4096 {
		t.Errorf("manifest-mac 字段要与更新包一致：%+v", m)
	}
	if len(m.Files) != 2 || m.Files["resources/bin/lxcode"] != "bb" {
		t.Errorf("manifest-mac.files 应是 Contents 相对路径（无 .exe）：%+v", m.Files)
	}
}

// mac 校验段：SHA256OfMacUpdate 非空才启用；Windows 规则不受影响。
func TestCheckReleaseDraftMac(t *testing.T) {
	sha := "aa"
	base := goodArtifacts("0.2.0", sha)

	macGood := func() []Artifact {
		return append(append([]Artifact{}, base...),
			Artifact{Kind: KindMacUpdate, Name: MacUpdatePackageName("0.2.0"), Size: 2048, SHA256: sha},
			Artifact{Kind: KindMacInstallerArm64, Name: MacInstallerName("0.2.0", "arm64"), Size: 1, SHA256: sha},
			Artifact{Kind: KindMacInstallerX64, Name: MacInstallerName("0.2.0", "x64"), Size: 1, SHA256: sha},
		)
	}

	// 1. 不带 mac：行为与原来完全一致（不受 mac 字段引入影响）
	if checks := CheckReleaseDraft(draft("0.2.0", base, sha), nil); !CanPublish(checks) {
		t.Errorf("不带 mac 的齐备草稿应可发布，阻断项：%v", labels(checks))
	}
	// 2. 带 mac 且齐备：可发布
	if checks := CheckReleaseDraft(draft("0.2.0", macGood(), sha), nil); !CanPublish(checks) {
		t.Errorf("带 mac 的齐备草稿应可发布，阻断项：%v", labels(checks))
	}
	// draft() 的 SHA256OfUpdate 同时充当 mac 声明哈希；上面第 2 例的 mac 哈希确实被核对过：
	// 声明值与产物一致。下面逐条验阻断项。
	cases := []struct {
		name      string
		artifacts []Artifact
		macSHA    string
		want      string
	}{
		{"缺 mac 更新包", base, sha, "mac 更新包"},
		{"mac 更新包命名不符", append(append([]Artifact{}, base...),
			Artifact{Kind: KindMacUpdate, Name: "update-0.1.9.zip", Size: 1, SHA256: sha}), sha, "mac 更新包命名"},
		{"mac 哈希不符", append(append([]Artifact{}, base...),
			Artifact{Kind: KindMacUpdate, Name: MacUpdatePackageName("0.2.0"), Size: 1, SHA256: sha}), "bb", "mac 更新包哈希"},
	}
	for _, c := range cases {
		d := draft("0.2.0", c.artifacts, sha)
		d.SHA256OfMacUpdate = c.macSHA
		checks := CheckReleaseDraft(d, nil)
		if CanPublish(checks) || !has(labels(checks), c.want) {
			t.Errorf("%s：应阻断且含 %q，实际 %v", c.name, c.want, labels(checks))
		}
	}

	// 缺 dmg / 命名不符 = warn，不阻断
	d := draft("0.2.0", append(append([]Artifact{}, base...),
		Artifact{Kind: KindMacUpdate, Name: MacUpdatePackageName("0.2.0"), Size: 1, SHA256: sha}), sha)
	d.SHA256OfMacUpdate = sha
	checks := CheckReleaseDraft(d, nil)
	if !CanPublish(checks) {
		t.Errorf("缺 dmg 不该阻断：%v", labels(checks))
	}
	d2 := draft("0.2.0", append(append([]Artifact{}, base...),
		Artifact{Kind: KindMacUpdate, Name: MacUpdatePackageName("0.2.0"), Size: 1, SHA256: sha},
		Artifact{Kind: KindMacInstallerArm64, Name: "Lxcode.dmg", Size: 1, SHA256: sha}), sha)
	d2.SHA256OfMacUpdate = sha
	checks = CheckReleaseDraft(d2, nil)
	if !CanPublish(checks) {
		t.Errorf("dmg 命名不符只该 warn：%v", labels(checks))
	}
	found := false
	for _, c := range checks {
		if c.Level == LevelWarn && c.Label == "mac 安装包命名" {
			found = true
		}
	}
	if !found {
		t.Errorf("dmg 命名不符应有 warn 提醒：%+v", checks)
	}
}
