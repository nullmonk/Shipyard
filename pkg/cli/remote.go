package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"shipyard/pkg/shipfile"
)

const interactiveShellPrefix = "[!] Interactive shell session ready: "

type remoteProfile struct {
	URL   string `yaml:"url" json:"url"`
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
}

type remoteConfig struct {
	Active  string                   `yaml:"active" json:"active"`
	Remotes map[string]remoteProfile `yaml:"remotes" json:"remotes"`
}

type remoteStatusResponse struct {
	Status        string `json:"status"`
	Service       string `json:"service"`
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user"`
	Role          string `json:"role"`
	Tools         int    `json:"tools"`
	Systems       int    `json:"systems"`
	RunningJobs   int    `json:"running_jobs"`
	BuildVerbose  bool   `json:"build_verbose"`
}

type remoteToolInfo struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type remotePushRequest struct {
	Name     string `json:"name"`
	Shipfile string `json:"shipfile"`
}

type remotePushResponse struct {
	Tool      remoteToolInfo         `json:"tool"`
	Created   bool                   `json:"created"`
	Type      string                 `json:"type"`
	Variables []shipfile.VariableDef `json:"variables,omitempty"`
}

type remoteBuildRequest struct {
	Name         string            `json:"name"`
	Shipfile     string            `json:"shipfile"`
	System       string            `json:"system"`
	SystemID     uint              `json:"system_id"`
	PackageImage string            `json:"package"`
	PackageName  string            `json:"package_name"`
	Image        string            `json:"image"`
	Version      string            `json:"version"`
	Variables    map[string]string `json:"variables,omitempty"`
	NoTest       bool              `json:"no_test"`
	Verbose      *bool             `json:"verbose,omitempty"`
	Stream       bool              `json:"stream"`
	Interactive  bool              `json:"interactive"`
	ShellOnly    bool              `json:"shell_only"`
	// SourceArchive is a base64-encoded gzip tar of the local source directory,
	// sent when the shipfile has no Url and copy mode is active.
	SourceArchive string `json:"source_archive,omitempty"`
}

type remoteBuildResponse struct {
	Tool      remoteToolInfo `json:"tool"`
	BuildID   uint           `json:"build_id,omitempty"`
	JobID     uint           `json:"job_id,omitempty"`
	StreamURL string         `json:"stream_url,omitempty"`
}

func remoteConfigPath() (string, error) {
	if p := os.Getenv("SHIPYARD_CONFIG"); p != "" {
		return p, nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", err
		}
		cfgDir = filepath.Join(home, ".config")
	}
	return filepath.Join(cfgDir, "shipyard", "config.yaml"), nil
}

func loadRemoteConfig() (*remoteConfig, error) {
	p, err := remoteConfigPath()
	if err != nil {
		return nil, err
	}
	cfg := &remoteConfig{Remotes: make(map[string]remoteProfile)}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]remoteProfile)
	}
	return cfg, nil
}

func saveRemoteConfig(cfg *remoteConfig) error {
	p, err := remoteConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func resolveRemoteEndpoint(flagURL, flagToken string) (string, string, error) {
	u := strings.TrimSpace(flagURL)
	if u == "" {
		u = strings.TrimSpace(globalRemote)
	}
	tok := strings.TrimSpace(flagToken)
	if u == "" {
		u = firstEnv("SHIPYARD_REMOTE", "DRYDOCK_REMOTE")
	}
	if tok == "" {
		tok = firstEnv("SHIPYARD_TOKEN", "DRYDOCK_TOKEN")
	}

	cfg, _ := loadRemoteConfig()
	if cfg != nil {
		// A bare profile name (e.g. --remote <name>) expands to its URL.
		if prof, ok := cfg.Remotes[u]; ok && u != "" {
			u = prof.URL
			if tok == "" {
				tok = prof.Token
			}
		} else if u == "" && cfg.Active != "" {
			if prof, ok := cfg.Remotes[cfg.Active]; ok {
				u = prof.URL
				if tok == "" {
					tok = prof.Token
				}
			}
		} else if u != "" {
			// URL spelling may differ from the saved profile (host vs
			// full URL): fall back to host matching.
			for _, prof := range cfg.Remotes {
				if strings.EqualFold(strings.TrimRight(prof.URL, "/"), strings.TrimRight(u, "/")) {
					if tok == "" {
						tok = prof.Token
					}
					break
				}
			}
		}
	}
	// Saved OAuth/API credentials in the auth store
	// (~/.local/share/shipyard/auth.json) win over nothing but lose to
	// explicit --token / env / config.yaml tokens above.
	if tok == "" && u != "" {
		if cred := getStoredCred(u); strings.TrimSpace(cred.APIToken) != "" {
			tok = strings.TrimSpace(cred.APIToken)
		}
	}
	if tok == "" && cfg != nil && cfg.Active != "" {
		if cred := getStoredCred(cfg.Active); strings.TrimSpace(cred.APIToken) != "" {
			tok = strings.TrimSpace(cred.APIToken)
		}
	}

	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return "", "", fmt.Errorf("no remote server configured (use `shipyard remote connect <url>`, `--remote`, or `$SHIPYARD_REMOTE`)")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	return u, tok, nil
}

// queryRemoteStatus GETs /api/remote/status and decodes the payload.
// A 401 means the server requires auth (caller decides between token and
// OIDC); any other non-200 is returned as an error with the server body.
func queryRemoteStatus(baseURL, token string) (*remoteStatusResponse, error) {
	req, err := newRemoteRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/remote/status", token, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var st remoteStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decode status response: %w", err)
	}
	return &st, nil
}

