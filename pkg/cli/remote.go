package cli

import (
	"bufio"
	"bytes"
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
	Name            string `json:"name"`
	Shipfile        string `json:"shipfile"`
	System          string `json:"system"`
	SystemID        uint   `json:"system_id"`
	PackageImage    string `json:"package"`
	PackageName     string `json:"package_name"`
	Image           string `json:"image"`
	Version         string `json:"version"`
	ArtifactPattern string `json:"artifacts"`
	NoTest          bool   `json:"no_test"`
	Verbose         *bool  `json:"verbose,omitempty"`
	Stream          bool   `json:"stream"`
	Interactive     bool   `json:"interactive"`
	ShellOnly       bool   `json:"shell_only"`
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
	connectCmd := &cobra.Command{
		Use:   "connect <url>",
		Short: "Connect and authenticate with a remote Shipyard/Drydock HTTP server",
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

			fmt.Printf("[*] Connecting to remote server at %s...\n", rawURL)
			if tok != "" {
				fmt.Println("    Authenticating with provided bearer token...")
			}

			req, err := newRemoteRequest(http.MethodGet, rawURL+"/api/remote/status", tok, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("connect to %s: %w", rawURL, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}

			var st remoteStatusResponse
			if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
				return fmt.Errorf("decode status response: %w", err)
			}
			if tok != "" && !st.Authenticated {
				return fmt.Errorf("authentication failed: invalid API token")
			}

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
			cfg.Remotes[name] = remoteProfile{
				URL:   rawURL,
				Token: tok,
			}
			cfg.Active = name
			if err := saveRemoteConfig(cfg); err != nil {
				return fmt.Errorf("save remote config: %w", err)
			}

			fmt.Printf("[+] Successfully connected to %s (profile: %s, role: %s)\n", rawURL, name, st.Role)
			return nil
		},
	}
	connectCmd.Flags().StringVar(&profileName, "name", "", "profile name to save as (defaults to host)")

	// 2. status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check remote server health, auth status, and build state",
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := resolveRemoteEndpoint(remoteURL, remoteToken)
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
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
		},
	}

	// 3. list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved remote server profiles",
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
				if prof.Token != "" {
					authLabel = "token"
				}
				fmt.Printf(" %s %-20s %s (%s)\n", marker, name, prof.URL, authLabel)
			}
			if cfg.Active == "" {
				fmt.Println(" * local                (local Dagger builds active)")
			}
			return nil
		},
	}

	// 4. use
	useCmd := &cobra.Command{
		Use:   "use <name|local>",
		Short: "Set the active remote server profile (or 'local' to build locally)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if name == "local" || name == "none" || name == "off" {
				cfg.Active = ""
				if err := saveRemoteConfig(cfg); err != nil {
					return err
				}
				fmt.Println("[*] Switched active build target to: local")
				return nil
			}
			if _, ok := cfg.Remotes[name]; !ok {
				return fmt.Errorf("remote profile %q not found", name)
			}
			cfg.Active = name
			if err := saveRemoteConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("[*] Switched active remote to: %s (%s)\n", name, cfg.Remotes[name].URL)
			return nil
		},
	}

	// 5. rm
	rmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a saved remote server profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := cfg.Remotes[name]; !ok {
				return fmt.Errorf("remote profile %q not found", name)
			}
			delete(cfg.Remotes, name)
			if cfg.Active == name {
				cfg.Active = ""
				for k := range cfg.Remotes {
					cfg.Active = k
					break
				}
			}
			if err := saveRemoteConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("[*] Removed remote profile: %s\n", name)
			return nil
		},
	}

	// 6. push
	var pushToolName string
	pushCmd := &cobra.Command{
		Use:   "push [shipfile]",
		Short: "Validate and send a shipfile.py to the remote server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			baseURL, tok, err := resolveRemoteEndpoint(remoteURL, remoteToken)
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
		rbArtifacts   string
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
			baseURL, tok, err := resolveRemoteEndpoint(remoteURL, remoteToken)
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
				Name:            rbName,
				Shipfile:        content,
				System:          rbSystem,
				PackageImage:    rbPackage,
				PackageName:     rbPackageName,
				Image:           rbImage,
				Version:         rbVersion,
				ArtifactPattern: rbArtifacts,
				NoTest:          rbNoTest,
				Verbose:         verbosePtr,
				Stream:          !rbNoStream || rbInteractive,
				Interactive:     rbInteractive,
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
	buildCmd.Flags().StringVarP(&rbArtifacts, "artifacts", "a", "", "artifact regex pattern")
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
			baseURL, tok, err := resolveRemoteEndpoint(remoteURL, remoteToken)
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
			baseURL, tok, err := resolveRemoteEndpoint(remoteURL, remoteToken)
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
