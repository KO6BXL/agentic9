package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	exp := exportfs.New(client, remoteRoot)
	mounter := fusefs.NewMountManager(cfg.RuntimeRoot())
	handle, err := mounter.Mount(ctx, *profileName, *agentID, mp, exp)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := syncdir.CopyTree(*source, mp, syncdir.Options{Mirror: *mirror}); err != nil {
		return err
	}
	meta := workspace.Metadata{
		Profile:    *profileName,
		AgentID:    *agentID,
		RemoteRoot: remoteRoot,
		Mountpoint: mp,
		CreatedAt:  time.Now().UTC(),
	}
	if err := manager.Save(meta); err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":          true,
			"agent_id":    *agentID,
			"remote_root": remoteRoot,
			"mountpoint":  mp,
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
	cfg, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	manager, err := workspace.NewManager(cfg.StateRoot())
	if err != nil {
		return err
	}
	meta, err := manager.Load(*profileName, *agentID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		mounter := fusefs.NewMountManager(cfg.RuntimeRoot())
		_ = mounter.Unmount(meta.Mountpoint)
	}
	client := tlsrcpu.NewClient(profile, secret)
	if err := client.RemoveRemoteTree(ctx, workspace.RemoteRoot(profile, *agentID)); err != nil && !errors.Is(err, tlsrcpu.ErrUnimplemented) {
		return err
	}
	if err == nil {
		_ = manager.Delete(*profileName, *agentID)
	}
	if *jsonOut {
		return writeJSON(map[string]any{"ok": true, "agent_id": *agentID})
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
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":          true,
			"agent_id":    meta.AgentID,
			"mountpoint":  meta.Mountpoint,
			"remote_root": meta.RemoteRoot,
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
	_, profile, secret, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*mountpoint, 0o755); err != nil {
		return err
	}
	client := tlsrcpu.NewClient(profile, secret)
	exp := exportfs.New(client, workspace.RemoteRoot(profile, *agentID))
	mounter := fusefs.NewMountManager(config.DefaultRuntimeRoot())
	handle, err := mounter.Mount(ctx, *profileName, *agentID, *mountpoint, exp)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(map[string]any{
			"ok":         true,
			"mountpoint": *mountpoint,
			"pid":        handle.PID(),
		})
	}
	return handle.Wait()
}

func unmountWorkspace(ctx context.Context, args []string) error {
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
	mounter := fusefs.NewMountManager(config.DefaultRuntimeRoot())
	err := mounter.Unmount(*mountpoint)
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