// sortedRemoteNames returns the configured profile names in stable order.
func sortedRemoteNames(cfg *remoteConfig) []string {
	if cfg == nil || len(cfg.Remotes) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// defaultRemoteName returns the default profile: Active when it still
// exists, otherwise the first name in sorted order ("the first" remote).
func defaultRemoteName(cfg *remoteConfig) string {
	if cfg == nil || len(cfg.Remotes) == 0 {
		return ""
	}
	if cfg.Active != "" {
		if _, ok := cfg.Remotes[cfg.Active]; ok {
			return cfg.Active
		}
	}
	return sortedRemoteNames(cfg)[0]
}

// quotedRemoteNames renders the configured profiles as 'a', 'b' for errors.
func quotedRemoteNames(cfg *remoteConfig) string {
	names := sortedRemoteNames(cfg)
	q := make([]string, 0, len(names))
	for _, n := range names {
		q = append(q, fmt.Sprintf("%q", n))
	}
	return strings.Join(q, ", ")
}

// explicitRemoteSelector reports whether the caller explicitly selected a
// remote (command --remote flag, global --remote, or $SHIPYARD_REMOTE).
// Explicit selections win over the Active default when several remotes exist.
func explicitRemoteSelector(flagURL string) (string, bool) {
	if s := strings.TrimSpace(flagURL); s != "" {
		return s, true
	}
	if s := strings.TrimSpace(globalRemote); s != "" {
		return s, true
	}
	if s := firstEnv("SHIPYARD_REMOTE", "DRYDOCK_REMOTE"); s != "" {
		return s, true
	}
	return "", false
}

// findProfileByNameOrURL resolves a user-supplied selector (profile name,
// full URL, or bare host) to its canonical profile name.
func findProfileByNameOrURL(cfg *remoteConfig, sel string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return "", false
	}
	if _, ok := cfg.Remotes[sel]; ok {
		return sel, true
	}
	trimmed := strings.TrimRight(sel, "/")
	for name, prof := range cfg.Remotes {
		if strings.EqualFold(strings.TrimRight(prof.URL, "/"), trimmed) {
			return name, true
		}
	}
	// Bare host (with or without port) matching the profile URL's host.
	host := strings.ToLower(trimmed)
	if strings.Contains(host, "://") {
		if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
			host = strings.ToLower(u.Host)
		}
	} else if strings.Contains(host, ":") || strings.Contains(host, ".") || host == "localhost" {
		// treat as host, keep as-is
	} else {
		return "", false
	}
	for name, prof := range cfg.Remotes {
		if u, err := url.Parse(prof.URL); err == nil && u.Host != "" {
			if strings.EqualFold(strings.ToLower(u.Host), host) || strings.EqualFold(strings.ToLower(u.Hostname()), host) {
				return name, true
			}
		}
	}
	return "", false
}

// resolveRemoteTokenFor returns the bearer token for a profile: explicit
// --token flag, then env, then the profile's saved token, then auth store.
func resolveRemoteTokenFor(cfg *remoteConfig, profileName, profileURL, flagToken string) string {
	if s := strings.TrimSpace(flagToken); s != "" {
		return s
	}
	if v := firstEnv("SHIPYARD_TOKEN", "DRYDOCK_TOKEN"); v != "" {
		return v
	}
	if cfg != nil && profileName != "" {
		if prof, ok := cfg.Remotes[profileName]; ok && strings.TrimSpace(prof.Token) != "" {
			return strings.TrimSpace(prof.Token)
		}
	}
	if profileURL != "" {
		if cred := getStoredCred(profileURL); strings.TrimSpace(cred.APIToken) != "" {
			return strings.TrimSpace(cred.APIToken)
		}
	}
	if profileName != "" {
		if cred := getStoredCred(profileName); strings.TrimSpace(cred.APIToken) != "" {
			return strings.TrimSpace(cred.APIToken)
		}
	}
	return ""
}

