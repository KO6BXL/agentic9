package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"agentic9/internal/buildinfo"
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
		"workspace status": workspaceStatus,
		"mount":            mountWorkspace,
		"unmount":          unmountWorkspace,
		"exec":             execRemote,
		"version":          printVersion,
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
		return errors.New("usage: agentic9 <profile verify|workspace create|workspace delete|workspace path|workspace status|mount|unmount|exec|version>")
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

func printVersion(ctx context.Context, args []string) error {
	_ = ctx
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := versionInfo()
	if *jsonOut {
		return writeJSON(info)
	}
	fmt.Printf("agentic9 %s\nexpected skill version %s\n", info.CLIVersion, info.ExpectedSkillVersion)
	return nil
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
	seedPath := fs.String("seed-path", "", "")
	mountpoint := fs.String("mountpoint", "", "")
	projectRoot := fs.String("project-root", "", "")
	mirror := fs.Bool("mirror", false, "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedSource, err := resolveAliasedPathFlag("--source", *source, "--seed-path", *seedPath)
	if err != nil {
		return err
	}
	resolvedProjectRoot, err := resolveAliasedPathFlag("--mountpoint", *mountpoint, "--project-root", *projectRoot)
	if err != nil {
		return err
	}
	if *agentID == "" || resolvedSource == "" {
		return errors.New("workspace create requires --agent-id and one of --seed-path or --source")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	sourcePath, err := filepath.Abs(resolvedSource)
	if err != nil {
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
	createdEmptyRemoteRoot, err := remoteDirectoryIsEmpty(ctx, client, remoteRoot)
	if err != nil {
		return err
	}
	if err := copyTreeToRemote(ctx, sourcePath, client, remoteRoot, syncdir.Options{Mirror: *mirror}); err != nil {
		if createdEmptyRemoteRoot {
			_ = client.RemoveRemoteTree(context.Background(), remoteRoot)
		}
		return err
	}
	mp := resolvedProjectRoot
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
		if createdEmptyRemoteRoot {
			_ = client.RemoveRemoteTree(context.Background(), remoteRoot)
		}
		return err
	}
	if err := waitForWritableMount(ctx, mp); err != nil {
		_ = stopDetachedMount(cfg.RuntimeRoot(), mp)
		if createdEmptyRemoteRoot {
			_ = client.RemoveRemoteTree(context.Background(), remoteRoot)
		}
		return err
	}
	if err := saveWorkspaceMetadata(manager, *profileName, *agentID, remoteRoot, mp, state.PID); err != nil {
		_ = stopDetachedMount(cfg.RuntimeRoot(), mp)
		if createdEmptyRemoteRoot {
			_ = client.RemoveRemoteTree(context.Background(), remoteRoot)
		}
		return err
	}
	if *jsonOut {
		return writeJSON(workspaceCreateJSON(*agentID, remoteRoot, mp, sourcePath, state.PID, *mirror))
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
	meta, loadErr := loadWorkspaceMetadata(cfg.RuntimeRoot(), manager, profile, *profileName, *agentID)
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
	cfg, profile, _, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	meta, err := loadWorkspaceMetadata(cfg.RuntimeRoot(), manager, profile, *profileName, *agentID)
	if err != nil {
		if *jsonOut && errors.Is(err, os.ErrNotExist) {
			return writeJSON(map[string]any{
				"ok":                  false,
				"agent_id":            *agentID,
				"remote_root":         workspace.RemoteRoot(profile, *agentID),
				"remote_project_root": workspace.RemoteRoot(profile, *agentID),
				"edit_path":           "",
				"mountpoint":          "",
				"project_root":        "",
				"mounted":             false,
				"error":               "workspace metadata and active mount state not found",
			})
		}
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
				"ok":                  false,
				"agent_id":            meta.AgentID,
				"remote_root":         meta.RemoteRoot,
				"remote_project_root": meta.RemoteRoot,
				"edit_path":           "",
				"mountpoint":          "",
				"project_root":        "",
				"mounted":             false,
				"error":               err.Error(),
			})
		}
		return err
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":                  true,
			"agent_id":            meta.AgentID,
			"edit_path":           meta.Mountpoint,
			"mountpoint":          meta.Mountpoint,
			"project_root":        meta.Mountpoint,
			"remote_root":         meta.RemoteRoot,
			"remote_project_root": meta.RemoteRoot,
			"mounted":             true,
			"pid":                 meta.MountPID,
		})
	}
	fmt.Println(meta.Mountpoint)
	return nil
}

func workspaceStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workspace status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentID == "" {
		return errors.New("workspace status requires --agent-id")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}

	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	profile, err := cfg.Profile(*profileName)
	if err != nil {
		return err
	}
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	report, err := inspectWorkspaceState(cfg.RuntimeRoot(), manager, profile, *profileName, *agentID)
	if err != nil {
		return err
	}
	report.Version = versionInfo()

	secret, secretErr := cfg.LoadSecret(*profileName)
	switch {
	case secretErr != nil:
		report.Remote.Checked = false
		report.Remote.Error = secretErr.Error()
	default:
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		exists, err := remoteWorkspaceExists(checkCtx, profile, secret, report.Remote.Root)
		report.Remote.Checked = true
		report.Remote.Exists = exists
		report.Remote.Error = errString(err)
	}

	if *jsonOut {
		return writeJSON(report)
	}

	fmt.Printf("agentic9 %s\n", report.Version.CLIVersion)
	fmt.Printf("workspace %s/%s\n", report.Profile, report.AgentID)
	fmt.Printf("project_root: %s\n", emptyDash(report.ProjectRoot))
	fmt.Printf("mounted: %t\n", report.Mounted)
	fmt.Printf("metadata present: %t\n", report.Metadata.Present)
	fmt.Printf("runtime present: %t\n", report.Runtime.Present)
	if report.Runtime.Present {
		fmt.Printf("runtime status: %s\n", report.Runtime.Status)
		fmt.Printf("runtime pid alive: %t\n", report.Runtime.ProcessAlive)
	}
	if report.Remote.Checked {
		fmt.Printf("remote exists: %t\n", report.Remote.Exists)
	} else {
		fmt.Printf("remote exists: unknown (%s)\n", report.Remote.Error)
	}
	for _, inconsistency := range report.Inconsistencies {
		fmt.Printf("warning: %s\n", inconsistency)
	}
	return nil
}

