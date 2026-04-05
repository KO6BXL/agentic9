package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentic9/internal/config"
	"agentic9/internal/exportfs"
	"agentic9/internal/transport/tlsrcpu"
	"agentic9/internal/workspace"
)

func TestVerifyAndExecAgainstRealHost(t *testing.T) {
	fixture, ok := loadIntegrationFixture()
	if !ok {
		t.Skip(skipMessage())
	}

	client := tlsrcpu.NewClient(fixture.Profile, fixture.Secret)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	const marker = "agentic9-integration-ok"
	var out strings.Builder
	if err := client.Exec(ctx, "echo "+marker+"\n", func(chunk []byte) error {
		_, _ = out.Write(chunk)
		return nil
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := out.String(); !strings.Contains(got, marker) {
		t.Fatalf("output %q does not contain %q", got, marker)
	}
}

func TestExportFSRoundTripAgainstRealHost(t *testing.T) {
	fixture, ok := loadIntegrationFixture()
	if !ok {
		t.Skip(skipMessage())
	}

	client := tlsrcpu.NewClient(fixture.Profile, fixture.Secret)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	agentID := uniqueIntegrationID()
	remoteRoot := path.Join(fixture.Profile.RemoteBase, agentID, "root")
	if err := client.EnsureRemoteDir(ctx, remoteRoot); err != nil {
		t.Fatalf("EnsureRemoteDir: %v", err)
	}
	defer func() {
		_ = client.RemoveRemoteTree(context.Background(), path.Dir(remoteRoot))
	}()

	fs := exportfs.New(client, remoteRoot)
	payload := []byte("hello from integration\n")
	handle, entry, err := fs.Create(ctx, "/hello.txt", 0o644, uint32(os.O_WRONLY|os.O_TRUNC))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if entry.Path != "/hello.txt" {
		t.Fatalf("Create entry path = %q, want /hello.txt", entry.Path)
	}
	if n, err := handle.WriteAt(payload, 0); err != nil {
		_ = handle.Close()
		t.Fatalf("WriteAt: %v", err)
	} else if n != len(payload) {
		_ = handle.Close()
		t.Fatalf("WriteAt bytes = %d, want %d", n, len(payload))
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close after Create: %v", err)
	}

	stat, err := fs.Stat(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Size != uint64(len(payload)) {
		t.Fatalf("Stat size = %d, want %d", stat.Size, len(payload))
	}

	readHandle, _, err := fs.Open(ctx, "/hello.txt", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	buf := make([]byte, len(payload))
	if n, err := readHandle.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		_ = readHandle.Close()
		t.Fatalf("ReadAt: %v", err)
	} else if n != len(payload) {
		_ = readHandle.Close()
		t.Fatalf("ReadAt bytes = %d, want %d", n, len(payload))
	}
	if err := readHandle.Close(); err != nil {
		t.Fatalf("Close after Open: %v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("ReadAt data = %q, want %q", string(buf), string(payload))
	}

	if err := fs.Rename(ctx, "/hello.txt", "/renamed.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := fs.Stat(ctx, "/renamed.txt"); err != nil {
		t.Fatalf("Stat renamed: %v", err)
	}
	entries, err := fs.List(ctx, "/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsPath(entries, "/renamed.txt") {
		t.Fatalf("List did not include /renamed.txt: %#v", entries)
	}
}

func TestWorkspaceCreateDeleteAgainstRealHost(t *testing.T) {
	if os.Getenv("AGENTIC9_IT_WORKSPACE") == "" {
		t.Skip("set AGENTIC9_IT_WORKSPACE=1 to run the real-host workspace lifecycle test")
	}
	fixture, ok := loadIntegrationFixture()
	if !ok || fixture.ProfileName == "" {
		t.Skip(skipMessage())
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("/dev/fuse is not available on this host")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	runtimeDir := t.TempDir()
	env := map[string]string{
		"XDG_RUNTIME_DIR": runtimeDir,
	}

	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "hello.txt"), []byte("workspace integration\n"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	agentID := uniqueIntegrationID()
	var createResp struct {
		OK                bool   `json:"ok"`
		ProjectRoot       string `json:"project_root"`
		Mountpoint        string `json:"mountpoint"`
		RemoteProjectRoot string `json:"remote_project_root"`
		SeedPath          string `json:"seed_path"`
		Mounted           bool   `json:"mounted"`
	}
	if err := runCLIJSONWithEnv(ctx, env, &createResp, "workspace", "create", "--profile", fixture.ProfileName, "--agent-id", agentID, "--seed-path", sourceDir, "--json"); err != nil {
		t.Fatalf("workspace create: %v", err)
	}
	defer func() {
		var deleteResp any
		_ = runCLIJSONWithEnv(context.Background(), env, &deleteResp, "workspace", "delete", "--profile", fixture.ProfileName, "--agent-id", agentID, "--json")
	}()
	if !createResp.OK || !createResp.Mounted || createResp.ProjectRoot == "" || createResp.Mountpoint != createResp.ProjectRoot {
		t.Fatalf("unexpected create response: %#v", createResp)
	}
	if createResp.RemoteProjectRoot == "" || createResp.SeedPath != sourceDir {
		t.Fatalf("unexpected project-root metadata: %#v", createResp)
	}

	data, err := os.ReadFile(filepath.Join(createResp.ProjectRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile mounted workspace: %v", err)
	}
	if string(data) != "workspace integration\n" {
		t.Fatalf("mounted file = %q", string(data))
	}

	publicDir := filepath.Join(createResp.ProjectRoot, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("MkdirAll public: %v", err)
	}
	indexPath := filepath.Join(publicDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<h1>nested write</h1>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}
	if err := os.WriteFile(indexPath, []byte("<h1>nested overwrite</h1>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile nested overwrite: %v", err)
	}
	renamedPath := filepath.Join(publicDir, "home.html")
	if err := os.Rename(indexPath, renamedPath); err != nil {
		t.Fatalf("Rename nested file: %v", err)
	}
	if _, err := os.Stat(renamedPath); err != nil {
		t.Fatalf("Stat renamed nested file: %v", err)
	}
	if err := os.Remove(renamedPath); err != nil {
		t.Fatalf("Remove renamed nested file: %v", err)
	}
	if err := os.WriteFile(indexPath, []byte("<h1>nested recreate</h1>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile nested recreate: %v", err)
	}

	var pathResp struct {
		OK                bool   `json:"ok"`
		ProjectRoot       string `json:"project_root"`
		Mountpoint        string `json:"mountpoint"`
		RemoteProjectRoot string `json:"remote_project_root"`
		Mounted           bool   `json:"mounted"`
	}
	if err := runCLIJSONWithEnv(ctx, env, &pathResp, "workspace", "path", "--profile", fixture.ProfileName, "--agent-id", agentID, "--json"); err != nil {
		t.Fatalf("workspace path: %v", err)
	}
	if !pathResp.OK || !pathResp.Mounted || pathResp.ProjectRoot != createResp.ProjectRoot || pathResp.Mountpoint != createResp.Mountpoint || pathResp.RemoteProjectRoot != createResp.RemoteProjectRoot {
		t.Fatalf("unexpected path response: %#v", pathResp)
	}

	manager, err := workspace.NewManager((&config.Config{}).StateRoot())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := os.Remove(manager.Path(fixture.ProfileName, agentID)); err != nil {
		t.Fatalf("Remove metadata file: %v", err)
	}
	var recoveredPathResp struct {
		OK          bool   `json:"ok"`
		ProjectRoot string `json:"project_root"`
		Mountpoint  string `json:"mountpoint"`
		Mounted     bool   `json:"mounted"`
	}
	if err := runCLIJSONWithEnv(ctx, env, &recoveredPathResp, "workspace", "path", "--profile", fixture.ProfileName, "--agent-id", agentID, "--json"); err != nil {
		t.Fatalf("workspace path after metadata removal: %v", err)
	}
	if !recoveredPathResp.OK || !recoveredPathResp.Mounted || recoveredPathResp.ProjectRoot != createResp.ProjectRoot || recoveredPathResp.Mountpoint != createResp.Mountpoint {
		t.Fatalf("unexpected recovered path response: %#v", recoveredPathResp)
	}

	var statusResp struct {
		OK       bool `json:"ok"`
		Mounted  bool `json:"mounted"`
		Metadata struct {
			Present bool `json:"present"`
		} `json:"metadata"`
		Runtime struct {
			Present      bool `json:"present"`
			ProcessAlive bool `json:"process_alive"`
		} `json:"runtime"`
		Remote struct {
			Checked bool   `json:"checked"`
			Exists  bool   `json:"exists"`
			Error   string `json:"error"`
		} `json:"remote"`
	}
	if err := runCLIJSONWithEnv(ctx, env, &statusResp, "workspace", "status", "--profile", fixture.ProfileName, "--agent-id", agentID, "--json"); err != nil {
		t.Fatalf("workspace status: %v", err)
	}
	if !statusResp.OK || !statusResp.Mounted || !statusResp.Metadata.Present || !statusResp.Runtime.Present || !statusResp.Runtime.ProcessAlive {
		t.Fatalf("unexpected status response: %#v", statusResp)
	}
	if !statusResp.Remote.Checked || !statusResp.Remote.Exists || statusResp.Remote.Error != "" {
		t.Fatalf("unexpected remote status: %#v", statusResp.Remote)
	}

	var deleteResp struct {
		OK           bool `json:"ok"`
		RemoteDelete struct {
			Status string `json:"status"`
		} `json:"remote_delete"`
	}
	if err := runCLIJSONWithEnv(ctx, env, &deleteResp, "workspace", "delete", "--profile", fixture.ProfileName, "--agent-id", agentID, "--json"); err != nil {
		t.Fatalf("workspace delete: %v", err)
	}
	if !deleteResp.OK || deleteResp.RemoteDelete.Status != "ok" {
		t.Fatalf("unexpected delete response: %#v", deleteResp)
	}
}

type integrationFixture struct {
	ProfileName string
	Profile     config.Profile
	Secret      config.Secret
}

func loadIntegrationFixture() (integrationFixture, bool) {
	if fixture, ok := loadIntegrationFixtureFromEnv(); ok {
		return fixture, true
	}

	profileName := os.Getenv("AGENTIC9_IT_PROFILE")
	if profileName == "" {
		profileName = "local"
	}
	cfg, err := config.Load("")
	if err != nil {
		return integrationFixture{}, false
	}
	profile, err := cfg.Profile(profileName)
	if err != nil {
		return integrationFixture{}, false
	}
	secret, err := cfg.LoadSecret(profileName)
	if err != nil {
		return integrationFixture{}, false
	}
	return integrationFixture{
		ProfileName: profileName,
		Profile:     profile,
		Secret:      secret,
	}, true
}

func loadIntegrationFixtureFromEnv() (integrationFixture, bool) {
	cpuHost := os.Getenv("AGENTIC9_IT_CPU_HOST")
	authHost := os.Getenv("AGENTIC9_IT_AUTH_HOST")
	user := os.Getenv("AGENTIC9_IT_USER")
	authDomain := os.Getenv("AGENTIC9_IT_AUTH_DOMAIN")
	secret := os.Getenv("AGENTIC9_IT_SECRET")
	if cpuHost == "" || authHost == "" || user == "" || authDomain == "" || secret == "" {
		return integrationFixture{}, false
	}
	return integrationFixture{
		ProfileName: os.Getenv("AGENTIC9_IT_PROFILE"),
		Profile: config.Profile{
			CPUHost:    cpuHost,
			AuthHost:   authHost,
			User:       user,
			AuthDomain: authDomain,
		},
		Secret: config.Secret{
			Value:  secret,
			Source: "env:AGENTIC9_IT_SECRET",
		},
	}, true
}

func runCLIJSON(ctx context.Context, dst any, args ...string) error {
	return runCLIJSONWithEnv(ctx, nil, dst, args...)
}

func runCLIJSONWithEnv(ctx context.Context, extraEnv map[string]string, dst any, args ...string) error {
	cmdArgs := append([]string{"run", "./cmd/agentic9"}, args...)
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = filepath.Join("..")
	cmd.Env = os.Environ()
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("command failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return err
	}
	return json.Unmarshal(out, dst)
}

func uniqueIntegrationID() string {
	return fmt.Sprintf("it-%d", time.Now().UTC().UnixNano())
}

func containsPath(entries []exportfs.Entry, want string) bool {
	for _, entry := range entries {
		if entry.Path == want {
			return true
		}
	}
	return false
}

func skipMessage() string {
	return "set AGENTIC9_IT_* explicitly or provide a loadable profile in ~/.config/agentic9/config.toml (default profile: local) with its secret env exported"
}
