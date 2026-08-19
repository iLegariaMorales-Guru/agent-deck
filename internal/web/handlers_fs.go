package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// FSBrowseEntry is one subdirectory entry in a FSBrowseResponse.
type FSBrowseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FSBrowseResponse is returned by GET /api/fs/browse. It lists the
// subdirectories of Path — never files, since this only exists to let the
// New Session dialog's folder browser pick a project working directory.
//
// Browsers refuse to hand a web page the absolute filesystem path of a
// folder chosen via a native picker (neither <input webkitdirectory> nor
// the File System Access API exposes it — a deliberate sandboxing
// restriction, not an oversight). Since the web UI here only ever serves
// 127.0.0.1 or an explicitly-tokened LAN/remote host, this endpoint lets
// the dialog walk the SAME filesystem a native picker would show, at the
// cost of a custom (not OS-native) browser UI. See CreateSessionDialog.js's
// FolderBrowser.
type FSBrowseResponse struct {
	Path    string          `json:"path"`
	Parent  string          `json:"parent,omitempty"`
	Entries []FSBrowseEntry `json:"entries"`
}

func (s *Server) handleFSBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized")
		return
	}

	reqPath := strings.TrimSpace(r.URL.Query().Get("path"))
	var dir string
	if reqPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "cannot resolve home directory")
			return
		}
		dir = home
	} else {
		resolved, err := session.ResolveProjectPath(reqPath)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		dir = resolved
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "not a directory: "+dir)
		return
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternalError, "cannot list directory: "+err.Error())
		return
	}

	entries := make([]FSBrowseEntry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			continue // hide dotfiles/dotdirs, same default Finder convention
		}
		isDir := item.IsDir()
		if !isDir && item.Type()&os.ModeSymlink != 0 {
			// A symlinked directory (common in worktree setups) should still
			// be browsable rather than silently disappearing from the list.
			if target, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil && target.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		entries = append(entries, FSBrowseEntry{Name: name, Path: filepath.Join(dir, name)})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	resp := FSBrowseResponse{Path: dir, Entries: entries}
	if parent := filepath.Dir(dir); parent != dir {
		resp.Parent = parent
	}
	writeJSON(w, http.StatusOK, resp)
}
