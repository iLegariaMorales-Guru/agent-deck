package prismatic

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixtureIntegration creates a minimal directory shape that Detect
// recognizes: <root>/<dir>/package.json + src/ (+ optional flows/*.ts).
func writeFixtureIntegration(t *testing.T, root, dir, pkgJSON string, flowFiles ...string) {
	t.Helper()
	base := filepath.Join(root, dir)
	if err := os.MkdirAll(filepath.Join(base, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if len(flowFiles) > 0 {
		flowsDir := filepath.Join(base, "src", "flows")
		if err := os.MkdirAll(flowsDir, 0o755); err != nil {
			t.Fatalf("mkdir flows: %v", err)
		}
		for _, f := range flowFiles {
			if err := os.WriteFile(filepath.Join(flowsDir, f), []byte("export {}"), 0o644); err != nil {
				t.Fatalf("write flow file: %v", err)
			}
		}
	}
}

func TestDetect_ClassifiesCNIComponentSharedAndOther(t *testing.T) {
	root := t.TempDir()

	writeFixtureIntegration(t, root, "jira-integration",
		`{"name":"jira-integration","version":"1.2.0","description":"Jira sync","dependencies":{"@prismatic-io/spectral":"^9.0.0"}}`,
		"createSource.ts", "scheduledSync.ts", "index.ts")
	writeFixtureIntegration(t, root, "http",
		`{"name":"http","dependencies":{"@prismatic-io/spectral":"^9.0.0"}}`) // no flows -> component
	writeFixtureIntegration(t, root, "guru-manifest",
		`{"name":"guru-manifest"}`) // no spectral dep at all, but special-cased dir name
	writeFixtureIntegration(t, root, "plain-lib",
		`{"name":"plain-lib"}`) // no spectral dep, not guru-manifest -> other

	// A directory that must be ignored outright even though it happens to
	// contain a package.json + src/ (build output / vendor-shaped noise).
	writeFixtureIntegration(t, root, "node_modules", `{"name":"whatever"}`)
	// A directory with package.json but no src/ must be skipped, not crash.
	if err := os.MkdirAll(filepath.Join(root, "no-src"), 0o755); err != nil {
		t.Fatalf("mkdir no-src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "no-src", "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := Detect(root)
	byDir := map[string]Integration{}
	for _, i := range got {
		byDir[i.Dir] = i
	}

	if _, ok := byDir["node_modules"]; ok {
		t.Fatalf("node_modules must be ignored, got it in results: %+v", got)
	}
	if _, ok := byDir["no-src"]; ok {
		t.Fatalf("dir without src/ must be skipped, got it in results: %+v", got)
	}

	jira, ok := byDir["jira-integration"]
	if !ok {
		t.Fatalf("jira-integration missing from Detect results: %+v", got)
	}
	if jira.Type != "cni" {
		t.Errorf("jira-integration type = %q, want cni", jira.Type)
	}
	if jira.FlowCount != 2 {
		t.Errorf("jira-integration flowCount = %d, want 2 (index.ts excluded)", jira.FlowCount)
	}
	if jira.Version != "1.2.0" || jira.Description != "Jira sync" {
		t.Errorf("jira-integration version/description not carried through: %+v", jira)
	}

	if got := byDir["http"].Type; got != "component" {
		t.Errorf("http type = %q, want component (spectral dep, no flows)", got)
	}
	if got := byDir["guru-manifest"].Type; got != "shared" {
		t.Errorf("guru-manifest type = %q, want shared (special-cased dir name)", got)
	}
	if got := byDir["plain-lib"].Type; got != "other" {
		t.Errorf("plain-lib type = %q, want other (no spectral dep)", got)
	}

	// Sort order: cni, component, shared, other; alpha within group.
	wantOrder := []string{"jira-integration", "http", "guru-manifest", "plain-lib"}
	if len(got) != len(wantOrder) {
		t.Fatalf("Detect returned %d integrations, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, dir := range wantOrder {
		if got[i].Dir != dir {
			t.Errorf("Detect()[%d].Dir = %q, want %q (order: %v)", i, got[i].Dir, dir, wantOrder)
		}
	}
}

func TestDetect_MissingDirReturnsNilNotError(t *testing.T) {
	got := Detect(filepath.Join(t.TempDir(), "does-not-exist"))
	if got != nil {
		t.Fatalf("Detect on a missing dir = %+v, want nil", got)
	}
}

func TestFindRoot_WalksUpFromInsideOneIntegration(t *testing.T) {
	root := t.TempDir()
	spectralDep := `{"dependencies":{"@prismatic-io/spectral":"^9.0.0"}}`
	writeFixtureIntegration(t, root, "jira-integration", spectralDep, "sync.ts")
	writeFixtureIntegration(t, root, "confluence", spectralDep, "sync.ts")

	// Session opened one level deep, inside jira-integration/src itself.
	start := filepath.Join(root, "jira-integration", "src")

	gotRoot, integrations, ok := FindRoot(start)
	if !ok {
		t.Fatalf("FindRoot(%q) ok = false, want true", start)
	}
	if gotRoot != root {
		t.Errorf("FindRoot root = %q, want %q", gotRoot, root)
	}
	if len(integrations) != 2 {
		t.Errorf("FindRoot integrations = %+v, want 2 entries", integrations)
	}
}

func TestFindRoot_SingleIntegrationIsNotMistakenForRoot(t *testing.T) {
	root := t.TempDir()
	writeFixtureIntegration(t, root, "jira-integration",
		`{"dependencies":{"@prismatic-io/spectral":"^9.0.0"}}`, "sync.ts")

	// Only one integration under root -- must NOT be reported as a
	// Prismatic monorepo root (the two-or-more-children guard).
	_, _, ok := FindRoot(filepath.Join(root, "jira-integration"))
	if ok {
		t.Fatalf("FindRoot on a dir with only one integration child returned ok=true, want false")
	}
}

func TestFindRoot_NoIntegrationsAnywhereIsNotSupported(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, _, ok := FindRoot(filepath.Join(dir, "a", "b", "c"))
	if ok {
		t.Fatalf("FindRoot on a plain non-Prismatic tree returned ok=true, want false")
	}
}
