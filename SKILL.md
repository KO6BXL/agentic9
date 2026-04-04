---
name: agentic9-cli
description: Use this skill when you need to operate the agentic9 CLI in this repository, especially to verify a 9front profile, create or mount a per-agent workspace, discover the local mount path, run remote commands with exec, or cleanly delete and unmount workspaces using the CLI's JSON and NDJSON outputs.
---

# agentic9 CLI

## Use this skill when

- you are working in this repository and need to drive the `agentic9` CLI instead of re-deriving its behavior from the source each time
- you need to provision, mount, inspect, or delete a remote per-agent workspace on a 9front host
- you need to run remote builds, tests, or shell commands through `agentic9 exec`
- you need machine-readable CLI output for automation

## First steps

1. Prefer `go run ./cmd/agentic9 ...` while iterating inside the repo. Use `go build ./cmd/agentic9` only if you specifically want a standalone binary.
2. Make sure `~/.config/agentic9/config.toml` exists and defines the target profile.
3. Each profile must set `cpu_host`, `auth_host`, `user`, `auth_domain`, and exactly one of `secret_env` or `secret_command`.
4. If `remote_base` is omitted, the CLI defaults it to `/usr/$user/agentic9/workspaces`.
5. Start with `go run ./cmd/agentic9 profile verify --profile <name> --json` before attempting workspace operations.

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

- `profile verify --profile <name> [--json]`
  Performs the real `dp9ik` plus TLS-PSK `rcpu` handshake. Prefer `--json` for automation.
- `workspace create --profile <name> --agent-id <id> --source <local-path> [--mountpoint <path>] [--mirror] [--json]`
  Ensures the remote workspace exists, starts a detached mount helper, copies the local source tree into the mount, and saves local workspace metadata.
- `workspace path --profile <name> --agent-id <id> [--json]`
  Returns the mounted local path if the workspace is currently mounted. With `--json`, an unmounted workspace returns `ok: false`, `mounted: false`, and `error: "workspace is not mounted"`.
- `mount --profile <name> --agent-id <id> --mountpoint <path> [--json]`
  With `--json`, starts a detached helper and waits until the mount is ready. Without `--json`, stays in the foreground until unmounted.
- `unmount --mountpoint <path> [--json]`
  Stops the detached helper if present and clears mount state when possible.
- `exec --profile <name> --agent-id <id> [--json] -- <command> [args...]`
  Runs a remote command inside the workspace root on the 9front host.
- `workspace delete --profile <name> --agent-id <id> [--json]`
  Unmounts locally if needed, removes the remote tree, and deletes or updates local metadata.

Do not call `__serve-mount` directly. It is an internal helper used by detached mount startup.

## Defaults and invariants

- `agent-id` must start with an ASCII letter or digit, may contain only ASCII letters, digits, `.`, `_`, and `-`, and must be at most 64 characters.
- Default local mountpoint is `$XDG_RUNTIME_DIR/agentic9/<profile>/<agent-id>`.
- If `XDG_RUNTIME_DIR` is unset, the runtime root falls back to `/tmp/agentic9-runtime`, so the default mountpoint becomes `/tmp/agentic9-runtime/<profile>/<agent-id>`.
- Remote workspace root is `<remote_base>/<agent-id>/root`.
- Workspace metadata is stored under `~/.local/state/agentic9/workspaces/<profile>/<agent-id>.json`.

## JSON-first automation

Prefer `--json` for all commands that support it.

- `profile verify --json` emits one JSON object with `ok`, `profile`, `cpu_host`, `auth_host`, `user`, `error`, and `configPath`.
- `workspace create --json` emits one JSON object including `ok`, `agent_id`, `remote_root`, `mountpoint`, `mounted`, and `pid`.
- `workspace path --json` emits one JSON object describing mounted state and mountpoint.
- `mount --json` emits one JSON object including `ok`, `agent_id`, `profile`, `mountpoint`, `pid`, and `mounted`.
- `unmount --json` emits one JSON object with `ok`, `mountpoint`, and `error`.
- `workspace delete --json` emits one JSON object with per-step status fields: `metadata_lookup`, `unmount`, `remote_delete`, `metadata`, plus top-level `ok`, `agent_id`, `remote_root`, and `error`.

`exec --json` is NDJSON, not a single JSON object. Expect one JSON object per line:

- a `start` event with `workspace` and `command`
- zero or more `output` events with `stream: "remote"` and `data`
- an `exit` event with `ok`, `exit_code`, `remote_status`, and `duration_ms`
- if the client fails before normal completion, a `client_error` event

Remote stdout and stderr are intentionally combined into the single `"remote"` stream.

## Recommended workflow for agents

1. Verify the profile: `go run ./cmd/agentic9 profile verify --profile default --json`
2. Create the workspace: `go run ./cmd/agentic9 workspace create --profile default --agent-id <id> --source <local-path> --json`
3. Read the returned `mountpoint` and operate on files through that mounted path.
4. Run remote commands with `go run ./cmd/agentic9 exec --profile default --agent-id <id> --json -- <cmd> ...`
5. If you need to rediscover the path later, use `workspace path --json`.
6. Delete the workspace when finished: `go run ./cmd/agentic9 workspace delete --profile default --agent-id <id> --json`

If a workspace exists remotely but is not mounted locally, use `mount --json` to reattach it before editing files.

## Validation

- Build: `go build ./cmd/agentic9`
- Local tests: `go test ./...`
- Real-host integration is opt-in through `go test ./integration -v` with the `AGENTIC9_IT_*` environment variables described in `README.md`

## References

- Read `README.md` for the end-to-end workflow, config examples, and integration-test setup.
- Read `cmd/agentic9/main.go` if you need the exact flag set or JSON field names for a command.
