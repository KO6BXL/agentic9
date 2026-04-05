---
name: agentic9-cli
description: Use this skill when you need to operate the agentic9 CLI in this repository, especially to verify a 9front profile, create or mount a per-agent workspace, discover the local mount path, run remote commands with exec, or cleanly delete and unmount workspaces using the CLI's JSON and NDJSON outputs.
version: 2026-04-05.1
---

# agentic9 CLI

## Use this skill when

- you need to provision, mount, inspect, or delete a remote per-agent workspace on a 9front host
- you need to run remote builds, tests, or shell commands through `agentic9 exec`
- you need machine-readable CLI output for automation

## First steps

1. Choose the launcher based on where you are working:
  - Otherwise, use the installed `agentic9 ...` binary and do not assume `./cmd/agentic9` exists.
2. Check the CLI/skill contract with `go run ./cmd/agentic9 version --json` and confirm `expected_skill_version` matches this skill version.
3. Make sure `~/.config/agentic9/config.toml` exists and defines the target profile.
4. Always check to make sure that either the secret command is valid, or that the enviroment variable for the secret exists. Stop all operations if either are false and notify the user.
5. Each profile must set `cpu_host`, `auth_host`, `user`, `auth_domain`, and exactly one of `secret_env` or `secret_command`.
6. If `remote_base` is omitted, the CLI defaults it to `/usr/$user/agentic9/workspaces`.
7. Start with `go run ./cmd/agentic9 profile verify --profile <name> --json` before attempting workspace operations.

Minimal config shape:

```toml
[profiles.default]
cpu_host = "cpu.example.net"
auth_host = "auth.example.net"
user = "glenda"
auth_domain = "example.net"
remote_base = "/usr/glenda/agentic9/workspaces"
secret_env = "AGENTIC9_SECRET"
```

## Command model

- `version [--json]`
  Reports the CLI version and the expected skill version for compatibility checks.
- `profile verify --profile <name> [--json]`
  Performs the real `dp9ik` plus TLS-PSK `rcpu` handshake. Prefer `--json` for automation.
- `workspace create --profile <name> --agent-id <id> --seed-path <local-path> [--project-root <path>] [--mirror] [--json]`
  Ensures the remote workspace exists, starts a detached mount helper, copies the local seed tree into the remote project root once, and saves local workspace metadata. `--source` and `--mountpoint` remain accepted as legacy aliases.
- `workspace path --profile <name> --agent-id <id> [--json]`
  Returns the mounted local path if the workspace is currently mounted. With `--json`, an unmounted workspace returns `ok: false`, `mounted: false`, and `error: "workspace is not mounted"`.
- `workspace status --profile <name> --agent-id <id> [--json]`
  Reports local metadata, runtime mount health, remote workspace reachability, and the canonical local `project_root` when mounted.
- `mount --profile <name> --agent-id <id> --project-root <path> [--json]`
  With `--json`, starts a detached helper and waits until the mount is ready. Without `--json`, stays in the foreground until unmounted.
- `unmount --project-root <path> [--json]`
  Stops the detached helper if present and clears mount state when possible.
- `exec --profile <name> --agent-id <id> [--json] -- <command> [args...]`
  Runs a remote command inside the workspace root on the 9front host.
- `workspace delete --profile <name> --agent-id <id> [--json]`
  Unmounts locally if needed, removes the remote tree, and deletes or updates local metadata.

Do not call `__serve-mount` directly. It is an internal helper used by detached mount startup.

## Defaults and invariants

- `agent-id` must start with an ASCII letter or digit, may contain only ASCII letters, digits, `.`, `_`, and `-`, and must be at most 64 characters.
- Default local project root and mountpoint is `$XDG_RUNTIME_DIR/agentic9/<profile>/<agent-id>`.
- If `XDG_RUNTIME_DIR` is unset, the runtime root falls back to `/tmp/agentic9-runtime`, so the default mountpoint becomes `/tmp/agentic9-runtime/<profile>/<agent-id>`.
- Remote workspace root is `<remote_base>/<agent-id>/root`.
- Workspace metadata is stored under `~/.local/state/agentic9/workspaces/<profile>/<agent-id>.json`.

## JSON-first automation

Prefer `--json` for all commands that support it.