func mountWorkspace(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "")
	agentID := fs.String("agent-id", "", "")
	mountpoint := fs.String("mountpoint", "", "")
	projectRoot := fs.String("project-root", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedProjectRoot, err := resolveAliasedPathFlag("--mountpoint", *mountpoint, "--project-root", *projectRoot)
	if err != nil {
		return err
	}
	if *agentID == "" || resolvedProjectRoot == "" {
		return errors.New("mount requires --agent-id and one of --project-root or --mountpoint")
	}
	if err := workspace.ValidateAgentID(*agentID); err != nil {
		return err
	}
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(resolvedProjectRoot, 0o755); err != nil {
		return err
	}
	if *jsonOut {
		state, err := startDetachedMount(ctx, cfg, *profileName, *agentID, resolvedProjectRoot)
		if err != nil {
			return err
		}
		manager, err := workspace.NewManager(cfg.StateRoot())
		if err != nil {
			_ = stopDetachedMount(cfg.RuntimeRoot(), resolvedProjectRoot)
			return err
		}
		if err := saveWorkspaceMetadata(manager, *profileName, *agentID, workspace.RemoteRoot(profile, *agentID), resolvedProjectRoot, state.PID); err != nil {
			_ = stopDetachedMount(cfg.RuntimeRoot(), resolvedProjectRoot)
			return err
		}
		return writeJSON(map[string]any{
			"ok":                  true,
			"agent_id":            *agentID,
			"profile":             *profileName,
			"mountpoint":          resolvedProjectRoot,
			"project_root":        resolvedProjectRoot,
			"remote_root":         workspace.RemoteRoot(profile, *agentID),
			"remote_project_root": workspace.RemoteRoot(profile, *agentID),
			"pid":                 state.PID,
			"mounted":             true,
		})
	}
	client := tlsrcpu.NewClient(profile, secret)
	exp := exportfs.New(client, workspace.RemoteRoot(profile, *agentID))
	mounter := fusefs.NewMountManager(cfg.RuntimeRoot())
	handle, err := mounter.Mount(ctx, *profileName, *agentID, resolvedProjectRoot, exp)
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
	projectRoot := fs.String("project-root", "", "")
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedProjectRoot, err := resolveAliasedPathFlag("--mountpoint", *mountpoint, "--project-root", *projectRoot)
	if err != nil {
		return err
	}
	if resolvedProjectRoot == "" {
		return errors.New("unmount requires one of --project-root or --mountpoint")
	}
	runtime := fusefs.NewRuntime(config.DefaultRuntimeRoot())
	state, loadErr := runtime.Load(resolvedProjectRoot)
	err = stopDetachedMount(config.DefaultRuntimeRoot(), resolvedProjectRoot)
	if err == nil && loadErr == nil && state.Profile != "" && state.AgentID != "" {
		manager, mgrErr := workspace.NewManager((&config.Config{}).StateRoot())
		if mgrErr == nil {
			_ = clearWorkspaceMountState(manager, state.Profile, state.AgentID)
		}
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":           err == nil,
			"mountpoint":   resolvedProjectRoot,
			"project_root": resolvedProjectRoot,
			"error":        errString(err),
		})
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
	outputRouter := newExecOutputRouter(*jsonOut)
	if *jsonOut {
		_ = writeJSON(map[string]any{
			"type":                "start",
			"workspace":           workspace.RemoteRoot(profile, *agentID),
			"remote_project_root": workspace.RemoteRoot(profile, *agentID),
			"command":             cmdArgs,
		})
	}
	result, err := runner.Run(ctx, cmdArgs, outputRouter.Write)
	if err != nil {
		_ = outputRouter.Flush()
		if *jsonOut {
			_ = writeJSON(map[string]any{
				"type":  "client_error",
				"error": err.Error(),
			})
		}
		return err
	}
	if err := outputRouter.Flush(); err != nil {
		return err
	}
	if *jsonOut {
		exitEvent := map[string]any{
			"type":          "exit",
			"ok":            result.ExitCode == 0,
			"exit_code":     result.ExitCode,
			"remote_status": result.RemoteStatus,
			"duration_ms":   time.Since(start).Milliseconds(),
		}
		if len(outputRouter.warnings) > 0 {
			exitEvent["warnings"] = outputRouter.warnings
		}
		_ = writeJSON(exitEvent)
	}
	if result.ExitCode != 0 {
		return exitCodeError(result.ExitCode)
	}
	return nil
}

type execWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Benign  bool   `json:"benign"`
}

type execOutputRouter struct {
	jsonOut  bool
	pending  bytes.Buffer
	warnings []execWarning
}

func newExecOutputRouter(jsonOut bool) *execOutputRouter {
	return &execOutputRouter{jsonOut: jsonOut}
}

func (r *execOutputRouter) Write(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	if _, err := r.pending.Write(chunk); err != nil {
		return err
	}
	return r.flushLines(false)
}

func (r *execOutputRouter) Flush() error {
	return r.flushLines(true)
}

func (r *execOutputRouter) flushLines(flushRemainder bool) error {
	data := r.pending.Bytes()
	consumed := 0
	for {
		idx := bytes.IndexByte(data[consumed:], '\n')
		if idx < 0 {
			break
		}
		end := consumed + idx + 1
		if err := r.handleLine(data[consumed:end]); err != nil {
			return err
		}
		consumed = end
	}
	if flushRemainder && consumed < len(data) {
		if err := r.handleLine(data[consumed:]); err != nil {
			return err
		}
		consumed = len(data)
	}
	if consumed == 0 {
		return nil
	}
	remaining := append([]byte(nil), data[consumed:]...)
	r.pending.Reset()
	_, _ = r.pending.Write(remaining)
	return nil
}

