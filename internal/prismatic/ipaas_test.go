package prismatic

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIpaasCache_SetGetInvalidateRoundTrip(t *testing.T) {
	match := IpaasMatch{ID: "abc123", Name: "Jira Cloud", VersionNumber: 4}
	if err := SetCachedIpaasMatch("qa", "jira-integration", match); err != nil {
		t.Fatalf("SetCachedIpaasMatch: %v", err)
	}

	got, ok := GetCachedIpaasMatch("qa", "jira-integration")
	if !ok {
		t.Fatal("GetCachedIpaasMatch: ok = false right after Set, want true")
	}
	if got != match {
		t.Errorf("GetCachedIpaasMatch = %+v, want %+v", got, match)
	}

	// A different env or dir must not collide.
	if _, ok := GetCachedIpaasMatch("prod", "jira-integration"); ok {
		t.Error("prod entry should not exist after only qa was set")
	}
	if _, ok := GetCachedIpaasMatch("qa", "other-integration"); ok {
		t.Error("other-integration entry should not exist")
	}

	if err := InvalidateCachedIpaasMatch("qa", "jira-integration"); err != nil {
		t.Fatalf("InvalidateCachedIpaasMatch: %v", err)
	}
	if _, ok := GetCachedIpaasMatch("qa", "jira-integration"); ok {
		t.Error("entry should be gone after Invalidate")
	}
}

func TestIpaasCache_InvalidateUnsetEntryIsNotAnError(t *testing.T) {
	if err := InvalidateCachedIpaasMatch("prod", "never-cached"); err != nil {
		t.Fatalf("InvalidateCachedIpaasMatch on unset entry: %v", err)
	}
}

func TestParseIntegrationsJSON(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"empty", "", 0},
		{"bare array", `[{"id":"a","name":"Jira Cloud","versionNumber":3}]`, 1},
		{"wrapped results", `{"results":[{"id":"b","name":"Confluence"}]}`, 1},
		{"wrapped integrations", `{"integrations":[{"id":"c","name":"Coda"}]}`, 1},
		{"string version number", `[{"id":"d","name":"X","versionNumber":"7"}]`, 1},
		{"skips entries missing id or name", `[{"id":"","name":"X"},{"id":"y","name":""}]`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIntegrationsJSON([]byte(tc.json))
			if err != nil {
				t.Fatalf("parseIntegrationsJSON(%q): %v", tc.json, err)
			}
			if len(got) != tc.want {
				t.Fatalf("len = %d, want %d (got %+v)", len(got), tc.want, got)
			}
		})
	}
}

func TestResolveIpaasID_NoTokenConfiguredReturnsSentinel(t *testing.T) {
	resetCredentials(t)
	_, err := ResolveIpaasID(context.Background(), "qa", []string{"Jira Cloud"})
	if err != ErrPrismTokenNotConfigured {
		t.Fatalf("err = %v, want ErrPrismTokenNotConfigured", err)
	}
}

// stubPrism writes a fake `prism` executable onto PATH that just echoes a
// canned JSON array, so ResolveIpaasID's scoring logic can be exercised
// without the real prism CLI or network access.
func stubPrism(t *testing.T, jsonOut string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub relies on a POSIX shell script shebang")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + jsonOut + "\nEOF\n"
	path := filepath.Join(dir, "prism")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub prism: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveIpaasID_ExactNameMatchWins(t *testing.T) {
	resetCredentials(t)
	if err := SetPrismToken("qa", "fake-refresh-token"); err != nil {
		t.Fatalf("SetPrismToken: %v", err)
	}
	stubPrism(t, `[{"id":"exact-1","name":"Jira Cloud","versionNumber":2},{"id":"other-1","name":"Jira Cloud Legacy"}]`)

	res, err := ResolveIpaasID(context.Background(), "qa", []string{"Jira Cloud"})
	if err != nil {
		t.Fatalf("ResolveIpaasID: %v", err)
	}
	if res.Match == nil {
		t.Fatalf("Match = nil, want exact match; options = %+v", res.Options)
	}
	if res.Match.ID != "exact-1" {
		t.Errorf("Match.ID = %q, want exact-1", res.Match.ID)
	}
}

func TestResolveIpaasID_AmbiguousReturnsOptionsNoMatch(t *testing.T) {
	resetCredentials(t)
	if err := SetPrismToken("qa", "fake-refresh-token"); err != nil {
		t.Fatalf("SetPrismToken: %v", err)
	}
	stubPrism(t, `[{"id":"1","name":"Jira Something"},{"id":"2","name":"Jira Other"}]`)

	res, err := ResolveIpaasID(context.Background(), "qa", []string{"Jira"})
	if err != nil {
		t.Fatalf("ResolveIpaasID: %v", err)
	}
	if res.Match != nil {
		t.Fatalf("Match = %+v, want nil for an ambiguous substring hit", res.Match)
	}
	if len(res.Options) != 2 {
		t.Fatalf("len(Options) = %d, want 2", len(res.Options))
	}
}

func TestResolveIpaasID_NoResultsReturnsEmptyOptionsNoError(t *testing.T) {
	resetCredentials(t)
	if err := SetPrismToken("qa", "fake-refresh-token"); err != nil {
		t.Fatalf("SetPrismToken: %v", err)
	}
	stubPrism(t, `[]`)

	res, err := ResolveIpaasID(context.Background(), "qa", []string{"Nonexistent Thing"})
	if err != nil {
		t.Fatalf("ResolveIpaasID: %v", err)
	}
	if res.Match != nil || len(res.Options) != 0 {
		t.Fatalf("res = %+v, want empty", res)
	}
}

func TestNameCandidates_HumanizesDirAndStripsIntegrationSuffix(t *testing.T) {
	info := SourceDefInfo{SourceName: "Jira Cloud"}
	got := NameCandidates(info, "jira-integration")
	if len(got) != 2 || got[0] != "Jira Cloud" || got[1] != "jira" {
		t.Fatalf("NameCandidates = %+v, want [Jira Cloud, jira]", got)
	}
}
