package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// testRemoteEnv isolates remote resolution from the ambient environment.
func testRemoteEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SHIPYARD_CONFIG", dir+"/config.yaml")
	t.Setenv("SHIPYARD_AUTH_FILE", dir+"/auth.json")
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("SHIPYARD_REMOTE", "")
	t.Setenv("DRYDOCK_REMOTE", "")
	t.Setenv("SHIPYARD_TOKEN", "")
	t.Setenv("DRYDOCK_TOKEN", "")
	oldGlobal := globalRemote
	globalRemote = ""
	t.Cleanup(func() { globalRemote = oldGlobal })
	return dir
}

func saveTestRemotes(t *testing.T, active string, urls map[string]string) {
	t.Helper()
	cfg := &remoteConfig{Active: active, Remotes: make(map[string]remoteProfile)}
	for name, u := range urls {
		cfg.Remotes[name] = remoteProfile{URL: u}
	}
	if err := saveRemoteConfig(cfg); err != nil {
		t.Fatalf("saveRemoteConfig: %v", err)
	}
}

func TestDefaultRemoteName(t *testing.T) {
	cfg := &remoteConfig{Remotes: map[string]remoteProfile{
		"beta":  {URL: "http://b"},
		"alpha": {URL: "http://a"},
	}}
	if got := defaultRemoteName(cfg); got != "alpha" {
		t.Errorf("no active: got %q, want alpha (first sorted)", got)
	}
	cfg.Active = "beta"
	if got := defaultRemoteName(cfg); got != "beta" {
		t.Errorf("active beta: got %q, want beta", got)
	}
	cfg.Active = "ghost"
	if got := defaultRemoteName(cfg); got != "alpha" {
		t.Errorf("stale active: got %q, want alpha fallback", got)
	}
	if got := defaultRemoteName(&remoteConfig{Remotes: map[string]remoteProfile{}}); got != "" {
		t.Errorf("empty: got %q, want \"\"", got)
	}
}

func TestFindProfileByNameOrURL(t *testing.T) {
	cfg := &remoteConfig{Remotes: map[string]remoteProfile{
		"prod": {URL: "https://build.example.com"},
		"dev":  {URL: "http://127.0.0.1:8080"},
	}}
	for sel, want := range map[string]string{
		"prod":                          "prod",
		"https://build.example.com":     "prod",
		"https://build.example.com/":    "prod",
		"build.example.com":             "prod",
		"dev":                           "dev",
		"127.0.0.1:8080":                "dev",
		"http://127.0.0.1:8080/api/req": "dev",
	} {
		if got, ok := findProfileByNameOrURL(cfg, sel); !ok || got != want {
			t.Errorf("find(%q) = (%q, %v), want (%q, true)", sel, got, ok, want)
		}
	}
	if _, ok := findProfileByNameOrURL(cfg, "nope"); ok {
		t.Errorf("find(nope) unexpectedly matched")
	}
}

func TestResolveRemoteTargetSingleDefaults(t *testing.T) {
	testRemoteEnv(t)
	saveTestRemotes(t, "", map[string]string{"only": "http://127.0.0.1:18081"})

	prof, u, _, err := resolveRemoteTarget("", "")
	if err != nil {
		t.Fatalf("single remote: %v", err)
	}
	if prof != "only" || u != "http://127.0.0.1:18081" {
		t.Errorf("got (%q, %q), want (only, url)", prof, u)
	}
}

func TestResolveRemoteTargetMultiActiveDefault(t *testing.T) {
	testRemoteEnv(t)
	saveTestRemotes(t, "prod", map[string]string{
		"prod": "https://build.example.com",
		"dev":  "http://127.0.0.1:8080",
	})

	prof, u, _, err := resolveRemoteTarget("", "")
	if err != nil {
		t.Fatalf("multi with active: %v", err)
	}
	if prof != "prod" || u != "https://build.example.com" {
		t.Errorf("got (%q, %q), want active prod", prof, u)
	}
}