// resolveRemoteTarget picks the single remote an action should run against.
// It returns the canonical profile name ("" for ad-hoc URL selections),
// the server URL, and the bearer token.
//
// When several remotes are configured, the caller must say which one via
// --remote <name|url> (or $SHIPYARD_REMOTE); otherwise the Active profile
// (falling back to the first in sorted order) is used. With no usable
// default, an error lists the available profiles.
func resolveRemoteTarget(flagURL, flagToken string) (profile, baseURL, token string, err error) {
	cfg, _ := loadRemoteConfig()
	if cfg == nil {
		cfg = &remoteConfig{Remotes: make(map[string]remoteProfile)}
	}
	if sel, explicit := explicitRemoteSelector(flagURL); explicit {
		u := strings.TrimSpace(sel)
		// Bare profile name expands to its URL.
		if prof, ok := cfg.Remotes[u]; ok {
			return u, prof.URL, resolveRemoteTokenFor(cfg, u, prof.URL, flagToken), nil
		}
		// URL (or host) spelling: find the owning profile, if any.
		if name, ok := findProfileByNameOrURL(cfg, u); ok {
			prof := cfg.Remotes[name]
			return name, prof.URL, resolveRemoteTokenFor(cfg, name, prof.URL, flagToken), nil
		}
		// Ad-hoc URL with no saved profile: token can still come from
		// flags/env/auth store.
		nu := strings.TrimRight(u, "/")
		if nu != "" && !strings.HasPrefix(nu, "http://") && !strings.HasPrefix(nu, "https://") {
			nu = "http://" + nu
		}
		if nu == "" || nu == "http://" || nu == "https://" {
			return "", "", "", fmt.Errorf("no remote server configured (use `shipyard remote connect <url>`, `--remote`, or `$SHIPYARD_REMOTE`)")
		}
		tok := strings.TrimSpace(flagToken)
		if tok == "" {
			tok = firstEnv("SHIPYARD_TOKEN", "DRYDOCK_TOKEN")
		}
		if tok == "" {
			if cred := getStoredCred(nu); strings.TrimSpace(cred.APIToken) != "" {
				tok = strings.TrimSpace(cred.APIToken)
			}
		}
		return "", nu, tok, nil
	}
	switch len(cfg.Remotes) {
	case 0:
		// Nothing saved: keep the historical error.
		_, _, lerr := resolveRemoteEndpoint(flagURL, flagToken)
		return "", "", "", lerr
	case 1:
		// A single remote is the default ("the first").
		name := sortedRemoteNames(cfg)[0]
		prof := cfg.Remotes[name]
		return name, prof.URL, resolveRemoteTokenFor(cfg, name, prof.URL, flagToken), nil
	default:
		if cfg.Active != "" {
			if prof, ok := cfg.Remotes[cfg.Active]; ok {
				return cfg.Active, prof.URL, resolveRemoteTokenFor(cfg, cfg.Active, prof.URL, flagToken), nil
			}
		}
		return "", "", "", fmt.Errorf("multiple remotes configured (%s); specify which one with --remote <name> (or $SHIPYARD_REMOTE), or set the default with `shipyard remote use <name>`", quotedRemoteNames(cfg))
	}
}

// requireRemoteTarget resolves the single remote for actions that target
// one server (push/build/shell/logs, and the top-level build delegation).
// With several remotes saved, the caller must say which one via
// --remote <name|url> (or $SHIPYARD_REMOTE); with a single remote it
// defaults to it (the Active profile, else the first in sorted order).
func requireRemoteTarget(flagURL, flagToken string) (string, string, error) {
	profile, baseURL, tok, err := resolveRemoteTarget(flagURL, flagToken)
	if err != nil {
		return "", "", err
	}
	if cfg, _ := loadRemoteConfig(); cfg != nil {
		if note := usingRemoteNote(profile, baseURL, cfg); note != "" {
			fmt.Println(note)
		}
	}
	return baseURL, tok, nil
}

// usingRemoteNote renders the "which remote" line printed when a command
// falls back to the default instead of an explicit --remote selection.
func usingRemoteNote(profile, baseURL string, cfg *remoteConfig) string {
	if profile == "" || cfg == nil || len(cfg.Remotes) < 2 {
		return ""
	}
	return fmt.Sprintf("[*] Using remote %q (%s) — %d remotes configured; override with --remote <name>", profile, baseURL, len(cfg.Remotes))
}

// queryRemoteStatusWithTimeout is queryRemoteStatus with a bounded wait so a
// single dead server cannot stall `status` across many remotes.
func queryRemoteStatusWithTimeout(baseURL, token string, timeout time.Duration) (*remoteStatusResponse, error) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := newRemoteRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/remote/status", token, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var st remoteStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decode status response: %w", err)
	}
	return &st, nil
}

