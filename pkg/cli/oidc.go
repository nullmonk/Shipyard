package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// CLI loopback defaults. 9876 is the registered default: the IdP client
// must allow http://127.0.0.1:9876/shipyard/callback as a redirect URI.
const (
	cliCallbackPort = 9876
	cliCallbackPath = "/shipyard/callback"
)

// remoteAuthConfig mirrors the server's GET /api/remote/auth-config.
type remoteAuthConfig struct {
	Mode          string `json:"mode"`
	OIDCIssuerURL string `json:"oidc_issuer_url"`
	OIDCClientID  string `json:"oidc_client_id"`
	OIDCScopes    string `json:"oidc_scopes"`
}

// remoteCLIExchangeReq mirrors POST /api/remote/cli-exchange.
type remoteCLIExchangeReq struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

// remoteCLIExchangeResp mirrors the server response: a mint API key plus
// the OAuth tokens for local storage.
type remoteCLIExchangeResp struct {
	APIToken     string `json:"api_token"`
	User         string `json:"user"`
	Role         string `json:"role"`
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type oidcDiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func generateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func fetchRemoteAuthConfig(ctx context.Context, baseURL string) (*remoteAuthConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/remote/auth-config", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch auth config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("auth config returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var cfg remoteAuthConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode auth config: %w", err)
	}
	return &cfg, nil
}

func fetchDiscovery(ctx context.Context, issuer string) (*oidcDiscoveryDoc, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return nil, fmt.Errorf("empty OIDC issuer")
	}
	u := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("OIDC discovery returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc oidcDiscoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode OIDC discovery: %w", err)
	}
	if doc.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery missing authorization_endpoint")
	}
	return &doc, nil
}

func openBrowser(rawURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	_ = cmd.Start()
}

// waitForLoopbackCode runs a one-shot HTTP server on 127.0.0.1 that waits
// for the IdP redirect (GET ?code=...&state=...). It replies with a small
// success page, then returns the code.
func waitForLoopbackCode(port int, wantState string, timeout time.Duration) (string, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w (is another shipyard login running?)", addr, err)
	}
	defer ln.Close()

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if r.URL.Path != cliCallbackPath {
				http.NotFound(w, r)
				return
			}
			if errParam := q.Get("error"); errParam != "" {
				desc := q.Get("error_description")
				http.Error(w, fmt.Sprintf("OIDC error: %s %s", errParam, desc), http.StatusBadRequest)
				select {
				case ch <- result{"", fmt.Errorf("OIDC error from IdP: %s %s", errParam, desc)}:
				default:
				}
				return
			}
			gotState := q.Get("state")
			code := q.Get("code")
			if gotState != wantState {
				http.Error(w, "invalid state parameter", http.StatusBadRequest)
				select {
				case ch <- result{"", fmt.Errorf("invalid state parameter (possible CSRF); try again")}:
				default:
				}
				return
			}
			if code == "" {
				http.Error(w, "missing code parameter", http.StatusBadRequest)
				select {
				case ch <- result{"", fmt.Errorf("missing code parameter in callback")}:
				default:
				}
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><body style="font-family:sans-serif;text-align:center;padding-top:4em"><h2>Authentication successful</h2><p>You can close this window and return to the terminal.</p></body></html>`)
			select {
			case ch <- result{code, nil}:
			default:
			}
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	select {
	case res := <-ch:
		return res.code, res.err
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out waiting for browser login (no callback within %s)", timeout.Round(time.Second))
	}
}

// doOIDCLoopbackLogin performs the full browser PKCE flow against the IdP
// and exchanges the resulting code for a Drydock API key via the server.
// It returns the API credential plus OAuth tokens for local storage.
func doOIDCLoopbackLogin(baseURL string) (storedRemoteCred, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := fetchRemoteAuthConfig(ctx, baseURL)
	if err != nil {
		return storedRemoteCred{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Mode), "oidc") {
		return storedRemoteCred{}, fmt.Errorf("server auth mode is %q (not OIDC); provide an API token with --token or $SHIPYARD_TOKEN", cfg.Mode)
	}
	if strings.TrimSpace(cfg.OIDCIssuerURL) == "" || strings.TrimSpace(cfg.OIDCClientID) == "" {
		return storedRemoteCred{}, fmt.Errorf("server OIDC is not configured (missing issuer/client); ask an admin for an API token")
	}
	scopes := strings.TrimSpace(cfg.OIDCScopes)
	if scopes == "" {
		scopes = "openid profile email offline_access groups"
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer dcancel()
	doc, err := fetchDiscovery(dctx, cfg.OIDCIssuerURL)
	if err != nil {
		return storedRemoteCred{}, err
	}

	verifier, err := generateCodeVerifier()
	if err != nil {
		return storedRemoteCred{}, err
	}
	state, err := generateState()
	if err != nil {
		return storedRemoteCred{}, err
	}
	challenge := codeChallengeS256(verifier)
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", cliCallbackPort, cliCallbackPath)

	v := url.Values{}
	v.Set("client_id", strings.TrimSpace(cfg.OIDCClientID))
	v.Set("redirect_uri", redirectURI)
	v.Set("response_type", "code")
	v.Set("scope", scopes)
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(doc.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	authURL := doc.AuthorizationEndpoint + sep + v.Encode()

	fmt.Println("[*] OIDC authentication required. Opening browser to sign in...")
	fmt.Println("    If the browser does not open automatically, visit:")
	fmt.Printf("    %s\n", authURL)
	openBrowser(authURL)

	code, err := waitForLoopbackCode(cliCallbackPort, state, 5*time.Minute)
	if err != nil {
		return storedRemoteCred{}, err
	}
	fmt.Println("[+] Browser login complete, exchanging code with server...")

	exchBody, _ := json.Marshal(remoteCLIExchangeReq{
		Code:         code,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
	})
	exchCtx, exchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer exchCancel()
	req, err := http.NewRequestWithContext(exchCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/remote/cli-exchange", strings.NewReader(string(exchBody)))
	if err != nil {
		return storedRemoteCred{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return storedRemoteCred{}, fmt.Errorf("code exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody["error"] != "" {
			return storedRemoteCred{}, fmt.Errorf("code exchange failed: %s", errBody["error"])
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return storedRemoteCred{}, fmt.Errorf("code exchange returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var exch remoteCLIExchangeResp
	if err := json.NewDecoder(resp.Body).Decode(&exch); err != nil {
		return storedRemoteCred{}, fmt.Errorf("decode exchange response: %w", err)
	}
	if strings.TrimSpace(exch.APIToken) == "" {
		return storedRemoteCred{}, fmt.Errorf("server returned no API token")
	}
	cred := storedRemoteCred{
		APIToken:     strings.TrimSpace(exch.APIToken),
		TokenType:    exch.TokenType,
		AccessToken:  exch.AccessToken,
		IDToken:      exch.IDToken,
		RefreshToken: exch.RefreshToken,
		ExpiresIn:    exch.ExpiresIn,
		Username:     exch.User,
		Role:         exch.Role,
	}
	if cred.ExpiresIn > 0 {
		// Store an approximate absolute expiry for the OAuth access token.
		cred.ExpiresAt = time.Now().Add(time.Duration(cred.ExpiresIn) * time.Second)
	}
	return cred, nil
}
