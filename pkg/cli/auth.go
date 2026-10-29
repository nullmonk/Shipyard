package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StoredRemoteCred holds the credentials shipyard keeps per remote server.
// API tokens (ddk_...) are the primary credential used on every request
// (Authorization: Bearer). OAuth tokens are kept alongside so a fresh
// `remote connect` can reuse/refresh without another browser round-trip,
// and so operators can inspect what was minted.
type storedRemoteCred struct {
	APIToken     string    `json:"api_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Username     string    `json:"username,omitempty"`
	Role         string    `json:"role,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// authStoreFile is the on-disk shape at ~/.local/share/shipyard/auth.json.
type authStoreFile struct {
	Remotes map[string]storedRemoteCred `json:"remotes"`
}

// authStorePath returns the primary read/write path for remote credentials:
//
//	$SHIPYARD_AUTH_FILE (explicit override) >
//	$XDG_DATA_HOME/shipyard/auth.json >
//	~/.local/share/shipyard/auth.json
//
// The legacy ~/.local/shipyard/auth.json path is still read (and migrated
// forward on save) because early docs pointed there.
func authStorePath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("SHIPYARD_AUTH_FILE")); p != "" {
		return p, nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "shipyard", "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "shipyard", "auth.json"), nil
}

// authStoreReadCandidates lists every path we will read credentials from,
// in priority order. The primary path wins; legacy fallbacks are merged
// underneath so old logins keep working.
func authStoreReadCandidates() []string {
	var out []string
	if primary, err := authStorePath(); err == nil && primary != "" {
		out = append(out, primary)
	}
	if home, err := os.UserHomeDir(); err == nil {
		legacy := filepath.Join(home, ".local", "shipyard", "auth.json")
		if len(out) == 0 || legacy != out[0] {
			out = append(out, legacy)
		}
		legacyAlt := filepath.Join(home, ".local", "shipyard", "credentials.json")
		out = append(out, legacyAlt)
	}
	return out
}

// normalizeAuthKey maps a remote URL (or bare host/profile) to a stable
// lookup key: lower-cased host (with port when present), or the trimmed
// raw value when unparseable.
func normalizeAuthKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if h := strings.ToLower(u.Host); h != "" {
			return h
		}
	}
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/"))
}

// authKeysFor returns every lookup key that could hold creds for remoteURL:
// the normalized URL, the host, and the raw trimmed value.
func authKeysFor(remoteURL string) []string {
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	trimmed := strings.TrimRight(strings.TrimSpace(remoteURL), "/")
	add(strings.ToLower(trimmed))
	add(normalizeAuthKey(remoteURL))
	if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
		add(strings.ToLower(u.Host))
		add(strings.ToLower(u.Hostname()))
	}
	return keys
}

func loadAuthStore() (*authStoreFile, error) {
	merged := &authStoreFile{Remotes: make(map[string]storedRemoteCred)}
	// Read lowest-priority first so higher-priority files overwrite.
	cands := authStoreReadCandidates()
	for i := len(cands) - 1; i >= 0; i-- {
		raw, err := os.ReadFile(cands[i])
		if err != nil {
			continue
		}
		var f authStoreFile
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		for k, v := range f.Remotes {
			merged.Remotes[k] = v
		}
	}
	if merged.Remotes == nil {
		merged.Remotes = make(map[string]storedRemoteCred)
	}
	return merged, nil
}

