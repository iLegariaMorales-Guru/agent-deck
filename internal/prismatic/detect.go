// Package prismatic detects Guru's Prismatic CNI integration monorepo
// layout inside a project directory.
//
// The classification mirrors guru-prismatic/cli's detectIntegrations
// (the "Prismatic Workbench" TUI's hub) so the web UI's Prismatic pane can
// show the same integration list — CNI vs component vs shared library —
// without depending on that CLI or its ~/.cni-cli config at all. Detection
// is purely structural (package.json + src/ shape, @prismatic-io/spectral
// dependency), not name-based, so it works for any Prismatic monorepo, not
// just one named "guru-prismatic".
package prismatic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ignoreDirs are skipped outright when scanning for integrations — tooling,
// vendor, and build-output directories that are never a CNI/component.
var ignoreDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "out": true,
	"coverage": true, ".git": true, ".claude": true, ".serena": true,
	".circleci": true, ".idea": true, ".vscode": true, "cli": true,
	"workbench": true, "docs": true, "assets": true,
}

// maxRootSearchDepth bounds how far FindRoot walks up from a session's
// project path looking for the monorepo root. A session is typically opened
// either at the root itself or one level down inside a single integration's
// own directory; a few extra levels of headroom costs nothing (each level is
// one os.ReadDir of a handful of entries) and covers a session opened deeper
// inside an integration (e.g. its src/flows/).
const maxRootSearchDepth = 4

// Integration is one detected CNI, component, or shared-library directory.
type Integration struct {
	Dir                  string `json:"dir"`
	Name                 string `json:"name"`
	Path                 string `json:"path"`
	Type                 string `json:"type"` // cni | component | shared | other
	Version              string `json:"version,omitempty"`
	Description          string `json:"description,omitempty"`
	FlowCount            int    `json:"flowCount"`
	HasApplicationSpec   bool   `json:"hasApplicationSpec"`
	HasConnections       bool   `json:"hasConnections"`
	HasComponentRegistry bool   `json:"hasComponentRegistry"`
}

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// Detect scans the immediate children of dir for Prismatic CNI/component/
// shared-library packages. A child must have its own package.json AND a
// src/ subdirectory to be considered at all — dot-directories, ignoreDirs,
// and non-directories are skipped first. Results are sorted CNI, component,
// shared, then other, alphabetically within each group — the same order
// the CLI's hub renders in.
func Detect(dir string) []Integration {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []Integration
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || ignoreDirs[name] || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		pkg, ok := readPackageJSON(filepath.Join(path, "package.json"))
		if !ok {
			continue
		}
		srcPath := filepath.Join(path, "src")
		if info, err := os.Stat(srcPath); err != nil || !info.IsDir() {
			continue
		}

		flowCount := countFlowFiles(filepath.Join(srcPath, "flows"))
		hasSpec := isRegularFile(filepath.Join(srcPath, "applicationSpec.ts"))
		hasConn := isRegularFile(filepath.Join(srcPath, "connections.ts"))
		hasReg := isRegularFile(filepath.Join(srcPath, "componentRegistry.ts"))
		usesSpectral := hasDependency(pkg, "@prismatic-io/spectral")

		pkgName := pkg.Name
		if pkgName == "" {
			pkgName = name
		}

		out = append(out, Integration{
			Dir:                  name,
			Name:                 pkgName,
			Path:                 path,
			Type:                 classify(name, pkgName, usesSpectral, flowCount > 0),
			Version:              pkg.Version,
			Description:          pkg.Description,
			FlowCount:            flowCount,
			HasApplicationSpec:   hasSpec,
			HasConnections:       hasConn,
			HasComponentRegistry: hasReg,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		oi, oj := typeOrder(out[i].Type), typeOrder(out[j].Type)
		if oi != oj {
			return oi < oj
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// FindRoot walks up from startPath looking for a Prismatic monorepo root —
// a directory whose immediate children include at least two detected
// integrations (CNI/component/shared). startPath itself is checked first
// (a session opened directly at the repo root), then each ancestor up to
// maxRootSearchDepth levels — the common case of a session opened inside
// one integration's own directory (or deeper, inside its src/).
//
// Requiring two-or-more relevant children (rather than one) is deliberate:
// a session opened one level ABOVE a single integration's own package.json
// would otherwise misreport that integration's own parent-of-one as "the
// monorepo root" purely by chance.
func FindRoot(startPath string) (root string, integrations []Integration, ok bool) {
	dir := filepath.Clean(startPath)
	for i := 0; i <= maxRootSearchDepth; i++ {
		if dir == "" || dir == "." || dir == string(filepath.Separator) {
			break
		}
		found := Detect(dir)
		if countRelevant(found) >= 2 {
			return dir, found, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil, false
}

func countRelevant(integrations []Integration) int {
	n := 0
	for _, i := range integrations {
		if i.Type == "cni" || i.Type == "component" || i.Type == "shared" {
			n++
		}
	}
	return n
}

func classify(dirName, pkgName string, usesSpectral, hasFlows bool) string {
	// Shared library: @component-manifests/* or the well-known guru-manifest
	// dir name — same special case cni-cli hardcodes, since these packages
	// don't themselves import @prismatic-io/spectral.
	if strings.HasPrefix(pkgName, "@component-manifests/") || dirName == "guru-manifest" {
		return "shared"
	}
	if !usesSpectral {
		return "other"
	}
	if hasFlows {
		return "cni"
	}
	return "component"
}

func typeOrder(t string) int {
	switch t {
	case "cni":
		return 0
	case "component":
		return 1
	case "shared":
		return 2
	default:
		return 3
	}
}

func readPackageJSON(path string) (packageJSON, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageJSON{}, false
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, false
	}
	return pkg, true
}

func hasDependency(pkg packageJSON, name string) bool {
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	if _, ok := pkg.DevDependencies[name]; ok {
		return true
	}
	return false
}

// countFlowFiles counts *.ts files directly under a src/flows directory,
// excluding index.ts (the barrel export, not a flow) — mirrors cni-cli's
// listTsFiles/flowCount. Missing directory is not an error, just zero flows.
func countFlowFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".ts") && e.Name() != "index.ts" {
			n++
		}
	}
	return n
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
