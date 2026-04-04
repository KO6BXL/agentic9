package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentic9/internal/config"
	"agentic9/internal/exportfs"
	"agentic9/internal/fusefs"
	"agentic9/internal/remoteexec"
	"agentic9/internal/sync"
	"agentic9/internal/transport/tlsrcpu"
	"agentic9/internal/workspace"
)

type command func(context.Context, []string) error

func main() {
	ctx := context.Background()
	commands := map[string]command{
		"profile verify":   profileVerify,
		"workspace create": workspaceCreate,
		"workspace delete": workspaceDelete,
		"workspace path":   workspacePath,
		"mount":            mountWorkspace,
		"unmount":          unmountWorkspace,
		"exec":             execRemote,
		"__serve-mount":    serveMount,
	}
	if err := dispatch(ctx, os.Args[1:], commands); err != nil {
		var exitErr interface{ ExitCode() int }
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, args []string, commands map[string]command) error {
	if len(args) == 0 {
		return errors.New("usage: agentic9 <profile verify|workspace create|workspace delete|workspace path|mount|unmount|exec>")
	}
	if len(args) >= 2 {
		if cmd, ok := commands[strings.Join(args[:2], " ")]; ok {
			return cmd(ctx, args[2:])
		}
	}
	if cmd, ok := commands[args[0]]; ok {
		return cmd(ctx, args[1:])
	}
	return fmt.Errorf("unknown command: %s", strings.Join(args, " "))
}

func profileVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profile verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	client := tlsrcpu.NewClient(profile, secret)
	err = client.Verify(ctx)
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":         err == nil,
			"profile":    *profileName,
			"cpu_host":   profile.CPUHost,
			"auth_host":  profile.AuthHost,
			"user":       profile.User,
			"error":      errString(err),
			"configPath": cfg.Path,
		})
	}
	if err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

func workspaceCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workspace create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	source := fs.String("source", "", "")
	mountpoint := fs.String("mountpoint", "", "")
	mirror := fs.Bool("mirror", false, "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" || *source == "" {
		return errors.New("workspace create requires --agent-id and --source")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	client := tlsrcpu.NewClient(profile, secret)
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	remoteRoot := workspace.RemoteRoot(profile, *agentID)
	if err := client.Verify(ctx); err != nil {
		return err
	}
	if err := client.EnsureRemoteDir(ctx, remoteRoot); err != nil {
		return err
	}
	mp := *mountpoint
	if mp == "" {
		mp, err = workspace.DefaultMountpoint(*profileName, *agentID)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return err
	}
	state, err := startDetachedMount(ctx, cfg, *profileName, *agentID, mp)
	if err != nil {
		return err
	}
	if err := copyTreeWithRetry(ctx, *source, mp, syncdir.Options{Mirror: *mirror}); err != nil {
		_ = stopDetachedMount(cfg.RuntimeRoot(), mp)
		return err
	}
	if err := saveWorkspaceMetadata(manager, *profileName, *agentID, remoteRoot, mp, state.PID); err != nil {
		_ = stopDetachedMount(cfg.RuntimeRoot(), mp)
		return err
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":          true,
			"agent_id":    *agentID,
			"remote_root": remoteRoot,
			"mountpoint":  mp,
			"mounted":     true,
			"pid":         state.PID,
		})
	}
	fmt.Println(mp)
	return nil
}

func workspaceDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workspace delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" {
		return errors.New("workspace delete requires --agent-id")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	report := workspaceDeleteReport{
		OK:             true,
		AgentID:        *agentID,
		RemoteRoot:     workspace.RemoteRoot(profile, *agentID),
		MetadataLookup: skippedStep("not attempted"),
		Unmount:        skippedStep("not attempted"),
		RemoteDelete:   skippedStep("not attempted"),
		Metadata:       skippedStep("not attempted"),
	}
	meta, loadErr := manager.Load(*profileName, *agentID)
	metaFound := false
	switch {
	case loadErr == nil:
		report.MetadataLookup = okStep()
		meta, loadErr = refreshWorkspaceMetadata(cfg.RuntimeRoot(), manager, meta)
		if loadErr != nil {
			report.MetadataLookup = errorStep(loadErr)
			report.OK = false
		} else {
			metaFound = true
			if meta.RemoteRoot != "" {
				report.RemoteRoot = meta.RemoteRoot
			}
		}
	case errors.Is(loadErr, os.ErrNotExist):
		report.MetadataLookup = skippedStep("workspace metadata not found")
	default:
		report.MetadataLookup = errorStep(loadErr)
		report.OK = false
	}

	unmountOK := false
	if metaFound {
		if meta.Mounted && meta.Mountpoint != "" {
			if err := stopDetachedMount(cfg.RuntimeRoot(), meta.Mountpoint); err != nil {
				report.Unmount = errorStep(err)
				report.OK = false
			} else {
				report.Unmount = okStep()
				unmountOK = true
			}
		} else {
			report.Unmount = skippedStep("workspace was not mounted")
			unmountOK = true
		}
	} else {
		report.Unmount = skippedStep("workspace metadata unavailable")
	}

	client := tlsrcpu.NewClient(profile, secret)
	if err := client.RemoveRemoteTree(ctx, report.RemoteRoot); err != nil {
		report.RemoteDelete = errorStep(err)
		report.OK = false
	} else {
		report.RemoteDelete = okStep()
	}

	switch {
	case !metaFound:
		report.Metadata = skippedStep("workspace metadata not found")
	case report.RemoteDelete.Status == stepOK && unmountOK:
		if err := manager.Delete(*profileName, *agentID); err != nil && !errors.Is(err, os.ErrNotExist) {
			report.Metadata = errorStep(err)
			report.OK = false
		} else {
			report.Metadata = stepResult{Status: "deleted"}
		}
	case report.Unmount.Status == stepOK:
		if err := clearWorkspaceMountState(manager, *profileName, *agentID); err != nil {
			report.Metadata = errorStep(err)
			report.OK = false
		} else {
			report.Metadata = stepResult{Status: "updated"}
		}
	default:
		report.Metadata = skippedStep("leaving metadata in place after earlier failure")
	}

	report.finish()
	if *jsonOut {
		if err := writeJSON(report); err != nil {
			return err
		}
		if !report.OK {
			return errors.New(report.Error)
		}
		return nil
	}
	if !report.OK {
		return errors.New(report.Error)
	}
	return nil
}

func workspacePath(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workspace path", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" {
		return errors.New("workspace path requires --agent-id")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cfg, _, _, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	meta, err := manager.Load(*profileName, *agentID)
	if err != nil {
		return err
	}
	meta, err = refreshWorkspaceMetadata(cfg.RuntimeRoot(), manager, meta)
	if err != nil {
		return err
	}
	if !meta.Mounted || meta.Mountpoint == "" {
		err := errors.New("workspace is not mounted")
		if *jsonOut {
			return writeJSON(map[string]any{
				"ok":          false,
				"agent_id":    meta.AgentID,
				"remote_root": meta.RemoteRoot,
				"mountpoint":  "",
				"mounted":     false,
				"error":       err.Error(),
			})
		}
		return err
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":          true,
			"agent_id":    meta.AgentID,
			"mountpoint":  meta.Mountpoint,
			"remote_root": meta.RemoteRoot,
			"mounted":     true,
			"pid":         meta.MountPID,
		})
	}
	fmt.Println(meta.Mountpoint)
	return nil
}