func saveAuthStore(st *authStoreFile) error {
	p, err := authStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if st.Remotes == nil {
		st.Remotes = make(map[string]storedRemoteCred)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	// Best-effort migration: if a legacy file exists at ~/.local/shipyard/,
	// keep it in sync so older tooling sees the same credentials.
	if home, herr := os.UserHomeDir(); herr == nil {
		for _, legacy := range []string{
			filepath.Join(home, ".local", "shipyard", "auth.json"),
			filepath.Join(home, ".local", "shipyard", "credentials.json"),
		} {
			if legacy == p {
				continue
			}
			if _, serr := os.Stat(legacy); serr == nil {
				_ = os.MkdirAll(filepath.Dir(legacy), 0o700)
				_ = os.WriteFile(legacy, append(raw, '\n'), 0o600)
			}
		}
	}
	return nil
}

// getStoredCred returns the saved credential for remoteURL, checking the
// auth store (all key forms) and falling back to the legacy config.yaml
// profile token.
func getStoredCred(remoteURL string) storedRemoteCred {
	if st, err := loadAuthStore(); err == nil && st != nil {
		for _, k := range authKeysFor(remoteURL) {
			if c, ok := st.Remotes[k]; ok {
				return c
			}
			// Case-insensitive scan for older files.
			for sk, sv := range st.Remotes {
				if strings.EqualFold(sk, k) {
					return sv
				}
			}
		}
	}
	// Legacy fallback: token kept in ~/.config/shipyard/config.yaml.
	if cfg, err := loadRemoteConfig(); err == nil && cfg != nil {
		trimmed := strings.TrimRight(strings.TrimSpace(remoteURL), "/")
		for name, prof := range cfg.Remotes {
			if strings.EqualFold(strings.TrimRight(prof.URL, "/"), trimmed) || strings.EqualFold(name, normalizeAuthKey(remoteURL)) {
				if strings.TrimSpace(prof.Token) != "" {
					return storedRemoteCred{APIToken: prof.Token}
				}
			}
		}
	}
	return storedRemoteCred{}
}

// setStoredCred saves cred for remoteURL under every lookup key form so
// future lookups hit regardless of URL-vs-host spelling.
func setStoredCred(remoteURL string, cred storedRemoteCred) error {
	st, _ := loadAuthStore()
	if st == nil {
		st = &authStoreFile{Remotes: make(map[string]storedRemoteCred)}
	}
	if st.Remotes == nil {
		st.Remotes = make(map[string]storedRemoteCred)
	}
	cred.UpdatedAt = time.Now().UTC()
	for _, k := range authKeysFor(remoteURL) {
		st.Remotes[k] = cred
	}
	return saveAuthStore(st)
}

// clearStoredCred removes saved credentials for remoteURL (all key forms).
func clearStoredCred(remoteURL string) error {
	st, _ := loadAuthStore()
	if st == nil || len(st.Remotes) == 0 {
		return nil
	}
	for _, k := range authKeysFor(remoteURL) {
		delete(st.Remotes, k)
		for sk := range st.Remotes {
			if strings.EqualFold(sk, k) {
				delete(st.Remotes, sk)
			}
		}
	}
	return saveAuthStore(st)
}

// saveRemoteProfileAndCred persists the profile URL in config.yaml and the
// secret credential in the auth store (0600). The secret never stays only
// in config.yaml: config keeps the URL, the auth store keeps the token.
func saveRemoteProfileAndCred(rawURL, profileName string, cred storedRemoteCred) error {
	cfg, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(profileName)
	if name == "" {
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Host != "" {
			name = parsed.Host
		} else {
			name = "default"
		}
	}
	cfg.Remotes[name] = remoteProfile{URL: rawURL, Token: cred.APIToken}
	cfg.Active = name
	if err := saveRemoteConfig(cfg); err != nil {
		return fmt.Errorf("save remote config: %w", err)
	}
	if strings.TrimSpace(cred.APIToken) != "" || strings.TrimSpace(cred.AccessToken) != "" || strings.TrimSpace(cred.RefreshToken) != "" {
		if err := setStoredCred(rawURL, cred); err != nil {
			return fmt.Errorf("save auth store: %w", err)
		}
		// Also index under the profile name so `shipyard remote --remote <name>`
		// resolves even when the name is not a URL.
		if !strings.EqualFold(name, normalizeAuthKey(rawURL)) {
			if err := setStoredCred(name, cred); err != nil {
				return fmt.Errorf("save auth store: %w", err)
			}
		}
	}
	return nil
}
