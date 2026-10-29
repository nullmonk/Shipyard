package cli

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestNormalizeAuthKey(t *testing.T) {
	cases := map[string]string{
		"https://build.linuxservers.uk": "build.linuxservers.uk",
		"build.linuxservers.uk":         "build.linuxservers.uk",
		"http://127.0.0.1:8080/foo":     "127.0.0.1:8080",
	}
	for in, want := range cases {
		if got := normalizeAuthKey(in); got != want {
			t.Errorf("normalizeAuthKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodeChallengeRoundTrip(t *testing.T) {
	v, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("generateCodeVerifier: %v", err)
	}
	if len(v) < 43 {
		t.Fatalf("verifier too short: %q", v)
	}
	ch := codeChallengeS256(v)
	if ch == "" || ch == v {
		t.Fatalf("bad challenge: %q", ch)
	}
}

func TestAuthStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("SHIPYARD_AUTH_FILE", "")
	t.Setenv("SHIPYARD_CONFIG", dir+"/config.yaml")

	rawURL := "https://build.linuxservers.uk"
	cred := storedRemoteCred{APIToken: "ddk_test123", Username: "alice", Role: "admin", AccessToken: "acc", RefreshToken: "ref"}
	if err := setStoredCred(rawURL, cred); err != nil {
		t.Fatalf("setStoredCred: %v", err)
	}
	p, _ := authStorePath()
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("auth file missing at %s: %v", p, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("auth file perms = %o, want 600", fi.Mode().Perm())
	}
	for _, key := range []string{rawURL, "build.linuxservers.uk", "https://build.linuxservers.uk/"} {
		got := getStoredCred(key)
		if got.APIToken != "ddk_test123" {
			t.Errorf("getStoredCred(%q).APIToken = %q", key, got.APIToken)
		}
		if got.RefreshToken != "ref" {
			t.Errorf("getStoredCred(%q) lost oauth tokens: %+v", key, got)
		}
	}
	t.Setenv("SHIPYARD_REMOTE", "")
	t.Setenv("DRYDOCK_REMOTE", "")
	t.Setenv("SHIPYARD_TOKEN", "")
	t.Setenv("DRYDOCK_TOKEN", "")
	u, tok, err := resolveRemoteEndpoint(rawURL, "")
	if err != nil {
		t.Fatalf("resolveRemoteEndpoint: %v", err)
	}
	if u != rawURL || tok != "ddk_test123" {
		t.Errorf("resolve = (%q, %q), want (%q, ddk_test123)", u, tok, rawURL)
	}
	if err := clearStoredCred(rawURL); err != nil {
		t.Fatalf("clearStoredCred: %v", err)
	}
	if got := getStoredCred(rawURL); got.APIToken != "" {
		t.Errorf("want cleared cred, got %+v", got)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %d never opened", port)
}

func TestWaitForLoopbackCode(t *testing.T) {
	port := freeLoopbackPort(t)
	state := "test-state-123"
	type res struct {
		code string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		code, err := waitForLoopbackCode(port, state, 10*time.Second)
		ch <- res{code, err}
	}()
	// Retry the GET until the HTTP server is up (no raw dial: it would
	// hold a connection open on the one-shot listener).
	var resp *http.Response
	var err error
	for i := 0; i < 100; i++ {
		resp, err = http.Get("http://127.0.0.1:" + fmt.Sprint(port) + "/shipyard/callback?code=authcode-1&state=" + url.QueryEscape(state))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("waitForLoopbackCode: %v", r.err)
		}
		if r.code != "authcode-1" {
			t.Errorf("code = %q, want authcode-1", r.code)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out")
	}
}

func TestWaitForLoopbackRejectsBadState(t *testing.T) {
	port := freeLoopbackPort(t)
	done := make(chan error, 1)
	go func() {
		_, err := waitForLoopbackCode(port, "right-state", 10*time.Second)
		done <- err
	}()
	var resp *http.Response
	var err error
	for i := 0; i < 100; i++ {
		resp, err = http.Get("http://127.0.0.1:" + fmt.Sprint(port) + "/shipyard/callback?code=x&state=wrong-state")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("want state mismatch error, got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for state rejection")
	}
}