// runRemoteStatusSingle shows the detailed status of one remote.
func runRemoteStatusSingle(sel, flagToken string) error {
	baseURL, tok, err := resolveRemoteEndpoint(sel, flagToken)
	if err != nil {
		return err
	}
	fmt.Printf("[*] Checking status for %s...\n", baseURL)
	req, err := newRemoteRequest(http.MethodGet, baseURL+"/api/remote/status", tok, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("HTTP 401: %s (run `shipyard remote connect <url>` or `shipyard remote login` to authenticate; API tokens also work via --token or $SHIPYARD_TOKEN)", msg)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	var st remoteStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	fmt.Printf("    Status:        %s\n", strings.ToUpper(st.Status))
	fmt.Printf("    User / Role:   %s (%s)\n", st.User, st.Role)
	fmt.Printf("    Tools:         %d\n", st.Tools)
	fmt.Printf("    Systems:       %d\n", st.Systems)
	fmt.Printf("    Running Jobs:  %d\n", st.RunningJobs)
	fmt.Printf("    Build Verbose: %v\n", st.BuildVerbose)
	return nil
}

// runRemoteStatusAll checks every saved remote and prints one row each.
func runRemoteStatusAll(cfg *remoteConfig) error {
	names := sortedRemoteNames(cfg)
	fmt.Printf("Configured remotes (%d):\n", len(names))
	for _, name := range names {
		prof := cfg.Remotes[name]
		marker := " "
		if name == cfg.Active {
			marker = "*"
		}
		tok := resolveRemoteTokenFor(cfg, name, prof.URL, "")
		st, err := queryRemoteStatusWithTimeout(prof.URL, tok, 8*time.Second)
		if err != nil {
			fmt.Printf(" %s %-20s %s\n", marker, name, prof.URL)
			fmt.Printf("     status: unreachable (%v)\n", err)
			continue
		}
		authLabel := "unauthenticated"
		if st.Authenticated {
			authLabel = fmt.Sprintf("%s (%s)", st.User, st.Role)
		}
		fmt.Printf(" %s %-20s %s\n", marker, name, prof.URL)
		fmt.Printf("     status: %s, user: %s, tools: %d, systems: %d, running jobs: %d\n",
			strings.ToUpper(st.Status), authLabel, st.Tools, st.Systems, st.RunningJobs)
	}
	if cfg.Active == "" {
		fmt.Println("    (no active remote — `shipyard remote use <name>` sets the default)")
	}
	return nil
}

// saveConnectProfile persists the URL in config.yaml (profile name + active)
// and the secret credential in the 0600 auth store.
func saveConnectProfile(rawURL, profileName string, cred storedRemoteCred) (string, error) {
	name := strings.TrimSpace(profileName)
	if name == "" {
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Host != "" {
			name = parsed.Host
		} else {
			name = "default"
		}
	}
	if err := saveRemoteProfileAndCred(rawURL, name, cred); err != nil {
		return "", err
	}
	return name, nil
}

func resolveAndValidateLocalShipfile(target string) (string, string, error) {
	sfPath, err := resolveShipfilePath(target)
	if err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(sfPath)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", sfPath, err)
	}
	interp, err := getInterpreter()
	if err != nil {
		return "", "", fmt.Errorf("init interpreter: %w", err)
	}
	defer interp.Close()

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return "", "", fmt.Errorf("load %s: %w", sfPath, err)
	}
	if err := sf.Validate(); err != nil {
		return "", "", fmt.Errorf("validate %s: %w", sfPath, err)
	}
	return sfPath, string(raw), nil
}