func mountWorkspace(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	mountpoint := fs.String("mountpoint", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" || *mountpoint == "" {
		return errors.New("mount requires --agent-id and --mountpoint")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*mountpoint, 0o755); err != nil {
		return err
	}
	if *jsonOut {
		state, err := startDetachedMount(ctx, cfg, *profileName, *agentID, *mountpoint)
		if err != nil {
			return err
		}
		manager, err := workspace.NewManager(cfg.StateRoot())
		if err != nil {
			_ = stopDetachedMount(cfg.RuntimeRoot(), *mountpoint)
			return err
		}
		if err := saveWorkspaceMetadata(manager, *profileName, *agentID, workspace.RemoteRoot(profile, *agentID), *mountpoint, state.PID); err != nil {
			_ = stopDetachedMount(cfg.RuntimeRoot(), *mountpoint)
			return err
		}
		return writeJSON(map[string]any{
			"ok":         true,
			"agent_id":   *agentID,
			"profile":    *profileName,
			"mountpoint": *mountpoint,
			"pid":        state.PID,
			"mounted":    true,
		})
	}
	client := tlsrcpu.NewClient(profile, secret)
	exp := exportfs.New(client, workspace.RemoteRoot(profile, *agentID))
	mounter := fusefs.NewMountManager(cfg.RuntimeRoot())
	handle, err := mounter.Mount(ctx, *profileName, *agentID, *mountpoint, exp)
	if err != nil {
		return err
	}
	return handle.Wait()
}