func (r *execOutputRouter) handleLine(line []byte) error {
	if warning, ok := classifyExecWarning(line); ok {
		r.warnings = append(r.warnings, warning)
		return nil
	}
	if !r.jsonOut {
		_, err := os.Stdout.Write(line)
		return err
	}
	return writeJSON(map[string]any{
		"type":   "output",
		"stream": "remote",
		"data":   string(line),
	})
}

func classifyExecWarning(line []byte) (execWarning, bool) {
	message := strings.TrimRight(string(line), "\r\n")
	switch message {
	case "bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'":
		return execWarning{
			Kind:    "benign_remote_bind_warning",
			Message: message,
			Benign:  true,
		}, true
	case "bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'":
		return execWarning{
			Kind:    "benign_remote_bind_warning",
			Message: message,
			Benign:  true,
		}, true
	default:
		return execWarning{}, false
	}
}

func workspaceCreateJSON(agentID, remoteRoot, mountpoint, sourcePath string, pid int, mirror bool) map[string]any {
	bootstrapMode := "copy-once"
	if mirror {
		bootstrapMode = "mirror-once"
	}
	return map[string]any{
		"ok":                     true,
		"agent_id":               agentID,
		"cli_version":            buildinfo.CLIVersion,
		"expected_skill_version": buildinfo.SkillVersion,
		"remote_root":            remoteRoot,
		"remote_project_root":    remoteRoot,
		"mountpoint":             mountpoint,
		"edit_path":              mountpoint,
		"project_root":           mountpoint,
		"source_path":            sourcePath,
		"seed_path":              sourcePath,
		"sync_mode":              bootstrapMode,
		"bootstrap_mode":         bootstrapMode,
		"mounted":                true,
		"pid":                    pid,
	}
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
		if state, ok, err := findMountedRuntimeStateByAgent(runtimeRoot, meta.Profile, meta.AgentID, meta.Mountpoint); err != nil {
			return meta, err
		} else if ok {
			meta.Mountpoint = state.Mountpoint
			meta.Mounted = true
			meta.MountPID = state.PID
			if saveErr := manager.Save(meta); saveErr != nil {
				return meta, saveErr
			}
		}
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
	if recovered, ok, findErr := findMountedRuntimeStateByAgent(runtimeRoot, meta.Profile, meta.AgentID, meta.Mountpoint); findErr != nil {
		return meta, findErr
	} else if ok {
		meta.Mountpoint = recovered.Mountpoint
		meta.MountPID = recovered.PID
		meta.Mounted = true
		if saveErr := manager.Save(meta); saveErr != nil {
			return meta, saveErr
		}
		return meta, nil
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

func waitForWritableMount(ctx context.Context, mountpoint string) error {
	probePath := filepath.Join(mountpoint, fmt.Sprintf(".agentic9-ready-%d", os.Getpid()))
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		err = probeWritableMount(probePath)
		if err == nil {
			return nil
		}
		if !isRetriableMountReadyError(err) {
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

func probeWritableMount(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isRetriableMountReadyError(err error) bool {
	return isRetriableMountSyncError(err) || errors.Is(err, os.ErrNotExist)
}

func remoteDirectoryIsEmpty(ctx context.Context, client *tlsrcpu.Client, remoteRoot string) (bool, error) {
	debugSeedf("list root %s", remoteRoot)
	exp := exportfs.New(client, remoteRoot)
	entries, err := exp.List(ctx, "/")
	if err != nil {
		debugSeedf("list root error: %v", err)
		return false, err
	}
	debugSeedf("list root ok entries=%d", len(entries))
	return len(entries) == 0, nil
}

func copyTreeToRemote(ctx context.Context, src string, client *tlsrcpu.Client, remoteRoot string, opts syncdir.Options) error {
	seen := map[string]struct{}{}
	err := filepath.WalkDir(src, func(localPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, localPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldSkipSeedPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		seen[rel] = struct{}{}
		remotePath := path.Join("/", rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsDir():
			debugSeedf("mkdir %s", remotePath)
			exp := exportfs.New(client, remoteRoot)
			if err := exp.Mkdir(ctx, remotePath, info.Mode().Perm()); err != nil && !errors.Is(err, os.ErrExist) {
				debugSeedf("mkdir error %s: %v", remotePath, err)
				return err
			}
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("symlink seeding is not supported for %s", localPath)
		case info.Mode().IsRegular():
			debugSeedf("copy file %s -> %s", localPath, remotePath)
			return copyFileToRemote(ctx, localPath, remotePath, client, remoteRoot, info.Mode().Perm())
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	if !opts.Mirror {
		return nil
	}
	remotePaths, err := listRemotePaths(ctx, client, remoteRoot, "/")
	if err != nil {
		return err
	}
	sort.Slice(remotePaths, func(i, j int) bool {
		return strings.Count(remotePaths[i], "/") > strings.Count(remotePaths[j], "/")
	})
	for _, remotePath := range remotePaths {
		rel := strings.TrimPrefix(remotePath, "/")
		if _, ok := seen[rel]; ok {
			continue
		}
		debugSeedf("remove extra %s", remotePath)
		exp := exportfs.New(client, remoteRoot)
		if err := exp.Remove(ctx, remotePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			debugSeedf("remove extra error %s: %v", remotePath, err)
			return err
		}
	}
	return nil
}

func copyFileToRemote(ctx context.Context, localPath, remotePath string, client *tlsrcpu.Client, remoteRoot string, mode fs.FileMode) error {
	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()

	exp := exportfs.New(client, remoteRoot)
	debugSeedf("remove before create %s", remotePath)
	if err := exp.Remove(ctx, remotePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		debugSeedf("remove before create error %s: %v", remotePath, err)
		return err
	}
	debugSeedf("create %s", remotePath)
	handle, _, err := exp.Create(ctx, remotePath, mode, uint32(os.O_WRONLY|os.O_TRUNC))
	if err != nil {
		debugSeedf("create error %s: %v", remotePath, err)
		return err
	}
	defer handle.Close()

	buf := make([]byte, 128*1024)
	var off int64
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			written, writeErr := handle.WriteAt(buf[:n], off)
			if writeErr != nil {
				debugSeedf("write error %s at %d: %v", remotePath, off, writeErr)
				return writeErr
			}
			off += int64(written)
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func listRemotePaths(ctx context.Context, client *tlsrcpu.Client, remoteRoot, root string) ([]string, error) {
	exp := exportfs.New(client, remoteRoot)
	entries, err := exp.List(ctx, root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
		if entry.Mode&os.ModeDir != 0 {
			childPaths, err := listRemotePaths(ctx, client, remoteRoot, entry.Path)
			if err != nil {
				return nil, err
			}
			paths = append(paths, childPaths...)
		}
	}
	return paths, nil
}

func shouldSkipSeedPath(rel string) bool {
	return rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator))
}

func debugSeedf(format string, args ...any) {
	if os.Getenv("AGENTIC9_DEBUG_SEED") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "seed: "+format+"\n", args...)
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

func resolveAliasedPathFlag(primaryName, primaryValue, aliasName, aliasValue string) (string, error) {
	switch {
	case primaryValue == "":
		return aliasValue, nil
	case aliasValue == "":
		return primaryValue, nil
	case primaryValue == aliasValue:
		return primaryValue, nil
	default:
		return "", fmt.Errorf("%s and %s must match when both are set", primaryName, aliasName)
	}
}

func loadWorkspaceMetadata(runtimeRoot string, manager *workspace.Manager, profile config.Profile, profileName, agentID string) (workspace.Metadata, error) {
	meta, err := manager.Load(profileName, agentID)
	if err == nil {
		return meta, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return workspace.Metadata{}, err
	}
	state, ok, err := findMountedRuntimeStateByAgent(runtimeRoot, profileName, agentID, "")
	if err != nil {
		return workspace.Metadata{}, err
	}
	if ok {
		meta = workspace.Metadata{
			Profile:    profileName,
			AgentID:    agentID,
			RemoteRoot: workspace.RemoteRoot(profile, agentID),
			Mountpoint: state.Mountpoint,
			Mounted:    true,
			MountPID:   state.PID,
			CreatedAt:  state.UpdatedAt,
		}
		if meta.CreatedAt.IsZero() {
			meta.CreatedAt = time.Now().UTC()
		}
		if err := manager.Save(meta); err != nil {
			return workspace.Metadata{}, err
		}
		return meta, nil
	}
	return workspace.Metadata{}, os.ErrNotExist
}

type runtimeStateSelector func(fusefs.MountState) bool

func findMountedRuntimeStateByAgent(runtimeRoot, profileName, agentID, hintMountpoint string) (fusefs.MountState, bool, error) {
	return findRuntimeStateByAgent(runtimeRoot, profileName, agentID, hintMountpoint, func(state fusefs.MountState) bool {
		return state.Status == fusefs.MountStatusMounted && fusefs.ProcessExists(state.PID)
	})
}

func findAnyRuntimeStateByAgent(runtimeRoot, profileName, agentID, hintMountpoint string) (fusefs.MountState, bool, error) {
	return findRuntimeStateByAgent(runtimeRoot, profileName, agentID, hintMountpoint, func(state fusefs.MountState) bool {
		return true
	})
}

func findRuntimeStateByAgent(runtimeRoot, profileName, agentID, hintMountpoint string, selectState runtimeStateSelector) (fusefs.MountState, bool, error) {
	runtime := fusefs.NewRuntime(runtimeRoot)
	if hintMountpoint != "" {
		state, err := runtime.Load(hintMountpoint)
		switch {
		case err == nil:
			if state.Profile == profileName && state.AgentID == agentID && selectState(state) {
				return state, true, nil
			}
		case !errors.Is(err, os.ErrNotExist):
			return fusefs.MountState{}, false, err
		}
	}

	states, err := runtime.List()
	if err != nil {
		return fusefs.MountState{}, false, err
	}
	var (
		best    fusefs.MountState
		found   bool
		bestAge time.Time
	)
	for _, state := range states {
		if state.Profile != profileName || state.AgentID != agentID || !selectState(state) {
			continue
		}
		if !found || state.UpdatedAt.After(bestAge) {
			best = state
			bestAge = state.UpdatedAt
			found = true
		}
	}
	return best, found, nil
}

type versionReport struct {
	CLIVersion           string `json:"cli_version"`
	ExpectedSkillVersion string `json:"expected_skill_version"`
}

type workspaceStatusMetadata struct {
	Present    bool   `json:"present"`
	Path       string `json:"path"`
	RemoteRoot string `json:"remote_root,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Mounted    bool   `json:"mounted"`
	PID        int    `json:"pid,omitempty"`
}

type workspaceStatusRuntime struct {
	Present      bool               `json:"present"`
	StatePath    string             `json:"state_path,omitempty"`
	LogPath      string             `json:"log_path,omitempty"`
	Mountpoint   string             `json:"mountpoint,omitempty"`
	Status       fusefs.MountStatus `json:"status,omitempty"`
	PID          int                `json:"pid,omitempty"`
	ProcessAlive bool               `json:"process_alive"`
	UpdatedAt    time.Time          `json:"updated_at,omitempty"`
}

type workspaceStatusRemote struct {
	Checked bool   `json:"checked"`
	Exists  bool   `json:"exists"`
	Root    string `json:"root"`
	Error   string `json:"error,omitempty"`
}

type workspaceStatusReport struct {
	OK                bool                    `json:"ok"`
	AgentID           string                  `json:"agent_id"`
	Profile           string                  `json:"profile"`
	ProjectRoot       string                  `json:"project_root"`
	RemoteProjectRoot string                  `json:"remote_project_root"`
	Mounted           bool                    `json:"mounted"`
	Inconsistencies   []string                `json:"inconsistencies,omitempty"`
	Version           versionReport           `json:"version"`
	Metadata          workspaceStatusMetadata `json:"metadata"`
	Runtime           workspaceStatusRuntime  `json:"runtime"`
	Remote            workspaceStatusRemote   `json:"remote"`
}

func inspectWorkspaceState(runtimeRoot string, manager *workspace.Manager, profile config.Profile, profileName, agentID string) (workspaceStatusReport, error) {
	report := workspaceStatusReport{
		OK:      true,
		AgentID: agentID,
		Profile: profileName,
		Metadata: workspaceStatusMetadata{
			Path: manager.Path(profileName, agentID),
		},
		Remote: workspaceStatusRemote{
			Root: workspace.RemoteRoot(profile, agentID),
		},
	}

	meta, err := manager.Load(profileName, agentID)
	switch {
	case err == nil:
		report.Metadata.Present = true
		report.Metadata.RemoteRoot = meta.RemoteRoot
		report.Metadata.Mountpoint = meta.Mountpoint
		report.Metadata.Mounted = meta.Mounted
		report.Metadata.PID = meta.MountPID
		if meta.RemoteRoot != "" {
			report.Remote.Root = meta.RemoteRoot
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return report, err
	}

	runtime := fusefs.NewRuntime(runtimeRoot)
	state, runtimePresent, err := findAnyRuntimeStateByAgent(runtimeRoot, profileName, agentID, report.Metadata.Mountpoint)
	if err != nil {
		return report, err
	}
	if runtimePresent {
		report.Runtime.Present = true
		report.Runtime.StatePath = runtime.StatePath(state.Mountpoint)
		report.Runtime.LogPath = runtime.LogPath(state.Mountpoint)
		report.Runtime.Mountpoint = state.Mountpoint
		report.Runtime.Status = state.Status
		report.Runtime.PID = state.PID
		report.Runtime.ProcessAlive = fusefs.ProcessExists(state.PID)
		report.Runtime.UpdatedAt = state.UpdatedAt
	}

	report.Mounted = runtimePresent && state.Status == fusefs.MountStatusMounted && report.Runtime.ProcessAlive
	switch {
	case report.Mounted:
		report.ProjectRoot = state.Mountpoint
	case report.Metadata.Mountpoint != "":
		report.ProjectRoot = report.Metadata.Mountpoint
	}
	report.RemoteProjectRoot = report.Remote.Root

	if !report.Metadata.Present && report.Mounted {
		report.Inconsistencies = append(report.Inconsistencies, "metadata is missing while an active runtime mount exists")
	}
	if report.Metadata.Present && report.Metadata.Mounted && !report.Mounted {
		report.Inconsistencies = append(report.Inconsistencies, "metadata says the workspace is mounted but runtime state is not active")
	}
	if report.Metadata.Present && report.Mounted {
		if report.Metadata.Mountpoint != "" && report.Metadata.Mountpoint != state.Mountpoint {
			report.Inconsistencies = append(report.Inconsistencies, "metadata mountpoint does not match the active runtime mountpoint")
		}
		if report.Metadata.PID != 0 && report.Metadata.PID != state.PID {
			report.Inconsistencies = append(report.Inconsistencies, "metadata mount pid does not match the active runtime pid")
		}
	}
	return report, nil
}

func remoteWorkspaceExists(ctx context.Context, profile config.Profile, secret config.Secret, remoteRoot string) (bool, error) {
	fs := exportfs.New(tlsrcpu.NewClient(profile, secret), remoteRoot)
	_, err := fs.Stat(ctx, "/")
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func versionInfo() versionReport {
	return versionReport{
		CLIVersion:           buildinfo.CLIVersion,
		ExpectedSkillVersion: buildinfo.SkillVersion,
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func directoryIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
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