func newRemoteRequest(method, fullURL, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func newRemoteCmd() *cobra.Command {
	var (
		remoteURL   string
		remoteToken string
	)

	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Connect to a remote Shipyard/Drydock server, push shipfiles, and stream or debug builds remotely",
	}
	cmd.PersistentFlags().StringVar(&remoteURL, "remote", "", "remote server URL or profile name ($SHIPYARD_REMOTE)")
	cmd.PersistentFlags().StringVar(&remoteToken, "token", "", "authentication bearer token ($SHIPYARD_TOKEN)")

	// 1. connect
	var profileName string
	var noBrowser bool
	connectCmd := &cobra.Command{
		Use:   "connect <url>",
		Short: "Connect and authenticate with a remote Shipyard/Drydock HTTP server (API token or OIDC browser login)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			rawURL := strings.TrimRight(strings.TrimSpace(args[0]), "/")
			if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
				rawURL = "http://" + rawURL
			}
			tok := strings.TrimSpace(remoteToken)
			if tok == "" {
				tok = firstEnv("SHIPYARD_TOKEN", "DRYDOCK_TOKEN")
			}
			if tok == "" {
				if cred := getStoredCred(rawURL); strings.TrimSpace(cred.APIToken) != "" {
					tok = strings.TrimSpace(cred.APIToken)
					fmt.Println("    Using saved credential from ~/.local/share/shipyard/auth.json...")
				}
			}

			fmt.Printf("[*] Connecting to remote server at %s...\n", rawURL)

			// Fast path: a bearer token is already available (flag, env,
			// or saved auth store). Verify it and save the profile.
			if tok != "" {
				fmt.Println("    Authenticating with provided bearer token...")
				st, err := queryRemoteStatus(rawURL, tok)
				if err != nil {
					return err
				}
				if !st.Authenticated {
					return fmt.Errorf("authentication failed: invalid API token")
				}
				cred := getStoredCred(rawURL)
				cred.APIToken = tok
				cred.Username = st.User
				cred.Role = st.Role
				name, err := saveConnectProfile(rawURL, profileName, cred)
				if err != nil {
					return err
				}
				fmt.Printf("[+] Successfully connected to %s (profile: %s, role: %s)\n", rawURL, name, st.Role)
				return nil
			}

			// No token: probe the server. An open/allowlisted server
			// answers 200 without auth; a locked one answers 401 with
			// {"error":"authentication required"}.
			if st, err := queryRemoteStatus(rawURL, ""); err == nil {
				name, serr := saveConnectProfile(rawURL, profileName, storedRemoteCred{Username: st.User, Role: st.Role})
				if serr != nil {
					return serr
				}
				fmt.Printf("[+] Successfully connected to %s (profile: %s, role: %s)\n", rawURL, name, st.Role)
				return nil
			}

			// Auth is required. Ask the server how to authenticate.
			ctx := c.Context()
			acfg, err := fetchRemoteAuthConfig(ctx, rawURL)
			if err != nil {
				return fmt.Errorf("authentication required, and auth discovery failed: %w\nHint: pass an API token with --token or $SHIPYARD_TOKEN (create one in the Drydock UI under Settings > API Keys)", err)
			}
			if !strings.EqualFold(strings.TrimSpace(acfg.Mode), "oidc") || strings.TrimSpace(acfg.OIDCIssuerURL) == "" {
				return fmt.Errorf("authentication required: server auth mode is %q (no browser login available). Pass an API token with --token or $SHIPYARD_TOKEN", acfg.Mode)
			}
			if noBrowser {
				return fmt.Errorf("authentication required: OIDC login needs a browser; re-run without --no-browser or pass --token")
			}
			cred, err := doOIDCLoopbackLogin(rawURL)
			if err != nil {
				return err
			}
			st, err := queryRemoteStatus(rawURL, cred.APIToken)
			if err != nil {
				return fmt.Errorf("browser login succeeded but the minted credential was rejected: %w", err)
			}
			if cred.Username == "" {
				cred.Username = st.User
			}
			if cred.Role == "" {
				cred.Role = st.Role
			}
			name, err := saveConnectProfile(rawURL, profileName, cred)
			if err != nil {
				return err
			}
			fmt.Printf("[+] Successfully connected to %s (profile: %s, user: %s, role: %s)\n", rawURL, name, st.User, st.Role)
			fmt.Println("    Credentials saved to ~/.local/share/shipyard/auth.json")
			return nil
		},
	}
	connectCmd.Flags().StringVar(&profileName, "name", "", "profile name to save as (defaults to host)")
	connectCmd.Flags().BoolVar(&noBrowser, "no-browser", false, "do not open a browser for OIDC login (fail if no API token is available)")

	// 2. status — with no selector, shows ALL remotes; with --remote <name>,
	// $SHIPYARD_REMOTE, or `status <name>`, shows that one in detail.
	statusCmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Check remote server health, auth status, and build state (all remotes by default)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			sel := strings.TrimSpace(remoteURL)
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				sel = strings.TrimSpace(args[0])
			}
			if _, explicit := explicitRemoteSelector(sel); explicit {
				return runRemoteStatusSingle(sel, remoteToken)
			}
			cfg, _ := loadRemoteConfig()
			if cfg == nil || len(cfg.Remotes) == 0 {
				// No saved remotes: single-shot against any URL from env/flags.
				return runRemoteStatusSingle(sel, remoteToken)
			}
			return runRemoteStatusAll(cfg)
		},
	}

	// 3. list (alias: ls) — shows ALL saved remotes.
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all saved remote server profiles (* = active)",
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			fmt.Println("Configured remotes:")
			if len(cfg.Remotes) == 0 {
				fmt.Println("  (no saved profiles found)")
				return nil
			}
			names := make([]string, 0, len(cfg.Remotes))
			for name := range cfg.Remotes {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				prof := cfg.Remotes[name]
				marker := " "
				if name == cfg.Active {
					marker = "*"
				}
				authLabel := "no-token"
				credNote := ""
				if prof.Token != "" {
					authLabel = "token"
				} else if cred := getStoredCred(prof.URL); strings.TrimSpace(cred.APIToken) != "" {
					authLabel = "token"
					if cred.Username != "" {
						credNote = " user:" + cred.Username
					}
				} else if cred := getStoredCred(name); strings.TrimSpace(cred.APIToken) != "" {
					authLabel = "token"
					if cred.Username != "" {
						credNote = " user:" + cred.Username
					}
				}
				fmt.Printf(" %s %-20s %s (%s%s)\n", marker, name, prof.URL, authLabel, credNote)
			}
			if cfg.Active == "" {
				fmt.Println(" * local                (local Dagger builds active)")
			}
			return nil
		},
	}

	// 4. use (alias: switch) — set the active remote. Accepts a profile
	// name, URL, or host; `use local` builds locally.
	runRemoteUse := func(args []string) error {
		cfg, err := loadRemoteConfig()
		if err != nil {
			return err
		}
		sel := strings.TrimSpace(args[0])
		if strings.EqualFold(sel, "local") || strings.EqualFold(sel, "none") || strings.EqualFold(sel, "off") {
			cfg.Active = ""
			if err := saveRemoteConfig(cfg); err != nil {
				return err
			}
			fmt.Println("[*] Switched active build target to: local")
			return nil
		}
		name, ok := findProfileByNameOrURL(cfg, sel)
		if !ok {
			if len(cfg.Remotes) == 0 {
				return fmt.Errorf("remote profile %q not found (no remotes configured yet — run `shipyard remote connect <url>`)", sel)
			}
			return fmt.Errorf("remote profile %q not found (available: %s)", sel, quotedRemoteNames(cfg))
		}
		cfg.Active = name
		if err := saveRemoteConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("[*] Switched active remote to: %s (%s)\n", name, cfg.Remotes[name].URL)
		return nil
	}
	useCmd := &cobra.Command{
		Use:     "use <name|url|local>",
		Aliases: []string{"switch"},
		Short:   "Set the active remote server profile (or 'local' to build locally)",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runRemoteUse(args)
		},
	}

	// 5. rm
	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a saved remote server profile and its stored credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			name := args[0]
			prof, ok := cfg.Remotes[name]
			if !ok {
				return fmt.Errorf("remote profile %q not found", name)
			}
			delete(cfg.Remotes, name)
			if cfg.Active == name {
				cfg.Active = defaultRemoteName(cfg)
			}
			if err := saveRemoteConfig(cfg); err != nil {
				return err
			}
			_ = clearStoredCred(prof.URL)
			_ = clearStoredCred(name)
			fmt.Printf("[*] Removed remote profile: %s\n", name)
			return nil
		},
	}

	// 5b. login (alias for connect without a URL: re-auth the active profile)
	loginCmd := &cobra.Command{
		Use:   "login [url]",
		Short: "Authenticate with the active remote (or given URL) via OIDC browser login or saved API token",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			target := strings.TrimSpace(remoteURL)
			if len(args) > 0 {
				target = strings.TrimSpace(args[0])
			}
			if target == "" {
				cfg, err := loadRemoteConfig()
				if err != nil {
					return err
				}
				if cfg.Active != "" {
					if prof, ok := cfg.Remotes[cfg.Active]; ok {
						target = prof.URL
					}
				}
			}
			if target == "" {
				return fmt.Errorf("no remote to log in to (pass a URL or run `shipyard remote connect <url>` first)")
			}
			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				target = "http://" + target
			}
			// Delegate to the connect flow by faking args.
			c.SetArgs([]string{target})
			return connectCmd.RunE(c, []string{target})
		},
	}

	// 5c. logout (clear stored credentials, keep the profile URL)
	logoutCmd := &cobra.Command{
		Use:   "logout [name]",
		Short: "Clear stored credentials for the active remote (or given profile)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = cfg.Active
			}
			if name == "" {
				return fmt.Errorf("no active remote (use `shipyard remote use <name>` first)")
			}
			prof, ok := cfg.Remotes[name]
			if !ok {
				return fmt.Errorf("remote profile %q not found", name)
			}
			_ = clearStoredCred(prof.URL)
			_ = clearStoredCred(name)
			prof.Token = ""
			cfg.Remotes[name] = prof
			if err := saveRemoteConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("[*] Logged out of %s (%s): stored credentials cleared\n", name, prof.URL)
			return nil
		},
	}

	// 5d. token (save an API token directly without a round-trip)
	var tokenValue string
	tokenCmd := &cobra.Command{
		Use:   "token [profile] --token <api-token>",
		Short: "Save an API token for a remote profile (no browser needed)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			tok := strings.TrimSpace(tokenValue)
			if tok == "" {
				tok = strings.TrimSpace(remoteToken)
			}
			if tok == "" {
				tok = firstEnv("SHIPYARD_TOKEN", "DRYDOCK_TOKEN")
			}
			if tok == "" {
				return fmt.Errorf("no token supplied (use --token, $SHIPYARD_TOKEN, or `shipyard remote connect` for OIDC login)")
			}
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			name := ""
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = cfg.Active
			}
			if name == "" {
				return fmt.Errorf("no active remote (use `shipyard remote use <name>` first or pass a profile name)")
			}
			prof, ok := cfg.Remotes[name]
			if !ok {
				return fmt.Errorf("remote profile %q not found", name)
			}
			st, err := queryRemoteStatus(prof.URL, tok)
			if err != nil {
				return fmt.Errorf("token verification failed: %w", err)
			}
			if !st.Authenticated {
				return fmt.Errorf("authentication failed: invalid API token")
			}
			cred := getStoredCred(prof.URL)
			cred.APIToken = tok
			cred.Username = st.User
			cred.Role = st.Role
			if err := saveRemoteProfileAndCred(prof.URL, name, cred); err != nil {
				return err
			}
			fmt.Printf("[+] Saved API token for %s (user: %s, role: %s)\n", name, st.User, st.Role)
			return nil
		},
	}
	tokenCmd.Flags().StringVar(&tokenValue, "token-value", "", "API token to save (or use --token / $SHIPYARD_TOKEN)")

	// 6. push
	var pushToolName string
	pushCmd := &cobra.Command{
		Use:   "push [shipfile]",
		Short: "Validate and send a shipfile.py to the remote server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := requireRemoteTarget(remoteURL, remoteToken)
			if err != nil {
				return err
			}
			target := ""
			if len(args) > 0 {
				target = args[0]
			}
			_, content, err := resolveAndValidateLocalShipfile(target)
			if err != nil {
				return err
			}

			payload, _ := json.Marshal(remotePushRequest{
				Name:     pushToolName,
				Shipfile: content,
			})
			req, err := newRemoteRequest(http.MethodPost, baseURL+"/api/remote/push", tok, bytes.NewReader(payload))
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("push shipfile: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				var errResp map[string]string
				_ = json.NewDecoder(resp.Body).Decode(&errResp)
				if errResp["error"] != "" {
					return fmt.Errorf("remote error: %s", errResp["error"])
				}
				return fmt.Errorf("remote returned HTTP %d", resp.StatusCode)
			}

			var res remotePushResponse
			if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
				return err
			}
			action := "Updated"
			if res.Created {
				action = "Created"
			}
			fmt.Printf("[+] %s tool %q (%s) on %s\n", action, res.Tool.Name, res.Type, baseURL)
			return nil
		},
	}
	pushCmd.Flags().StringVar(&pushToolName, "name", "", "override tool name on remote server")

	// 7. build
	var (
		rbName        string
		rbPackage     string
		rbPackageName string
		rbImage       string
		rbSystem      string
		rbVersion     string
		rbNoTest      bool
		rbVerbose     bool
		rbInteractive bool
		rbNoStream    bool
	)
	buildCmd := &cobra.Command{
		Use:   "build [shipfile]",
		Short: "Send a shipfile to the remote server, trigger a build, and stream logs or attach interactively",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := requireRemoteTarget(remoteURL, remoteToken)
			if err != nil {
				return err
			}

			var content string
			if len(args) > 0 || rbName == "" {
				target := ""
				if len(args) > 0 {
					target = args[0]
				}
				_, raw, err := resolveAndValidateLocalShipfile(target)
				if err != nil {
					return err
				}
				content = raw
			}

			var verbosePtr *bool
			if c.Flags().Changed("verbose") {
				v := rbVerbose
				verbosePtr = &v
			} else if globalVerbose {
				v := true
				verbosePtr = &v
			}

			reqBody := remoteBuildRequest{
				Name:         rbName,
				Shipfile:     content,
				System:       rbSystem,
				PackageImage: rbPackage,
				PackageName:  rbPackageName,
				Image:        rbImage,
				Version:      rbVersion,
				NoTest:       rbNoTest,
				Verbose:      verbosePtr,
				Stream:       !rbNoStream || rbInteractive,
				Interactive:  rbInteractive,
			}

			return executeRemoteBuildStream(baseURL, tok, reqBody)
		},
	}
	buildCmd.Flags().StringVar(&rbName, "name", "", "tool name on remote server")
	buildCmd.Flags().StringVarP(&rbPackage, "package", "p", "", "distro container image for package build (e.g. ubuntu:24.04)")
	buildCmd.Flags().StringVar(&rbPackageName, "package-name", "", "override package name")
	buildCmd.Flags().StringVarP(&rbImage, "image", "i", "", "base container image for generic build")
	buildCmd.Flags().StringVarP(&rbSystem, "system", "s", "", "target tracked system name or image")
	buildCmd.Flags().StringVarP(&rbVersion, "version", "v", "", "target version/tag to build")
	buildCmd.Flags().BoolVar(&rbNoTest, "no-test", false, "skip running tests defined in shipfile")
	buildCmd.Flags().BoolVarP(&rbVerbose, "verbose", "V", false, "enable verbose Shipyard build output")
	buildCmd.Flags().BoolVar(&rbInteractive, "interactive", false, "drop into interactive remote container shell on error")
	buildCmd.Flags().BoolVar(&rbNoStream, "no-stream", false, "trigger build asynchronously without streaming output")

	// 8. shell
	var (
		shName        string
		shPackage     string
		shPackageName string
		shImage       string
		shSystem      string
		shVersion     string
	)
	shellCmd := &cobra.Command{
		Use:   "shell [shipfile]",
		Short: "Open an interactive PTY shell inside a build container on the remote server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := requireRemoteTarget(remoteURL, remoteToken)
			if err != nil {
				return err
			}
			var content string
			if len(args) > 0 || shName == "" {
				target := ""
				if len(args) > 0 {
					target = args[0]
				}
				_, raw, err := resolveAndValidateLocalShipfile(target)
				if err != nil {
					return err
				}
				content = raw
			}

			reqBody := remoteBuildRequest{
				Name:         shName,
				Shipfile:     content,
				System:       shSystem,
				PackageImage: shPackage,
				PackageName:  shPackageName,
				Image:        shImage,
				Version:      shVersion,
				Stream:       true,
				Interactive:  true,
				ShellOnly:    true,
			}
			return executeRemoteBuildStream(baseURL, tok, reqBody)
		},
	}
	shellCmd.Flags().StringVar(&shName, "name", "", "tool name on remote server")
	shellCmd.Flags().StringVarP(&shPackage, "package", "p", "", "distro container image for package shell (e.g. ubuntu:24.04)")
	shellCmd.Flags().StringVar(&shPackageName, "package-name", "", "override package name")
	shellCmd.Flags().StringVarP(&shImage, "image", "i", "", "base container image for generic build shell")
	shellCmd.Flags().StringVarP(&shSystem, "system", "s", "", "target tracked system name or image")
	shellCmd.Flags().StringVarP(&shVersion, "version", "v", "", "target version/tag")

	// 9. logs
	logsCmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Stream live or completed output logs for a remote job",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := requireRemoteTarget(remoteURL, remoteToken)
			if err != nil {
				return err
			}
			jobID, err := strconv.ParseUint(strings.TrimSpace(args[0]), 10, 64)
			if err != nil || jobID == 0 {
				return fmt.Errorf("invalid job ID %q", args[0])
			}
			req, err := newRemoteRequest(http.MethodGet, fmt.Sprintf("%s/api/jobs/%d/stream", baseURL, jobID), tok, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}

	cmd.AddCommand(
		connectCmd,
		loginCmd,
		logoutCmd,
		tokenCmd,
		statusCmd,
		listCmd,
		useCmd,
		rmCmd,
		pushCmd,
		buildCmd,
		shellCmd,
		logsCmd,
	)
	return cmd
}