func unmountWorkspace(ctx context.Context, args []string) error {
	_ = ctx
	fs := flag.NewFlagSet("unmount", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mountpoint := fs.String("mountpoint", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mountpoint == "" {
		return errors.New("unmount requires --mountpoint")
	}
	runtime := fusefs.NewRuntime(config.DefaultRuntimeRoot())
	state, loadErr := runtime.Load(*mountpoint)
	err := stopDetachedMount(config.DefaultRuntimeRoot(), *mountpoint)
	if err == nil && loadErr == nil && state.Profile != "" && state.AgentID != "" {
		manager, mgrErr := workspace.NewManager((&config.Config{}).StateRoot())
		if mgrErr == nil {
			_ = clearWorkspaceMountState(manager, state.Profile, state.AgentID)
		}
	}
	if *jsonOut {
		return writeJSON(map[string]any{"ok": err == nil, "mountpoint": *mountpoint, "error": errString(err)})
	}
	return err
}

func execRemote(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" {
		return errors.New("exec requires --agent-id")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		return errors.New("exec requires a command after --")
	}
	_, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	client := tlsrcpu.NewClient(profile, secret)
	runner := remoteexec.NewRunner(client, workspace.RemoteRoot(profile, *agentID))
	start := time.Now()
	if *jsonOut {
		_ = writeJSON(map[string]any{
			"type":      "start",
			"workspace": workspace.RemoteRoot(profile, *agentID),
			"command":   cmdArgs,
		})
	}
	result, err := runner.Run(ctx, cmdArgs, func(data []byte) error {
		if !*jsonOut {
			_, werr := os.Stdout.Write(data)
			return werr
		}
		return writeJSON(map[string]any{
			"type":   "output",
			"stream": "remote",
			"data":   string(data),
		})
	})
	if err != nil {
		if *jsonOut {
			_ = writeJSON(map[string]any{
				"type":  "client_error",
				"error": err.Error(),
			})
		}
		return err
	}
	if *jsonOut {
		_ = writeJSON(map[string]any{
			"type":          "exit",
			"ok":            result.ExitCode == 0,
			"exit_code":     result.ExitCode,
			"remote_status": result.RemoteStatus,
			"duration_ms":   time.Since(start).Milliseconds(),
		})
	}
	if result.ExitCode != 0 {
		return exitCodeError(result.ExitCode)
	}
	return nil
}

func serveMount(ctx context.Context, args []string) error {
	_ = ctx
	fs := flag.NewFlagSet("__serve-mount", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	mountpoint := fs.String("mountpoint", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	if *mountpoint == "" {
		return errors.New("__serve-mount requires --mountpoint")
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	runtime := fusefs.NewRuntime(cfg.RuntimeRoot())
	state := fusefs.MountState{
		Profile:    *profileName,
		AgentID:    *agentID,
		Mountpoint: *mountpoint,
		PID:        os.Getpid(),
		Status:     fusefs.MountStatusStarting,
	}
	if err := runtime.Save(state); err != nil {
		return err
	}
	cleanupState := true
	defer func() {
		if cleanupState {
			_ = runtime.Delete(*mountpoint)
			_ = os.Remove(runtime.LogPath(*mountpoint))
		}
	}()

	client := tlsrcpu.NewClient(profile, secret)
	exp := exportfs.New(client, workspace.RemoteRoot(profile, *agentID))
	mounter := fusefs.NewMountManager(cfg.RuntimeRoot())
	handle, err := mounter.Mount(context.Background(), *profileName, *agentID, *mountpoint, exp)
	if err != nil {
		state.Status = fusefs.MountStatusFailed
		state.Error = err.Error()
		cleanupState = false
		_ = runtime.Save(state)
		return err
	}
	state.Status = fusefs.MountStatusMounted
	state.Error = ""
	if err := runtime.Save(state); err != nil {
		_ = handle.Close()
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- handle.Wait()
	}()

	select {
	case <-sigCh:
		_ = handle.Close()
		return <-waitCh
	case err := <-waitCh:
		return err
	}
}

func saveWorkspaceMetadata(manager *workspace.Manager, profileName, agentID, remoteRoot, mountpoint string, pid int) error {
	meta, err := manager.Load(profileName, agentID)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		meta = workspace.Metadata{
			Profile:   profileName,
			AgentID:   agentID,
			CreatedAt: time.Now().UTC(),
		}
	default:
		return err
	}
	meta.Profile = profileName
	meta.AgentID = agentID
	meta.RemoteRoot = remoteRoot
	meta.Mountpoint = mountpoint
	meta.Mounted = true
	meta.MountPID = pid
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	return manager.Save(meta)
}

func clearWorkspaceMountState(manager *workspace.Manager, profileName, agentID string) error {
	meta, err := manager.Load(profileName, agentID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	meta.Mounted = false
	meta.MountPID = 0
	meta.Mountpoint = ""
	return manager.Save(meta)
}

func refreshWorkspaceMetadata(runtimeRoot string, manager *workspace.Manager, meta workspace.Metadata) (workspace.Metadata, error) {
	if !meta.Mounted || meta.Mountpoint == "" || meta.MountPID == 0 {
		return meta, nil
	}
	runtime := fusefs.NewRuntime(runtimeRoot)
	state, err := runtime.Load(meta.Mountpoint)
	if err == nil && state.Status == fusefs.MountStatusMounted && state.PID == meta.MountPID && fusefs.ProcessExists(state.PID) {
		return meta, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return meta, err
	}
	meta.Mounted = false
	meta.MountPID = 0
	meta.Mountpoint = ""
	if saveErr := manager.Save(meta); saveErr != nil {
		return meta, saveErr
	}
	return meta, nil
}

func startDetachedMount(ctx context.Context, cfg *config.Config, profileName, agentID, mountpoint string) (fusefs.MountState, error) {
	runtime := fusefs.NewRuntime(cfg.RuntimeRoot())
	if err := runtime.ClearStale(mountpoint); err != nil {
		return fusefs.MountState{}, err
	}
	if state, err := runtime.Load(mountpoint); err == nil && fusefs.ProcessExists(state.PID) {
		return fusefs.MountState{}, fmt.Errorf("mountpoint %s is already managed by pid %d", mountpoint, state.PID)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fusefs.MountState{}, err
	}

	if err := os.MkdirAll(filepath.Dir(runtime.LogPath(mountpoint)), 0o755); err != nil {
		return fusefs.MountState{}, err
	}
	logFile, err := os.OpenFile(runtime.LogPath(mountpoint), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fusefs.MountState{}, err
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return fusefs.MountState{}, err
	}
	cmd := exec.Command(exe, "__serve-mount", "--profile", profileName, "--agent-id", agentID, "--mountpoint", mountpoint)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fusefs.MountState{}, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	state, err := runtime.WaitForMount(waitCtx, mountpoint, cmd.Process.Pid)
	if err != nil {
		snippet := readLogTail(runtime.LogPath(mountpoint))
		_ = runtime.ClearStale(mountpoint)
		if snippet != "" {
			return fusefs.MountState{}, fmt.Errorf("%w: %s", err, snippet)
		}
		return fusefs.MountState{}, err
	}
	return state, nil
}

func stopDetachedMount(runtimeRoot, mountpoint string) error {
	runtime := fusefs.NewRuntime(runtimeRoot)
	state, err := runtime.Load(mountpoint)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && state.PID > 0 && fusefs.ProcessExists(state.PID) {
		_ = syscall.Kill(state.PID, syscall.SIGTERM)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, loadErr := runtime.Load(mountpoint); errors.Is(loadErr, os.ErrNotExist) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	mounter := fusefs.NewMountManager(runtimeRoot)
	unmountErr := mounter.Unmount(mountpoint)
	if unmountErr != nil && !errors.Is(unmountErr, syscall.ENOSYS) {
		return unmountErr
	}
	_ = runtime.ClearStale(mountpoint)
	if unmountErr != nil && errors.Is(unmountErr, syscall.ENOSYS) {
		if _, loadErr := runtime.Load(mountpoint); errors.Is(loadErr, os.ErrNotExist) {
			return nil
		}
	}
	return unmountErr
}

func readLogTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	const max = 400
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return strings.TrimSpace(string(data))
}

func copyTreeWithRetry(ctx context.Context, src, dst string, opts syncdir.Options) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = syncdir.CopyTree(src, dst, opts)
		if err == nil {
			return nil
		}
		if !isRetriableMountSyncError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return err
}

func isRetriableMountSyncError(err error) bool {
	return errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.ENOTCONN) ||
		errors.Is(err, syscall.EINTR)
}

const stepOK = "ok"

type stepResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type workspaceDeleteReport struct {
	OK             bool       `json:"ok"`
	AgentID        string     `json:"agent_id"`
	RemoteRoot     string     `json:"remote_root"`
	MetadataLookup stepResult `json:"metadata_lookup"`
	Unmount        stepResult `json:"unmount"`
	RemoteDelete   stepResult `json:"remote_delete"`
	Metadata       stepResult `json:"metadata"`
	Error          string     `json:"error,omitempty"`
}

func okStep() stepResult {
	return stepResult{Status: stepOK}
}

func skippedStep(reason string) stepResult {
	return stepResult{Status: "skipped", Error: reason}
}

func errorStep(err error) stepResult {
	if err == nil {
		return okStep()
	}
	return stepResult{Status: "error", Error: err.Error()}
}

func (r *workspaceDeleteReport) finish() {
	if r.OK {
		r.Error = ""
		return
	}
	failures := make([]string, 0, 4)
	for _, step := range []struct {
		name string
		res  stepResult
	}{
		{name: "metadata lookup", res: r.MetadataLookup},
		{name: "unmount", res: r.Unmount},
		{name: "remote delete", res: r.RemoteDelete},
		{name: "metadata", res: r.Metadata},
	} {
		if step.res.Status == "error" {
			failures = append(failures, fmt.Sprintf("%s: %s", step.name, step.res.Error))
		}
	}
	if len(failures) == 0 {
		r.Error = "workspace delete failed"
		return
	}
	r.Error = strings.Join(failures, "; ")
}

func loadProfile(name string) (*config.Config, config.Profile, config.Secret, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, config.Profile{}, config.Secret{}, err
	}
	profile, err := cfg.Profile(name)
	if err != nil {
		return nil, config.Profile{}, config.Secret{}, err
	}
	secret, err := cfg.LoadSecret(name)
	if err != nil {
		return nil, config.Profile{}, config.Secret{}, err
	}
	return cfg, profile, secret, nil
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(v)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit code %d", e) }
func (e exitCodeError) ExitCode() int { return int(e) }

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "agentic9 commands are under %s\n", filepath.Base(os.Args[0]))
	}
}
