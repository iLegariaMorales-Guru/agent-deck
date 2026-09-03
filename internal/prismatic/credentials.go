package prismatic

// Per-environment Prismatic secrets: Prism refresh tokens (deploy auth) and
// Guru API credentials ("user:token", used by the source-definition curl
// generator in a later PR). Mirrors guru-prismatic/cli's ~/.cni-cli/
// prism-tokens.json + guru-api-creds.json, but as one file under agent-deck's
// own data dir, and NEVER read back over the API — handlers_prismatic_credentials.go
// exposes presence booleans only, never the stored value.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
	"github.com/asheshgoplani/agent-deck/internal/safeio"
)

// ValidEnvs are the only two environments Prismatic credentials are scoped
// to — matches cni-cli's qa/prod distinction exactly.
var ValidEnvs = map[string]bool{"qa": true, "prod": true}

// Credentials is the on-disk shape. Both maps are env -> secret; a missing
// key means "not configured", never an empty string (Clear removes the key
// entirely so presence checks stay a plain map lookup).
type Credentials struct {
	Prism map[string]string `json:"prism,omitempty"` // env -> Prism refresh token
	Guru  map[string]string `json:"guru,omitempty"`  // env -> "user:token"
}

// credentialsMu serializes every read-modify-write below. Request volume
// here is tiny (a human clicking Set/Clear in the UI), so a single package
// mutex is simpler than per-file locking and just as correct.
var credentialsMu sync.Mutex

// credentialsPath resolves to <agent-deck data dir>/prismatic/credentials.json
// — the same profile-aware, XDG-aware resolution internal/session uses for
// skills/hooks/triage data (agentpaths.EffectiveDataPath).
func credentialsPath() (string, error) {
	return agentpaths.EffectiveDataPath("prismatic/credentials.json", "prismatic")
}

func loadCredentialsLocked() (Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Credentials{}, nil
		}
		return Credentials{}, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return creds, nil
}

func saveCredentialsLocked(creds Credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	// 0o700: this directory holds nothing but secrets, same as
	// session.GetHooksDir()/auth_hold.go's own 0700 dirs.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	// SkipBackup: a secret's PREVIOUS value should not linger in a .bak
	// file next to the live one — unlike config.toml, there's no value in
	// recovering an old token.
	return safeio.SafeOverwrite(path, data, safeio.Options{Perm: 0o600, SkipBackup: true})
}

// CredentialStatus reports which env/kind combinations are configured,
// without ever exposing the stored value.
type CredentialStatus struct {
	Prism map[string]bool `json:"prism"` // env -> configured
	Guru  map[string]bool `json:"guru"`  // env -> configured
}

// LoadStatus returns presence booleans for every valid env, for both kinds.
func LoadStatus() (CredentialStatus, error) {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	creds, err := loadCredentialsLocked()
	if err != nil {
		return CredentialStatus{}, err
	}
	status := CredentialStatus{Prism: map[string]bool{}, Guru: map[string]bool{}}
	for env := range ValidEnvs {
		status.Prism[env] = creds.Prism[env] != ""
		status.Guru[env] = creds.Guru[env] != ""
	}
	return status, nil
}

// SetPrismToken stores a Prism refresh token for env, overwriting any
// existing one. Returns an error for an unknown env or an empty value.
func SetPrismToken(env, token string) error {
	if !ValidEnvs[env] {
		return fmt.Errorf("unknown environment %q", env)
	}
	if token == "" {
		return fmt.Errorf("token is required")
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	creds, err := loadCredentialsLocked()
	if err != nil {
		return err
	}
	if creds.Prism == nil {
		creds.Prism = map[string]string{}
	}
	creds.Prism[env] = token
	return saveCredentialsLocked(creds)
}

// ClearPrismToken removes env's Prism refresh token, if any. Clearing an
// already-absent token is a no-op, not an error.
func ClearPrismToken(env string) error {
	if !ValidEnvs[env] {
		return fmt.Errorf("unknown environment %q", env)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	creds, err := loadCredentialsLocked()
	if err != nil {
		return err
	}
	delete(creds.Prism, env)
	return saveCredentialsLocked(creds)
}

// SetGuruCreds stores Guru API credentials for env in "user:token" form —
// mirrors cni-cli's format requirement exactly (the source-def curl
// generator embeds this verbatim into a `-u user:token` curl flag).
func SetGuruCreds(env, creds string) error {
	if !ValidEnvs[env] {
		return fmt.Errorf("unknown environment %q", env)
	}
	if creds == "" {
		return fmt.Errorf("credentials are required")
	}
	if !hasColon(creds) {
		return fmt.Errorf("Guru API credentials must be in 'user:token' format")
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	all, err := loadCredentialsLocked()
	if err != nil {
		return err
	}
	if all.Guru == nil {
		all.Guru = map[string]string{}
	}
	all.Guru[env] = creds
	return saveCredentialsLocked(all)
}

// ClearGuruCreds removes env's Guru API credentials, if any.
func ClearGuruCreds(env string) error {
	if !ValidEnvs[env] {
		return fmt.Errorf("unknown environment %q", env)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	all, err := loadCredentialsLocked()
	if err != nil {
		return err
	}
	delete(all.Guru, env)
	return saveCredentialsLocked(all)
}

// GetPrismToken returns env's stored Prism refresh token, or "" if unset.
// Used server-side only (shelling out to the prism CLI for ipaas ID
// resolution) — never exposed over the credentials API.
func GetPrismToken(env string) (string, error) {
	if !ValidEnvs[env] {
		return "", fmt.Errorf("unknown environment %q", env)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	creds, err := loadCredentialsLocked()
	if err != nil {
		return "", err
	}
	return creds.Prism[env], nil
}

// GetGuruCreds returns env's stored Guru "user:token" credentials, or "" if
// unset. Used server-side only to embed into generated curl commands — the
// whole point of the vault (see credentials API's status-only response for
// the contrasting case where the raw value must never leave the server).
func GetGuruCreds(env string) (string, error) {
	if !ValidEnvs[env] {
		return "", fmt.Errorf("unknown environment %q", env)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	creds, err := loadCredentialsLocked()
	if err != nil {
		return "", err
	}
	return creds.Guru[env], nil
}

func hasColon(s string) bool {
	for _, r := range s {
		if r == ':' {
			return true
		}
	}
	return false
}