func executeRemoteBuildStream(baseURL, token string, reqBody remoteBuildRequest) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := newRemoteRequest(http.MethodPost, baseURL+"/api/remote/build", token, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote build request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if jsonErr := json.NewDecoder(resp.Body).Decode(&errResp); jsonErr == nil && errResp["error"] != "" {
			return fmt.Errorf("remote error: %s", errResp["error"])
		}
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if !reqBody.Stream {
		var res remoteBuildResponse
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return err
		}
		fmt.Printf("[+] Triggered remote build for %q (job #%d)\n", res.Tool.Name, res.JobID)
		if res.StreamURL != "" {
			fmt.Printf("    Stream logs with: shipyard remote logs %d\n", res.JobID)
		}
		return nil
	}

	reader := bufio.NewReader(resp.Body)
	var failed bool
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, interactiveShellPrefix) {
				sessionID := strings.TrimSpace(strings.TrimPrefix(trimmed, interactiveShellPrefix))
				fmt.Println("[!] Attaching to remote interactive container shell...")
				if shErr := attachRemoteShell(baseURL, token, sessionID); shErr != nil {
					fmt.Fprintf(os.Stderr, "[!] Remote shell error: %v\n", shErr)
				}
			} else {
				fmt.Print(line)
				if strings.HasPrefix(trimmed, "[-] Remote build failed:") {
					failed = true
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	if failed {
		return fmt.Errorf("remote build failed")
	}
	return nil
}

// --- Remote Terminal PTY Client ---

const (
	remoteShellMsgData   byte = 0
	remoteShellMsgResize byte = 1

	remoteIoctlTCGETS = 0x5401
	remoteIoctlTCSETS = 0x5402
)

type remoteTermios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [32]uint8
	Ispeed uint32
	Ospeed uint32
}

type remoteWinsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func makeRawTerminal(fd uintptr) (*remoteTermios, error) {
	var orig remoteTermios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, remoteIoctlTCGETS, uintptr(unsafe.Pointer(&orig))); errno != 0 {
		return nil, errno
	}
	raw := orig
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, remoteIoctlTCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return &orig, nil
}

func restoreTerminal(fd uintptr, orig *remoteTermios) {
	if orig == nil {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, remoteIoctlTCSETS, uintptr(unsafe.Pointer(orig)))
}

func currentTerminalSize(fd uintptr) (uint16, uint16) {
	var ws remoteWinsize
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Row > 0 && ws.Col > 0 {
		return ws.Row, ws.Col
	}
	return 24, 80
}

func writeRemoteShellFrame(mu *sync.Mutex, w io.Writer, msgType byte, payload []byte) error {
	mu.Lock()
	defer mu.Unlock()
	var hdr [5]byte
	hdr[0] = msgType
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func sendRemoteResizeFrame(mu *sync.Mutex, w io.Writer, fd uintptr) {
	rows, cols := currentTerminalSize(fd)
	var payload [4]byte
	binary.BigEndian.PutUint16(payload[0:2], rows)
	binary.BigEndian.PutUint16(payload[2:4], cols)
	_ = writeRemoteShellFrame(mu, w, remoteShellMsgResize, payload[:])
}

func remoteFdSet(fd int, set *syscall.FdSet) {
	set.Bits[fd/64] |= 1 << (uint(fd) % 64)
}

func remoteFdIsSet(fd int, set *syscall.FdSet) bool {
	return set.Bits[fd/64]&(1<<(uint(fd)%64)) != 0
}

func attachRemoteShell(baseURL, token, sessionID string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	host := parsed.Host
	if !strings.Contains(host, ":") {
		if parsed.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	var conn net.Conn
	if parsed.Scheme == "https" {
		conn, err = tls.Dial("tcp", host, &tls.Config{})
	} else {
		conn, err = net.Dial("tcp", host)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", host, err)
	}
	defer conn.Close()

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}
	path := strings.TrimRight(parsed.Path, "/") + "/api/remote/shell/" + sessionID
	var reqLines strings.Builder
	fmt.Fprintf(&reqLines, "POST %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&reqLines, "Host: %s\r\n", parsed.Host)
	fmt.Fprintf(&reqLines, "X-Term: %s\r\n", term)
	if token != "" {
		fmt.Fprintf(&reqLines, "Authorization: Bearer %s\r\n", token)
	}
	reqLines.WriteString("\r\n")

	if _, err := conn.Write([]byte(reqLines.String())); err != nil {
		return err
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read shell handshake: %w", err)
	}
	if !strings.Contains(statusLine, "200") {
		return fmt.Errorf("remote shell rejected: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	stdinFd := os.Stdin.Fd()
	origTerm, _ := makeRawTerminal(stdinFd)
	defer restoreTerminal(stdinFd, origTerm)

	var writeMu sync.Mutex
	sendRemoteResizeFrame(&writeMu, conn, stdinFd)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sigCh:
				sendRemoteResizeFrame(&writeMu, conn, stdinFd)
			case <-done:
				return
			}
		}
	}()

	cancelR, cancelW, pipeErr := os.Pipe()
	if pipeErr == nil {
		defer cancelR.Close()
		defer cancelW.Close()
	}

	go func() {
		buf := make([]byte, 4096)
		inFd := int(stdinFd)
		for {
			if pipeErr == nil {
				cFd := int(cancelR.Fd())
				var rfds syscall.FdSet
				remoteFdSet(inFd, &rfds)
				remoteFdSet(cFd, &rfds)
				maxFd := inFd
				if cFd > maxFd {
					maxFd = cFd
				}
				n, selErr := syscall.Select(maxFd+1, &rfds, nil, nil, nil)
				if selErr != nil {
					if selErr == syscall.EINTR {
						continue
					}
					return
				}
				if n <= 0 || remoteFdIsSet(cFd, &rfds) {
					return
				}
			}
			nr, rerr := os.Stdin.Read(buf)
			if nr > 0 {
				if werr := writeRemoteShellFrame(&writeMu, conn, remoteShellMsgData, buf[:nr]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	_, _ = io.Copy(os.Stdout, br)
	if pipeErr == nil {
		_ = cancelW.Close()
	}
	return nil
}