- `version --json` emits one JSON object with `cli_version` and `expected_skill_version`.
- `profile verify --json` emits one JSON object with `ok`, `profile`, `cpu_host`, `auth_host`, `user`, `error`, and `configPath`.
- `workspace create --json` emits one JSON object including `ok`, `agent_id`, `project_root`, `remote_project_root`, `mountpoint`, `seed_path`, `bootstrap_mode`, `mounted`, and `pid`. Legacy compatibility fields such as `edit_path`, `source_path`, `sync_mode`, and `remote_root` are still present.
- `workspace path --json` emits one JSON object describing mounted state and the local `project_root`, with `mountpoint` and `edit_path` kept for compatibility.
- `workspace status --json` emits one JSON object with top-level `project_root`, `mounted`, `metadata`, `runtime`, `remote`, and nested `version`.
- `mount --json` emits one JSON object including `ok`, `agent_id`, `profile`, `project_root`, `remote_project_root`, `mountpoint`, `pid`, and `mounted`.
- `unmount --json` emits one JSON object with `ok`, `project_root`, `mountpoint`, and `error`.
- `workspace delete --json` emits one JSON object with per-step status fields: `metadata_lookup`, `unmount`, `remote_delete`, `metadata`, plus top-level `ok`, `agent_id`, `remote_root`, and `error`.

`exec --json` is NDJSON, not a single JSON object. Expect one JSON object per line:

- a `start` event with `workspace`, `remote_project_root`, and `command`
- zero or more `output` events with `stream: "remote"` and `data`
- an `exit` event with `ok`, `exit_code`, `remote_status`, and `duration_ms`
- the `exit` event may also include a `warnings` array for benign remote warnings that were suppressed from normal output
- if the client fails before normal completion, a `client_error` event

Remote stdout and stderr are intentionally combined into the single `"remote"` stream.

## Recommended workflow for agents

1. Check compatibility: `go run ./cmd/agentic9 version --json`
2. Verify the configured profile: `go run ./cmd/agentic9 profile verify --profile <name> --json`
3. Create the workspace: `go run ./cmd/agentic9 workspace create --profile <name> --agent-id <id> --seed-path <local-path> --json`
4. Read the returned `project_root` and operate on files through that mounted path.
5. Inspect health when needed: `go run ./cmd/agentic9 workspace status --profile <name> --agent-id <id> --json`
6. Run remote commands with `go run ./cmd/agentic9 exec --profile <name> --agent-id <id> --json -- <cmd> ...`
7. If you need to rediscover the path later, use `workspace path --json`.

Use the actual configured profile name rather than assuming `default`. If the config really defines `default`, that is fine, but do not guess.

If a workspace exists remotely but is not mounted locally, use `mount --json` to reattach it before editing files.

Important: `workspace create --seed-path <local-path>` is a one-time copy into the mounted remote project root. After creation, treat the returned `project_root` as the canonical local project root the agent should edit. That path is backed by the remote Plan 9 tree, and `exec` runs from the matching repo-relative root on the remote host. Editing the original seed directory does not automatically update the remote workspace unless you copy or sync those changes yourself.

Important: workspaces are meant to persist until the user explicitly asks to clean them up. Do not call `workspace delete` as a routine success-path teardown after builds or tests. Use `workspace delete` only when the user asks to remove the remote project root or when cleanup is the stated goal.

Important: prefer editing files through the mounted `project_root` rather than trying to upload large content through `agentic9 exec` stdin. The mounted filesystem path is the intended way to move project files into the remote workspace.

## Remote exec notes

- Prefer explicit shell invocations for multi-step commands, for example `rc -lc '<command>'`, so quoting and working-directory expectations are clear.
- Non-interactive `exec` runs may print warnings like `bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'` or `bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'`. If the command still reaches a normal `exit` event, treat those warnings as non-fatal noise rather than a separate blocker.

Minimal cookbook:

- Inspect the remote workspace:
  `go run ./cmd/agentic9 exec --profile <name> --agent-id <id> --json -- rc -lc 'pwd; ls -l'`
- Build and run a simple program:
  `go run ./cmd/agentic9 exec --profile <name> --agent-id <id> --json -- rc -lc 'mk && ./hello'`
- Check expected build tool paths before building:
  `go run ./cmd/agentic9 exec --profile <name> --agent-id <id> --json -- rc -lc 'ls -l /bin/mk /bin/8c /bin/6c'`