func TestResolveRemoteTargetMultiRequiresChoice(t *testing.T) {
	testRemoteEnv(t)
	saveTestRemotes(t, "", map[string]string{
		"prod": "https://build.example.com",
		"dev":  "http://127.0.0.1:8080",
	})

	_, _, _, err := resolveRemoteTarget("", "")
	if err == nil {
		t.Fatalf("want error when multiple remotes and no selection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--remote") {
		t.Errorf("error should mention --remote, got: %s", msg)
	}
	for _, n := range []string{"prod", "dev"} {
		if !strings.Contains(msg, n) {
			t.Errorf("error should list %q, got: %s", n, msg)
		}
	}
}

func TestResolveRemoteTargetExplicitBeatsActive(t *testing.T) {
	testRemoteEnv(t)
	saveTestRemotes(t, "prod", map[string]string{
		"prod": "https://build.example.com",
		"dev":  "http://127.0.0.1:8080",
	})

	prof, u, _, err := resolveRemoteTarget("dev", "")
	if err != nil {
		t.Fatalf("explicit dev: %v", err)
	}
	if prof != "dev" || u != "http://127.0.0.1:8080" {
		t.Errorf("got (%q, %q), want dev", prof, u)
	}

	// Explicit URL matching a saved profile resolves to that profile.
	prof, u, _, err = resolveRemoteTarget("https://build.example.com/", "")
	if err != nil {
		t.Fatalf("explicit url: %v", err)
	}
	if prof != "prod" {
		t.Errorf("got profile %q, want prod", prof)
	}

	// Ad-hoc URL with no saved profile still resolves (empty profile).
	prof, u, _, err = resolveRemoteTarget("https://new.example.com", "")
	if err != nil {
		t.Fatalf("ad-hoc url: %v", err)
	}
	if prof != "" || u != "https://new.example.com" {
		t.Errorf("got (%q, %q), want (\"\", ad-hoc url)", prof, u)
	}

	// Env counts as explicit too.
	t.Setenv("SHIPYARD_REMOTE", "dev")
	prof, _, _, err = resolveRemoteTarget("", "")
	if err != nil {
		t.Fatalf("env selector: %v", err)
	}
	if prof != "dev" {
		t.Errorf("got profile %q, want dev via env", prof)
	}
}

func TestRemoteStatusAllListsEveryRemote(t *testing.T) {
	testRemoteEnv(t)

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(remoteStatusResponse{
			Status: "ok", Service: "drydock", Authenticated: true,
			User: "alice", Role: "admin", Tools: 3, Systems: 2,
		})
	}))
	defer okSrv.Close()
	downSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"authentication required"}`))
	}))
	defer downSrv.Close()

	saveTestRemotes(t, "good", map[string]string{"good": okSrv.URL, "bad": downSrv.URL})

	cfg, err := loadRemoteConfig()
	if err != nil {
		t.Fatalf("loadRemoteConfig: %v", err)
	}

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	runErr := runRemoteStatusAll(cfg)
	_ = w.Close()
	os.Stdout = old
	out := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	if runErr != nil {
		t.Fatalf("runRemoteStatusAll: %v", runErr)
	}
	got := string(out)
	for _, want := range []string{"good", "bad", okSrv.URL, downSrv.URL, "alice", "admin"} {
		if !strings.Contains(got, want) {
			t.Errorf("status-all output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "* good") {
		t.Errorf("active remote should be marked with *, got:\n%s", got)
	}
}

func TestRemoteUseAcceptsURLAndSwitchAlias(t *testing.T) {
	testRemoteEnv(t)
	saveTestRemotes(t, "", map[string]string{
		"prod": "https://build.example.com",
		"dev":  "http://127.0.0.1:8080",
	})

	root := newRemoteCmd()
	var useCmd, switchCmd *string
	_ = useCmd
	_ = switchCmd
	// find the use command and exercise its RunE via URL + host selectors
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "use" {
			found = true
			hasSwitch := false
			for _, a := range c.Aliases {
				if a == "switch" {
					hasSwitch = true
				}
			}
			if !hasSwitch {
				t.Errorf("use command missing switch alias (aliases=%v)", c.Aliases)
			}
			if err := c.RunE(c, []string{"https://build.example.com/"}); err != nil {
				t.Fatalf("use by URL: %v", err)
			}
			cfg, _ := loadRemoteConfig()
			if cfg.Active != "prod" {
				t.Errorf("use by URL: active=%q, want prod", cfg.Active)
			}
			if err := c.RunE(c, []string{"127.0.0.1:8080"}); err != nil {
				t.Fatalf("use by host: %v", err)
			}
			cfg, _ = loadRemoteConfig()
			if cfg.Active != "dev" {
				t.Errorf("use by host: active=%q, want dev", cfg.Active)
			}
			if err := c.RunE(c, []string{"nope"}); err == nil {
				t.Errorf("use unknown: want error, got nil")
			} else if !strings.Contains(fmt.Sprint(err), "prod") {
				t.Errorf("use unknown error should list available, got: %v", err)
			}
		}
	}
	if !found {
		t.Fatalf("use command not found")
	}
}
