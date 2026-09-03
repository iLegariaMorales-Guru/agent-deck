package prismatic

// 24h on-disk cache for env+integrationDir -> Prismatic ipaasIntegrationId,
// so the curl wizard doesn't shell out to `prism integrations:list` on
// every open. Mirrors cni-cli's lib/ipaasCache.js one-for-one, just under
// agent-deck's own data dir instead of ~/.cni-cli.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
	"github.com/asheshgoplani/agent-deck/internal/safeio"
)

const ipaasCacheTTL = 24 * time.Hour

// IpaasMatch is a resolved Prismatic integration identity.
type IpaasMatch struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	VersionNumber int    `json:"versionNumber,omitempty"`
}

type ipaasCacheEntry struct {
	IpaasMatch
	CachedAt time.Time `json:"cachedAt"`
}

type ipaasCacheFile struct {
	Entries map[string]ipaasCacheEntry `json:"entries"`
}

var ipaasCacheMu sync.Mutex

func ipaasCachePath() (string, error) {
	return agentpaths.EffectiveDataPath("prismatic/ipaas-cache.json", "prismatic")
}

func ipaasCacheKey(env, dir string) string {
	return env + "|" + dir
}

func loadIpaasCacheLocked() (ipaasCacheFile, error) {
	path, err := ipaasCachePath()
	if err != nil {
		return ipaasCacheFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ipaasCacheFile{Entries: map[string]ipaasCacheEntry{}}, nil
		}
		return ipaasCacheFile{}, err
	}
	var cache ipaasCacheFile
	if err := json.Unmarshal(data, &cache); err != nil || cache.Entries == nil {
		// A corrupt or pre-format cache file is not worth failing over —
		// same "start fresh" tolerance as ipaasCache.js's try/catch.
		return ipaasCacheFile{Entries: map[string]ipaasCacheEntry{}}, nil
	}
	return cache, nil
}

func saveIpaasCacheLocked(cache ipaasCacheFile) error {
	path, err := ipaasCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return safeio.SafeOverwrite(path, data, safeio.Options{Perm: 0o600})
}

// GetCachedIpaasMatch returns a fresh (<24h old) cached match for
// env+integrationDir, or ok=false if there's no entry or it has expired.
func GetCachedIpaasMatch(env, integrationDir string) (match IpaasMatch, ok bool) {
	ipaasCacheMu.Lock()
	defer ipaasCacheMu.Unlock()
	cache, err := loadIpaasCacheLocked()
	if err != nil {
		return IpaasMatch{}, false
	}
	entry, found := cache.Entries[ipaasCacheKey(env, integrationDir)]
	if !found {
		return IpaasMatch{}, false
	}
	age := time.Since(entry.CachedAt)
	if age < 0 || age >= ipaasCacheTTL {
		return IpaasMatch{}, false
	}
	return entry.IpaasMatch, true
}

// SetCachedIpaasMatch records a resolved match for env+integrationDir.
func SetCachedIpaasMatch(env, integrationDir string, match IpaasMatch) error {
	ipaasCacheMu.Lock()
	defer ipaasCacheMu.Unlock()
	cache, err := loadIpaasCacheLocked()
	if err != nil {
		return err
	}
	cache.Entries[ipaasCacheKey(env, integrationDir)] = ipaasCacheEntry{IpaasMatch: match, CachedAt: time.Now()}
	return saveIpaasCacheLocked(cache)
}

// InvalidateCachedIpaasMatch removes any cached entry for env+integrationDir
// (e.g. after the user picks a different match by hand).
func InvalidateCachedIpaasMatch(env, integrationDir string) error {
	ipaasCacheMu.Lock()
	defer ipaasCacheMu.Unlock()
	cache, err := loadIpaasCacheLocked()
	if err != nil {
		return err
	}
	delete(cache.Entries, ipaasCacheKey(env, integrationDir))
	return saveIpaasCacheLocked(cache)
}
