# agentic9

`agentic9` is a Go CLI for driving agent work on a 9front machine from Linux.

It is currently in **Alpha**: the core auth, remote exec, exportfs mount, and workspace lifecycle paths exist and test locally, but real-host filesystem coverage and workflow hardening are still incomplete.

Current compatibility markers:

- CLI version: `dev` by default in local source builds, overrideable at build time
- expected skill version: `2026-04-05.1`

## What it does

- verifies a configured 9front profile using real `dp9ik` and TLS-PSK `rcpu`
- creates one remote workspace per agent
- mounts that workspace locally through FUSE as the agent-visible project root
- runs remote commands inside the workspace with `agentic9 exec`
- tracks local workspace and mount metadata for reuse and cleanup

## Build and test

```bash
go build ./cmd/agentic9
go test ./...
```

If you are not inside the `agentic9` source tree, use the installed `agentic9` binary instead of `go run ./cmd/agentic9`.

## Configuration

Create `~/.config/agentic9/config.toml`:

```toml
[profiles.default]
cpu_host = "cpu.example.net"
auth_host = "auth.example.net"
user = "glenda"
auth_domain = "example.net"
secret_env = "AGENTIC9_SECRET"
```

Then export the secret:

```bash
export AGENTIC9_SECRET='your-9front-secret'
```

Make sure the secret is available to non-interactive shell execution too. Do not rely on a late `~/.bashrc` export that only runs for interactive shells.

Rules:

- each profile must set `cpu_host`, `auth_host`, `user`, and `auth_domain`
- set exactly one of `secret_env` or `secret_command`
- `remote_base` defaults to `/usr/$user/agentic9/workspaces`

Check the active CLI/skill contract before automation:

```bash
go run ./cmd/agentic9 version --json
```

Use the real configured profile name from `~/.config/agentic9/config.toml`. `default` below is only an example profile name.

## Typical workflow

```bash
go run ./cmd/agentic9 version --json
go run ./cmd/agentic9 profile verify --profile <name> --json
go run ./cmd/agentic9 workspace create --profile <name> --agent-id agent-123 --seed-path /path/to/src --json
go run ./cmd/agentic9 workspace status --profile <name> --agent-id agent-123 --json
go run ./cmd/agentic9 exec --profile <name> --agent-id agent-123 --json -- rc -lc 'mk test'
```

`workspace create --json` returns the mounted path as `project_root` and `mountpoint`. Treat that mounted path as the canonical local project root the agent should edit. The `seed_path` tree is copied into the remote project root once during creation; after that, edits under `project_root` are the writes that flow through to Plan 9, and `exec` runs from the matching repo-relative root on the remote host.

The CLI still accepts the older `--source` and `--mountpoint` flags, but `--seed-path` and `--project-root` make the intended model more explicit.

`exec --json` emits NDJSON with `start`, `output`, and `exit` events. Known benign non-interactive `/mnt/term/dev` bind warnings are suppressed from `output` events and reported as structured `warnings` on the final `exit` event instead.

`workspace status --json` reports local metadata state, runtime mount state, remote workspace reachability, and the canonical local `project_root` when one is active.

## Project root model

`agentic9` is intended to present one repo-relative project root to the agent:

- locally, the agent edits the mounted `project_root`
- remotely, `agentic9 exec` runs from the matching Plan 9 project root
- repo-relative paths should mean the same thing in both places, for example `./main.c`

The initial `--seed-path` is only a bootstrap input. Once the workspace is created, the mounted `project_root` is the canonical tree. Changes written there go through the mount to Plan 9 and are the files seen by remote builds and tests.

For non-trivial file edits or content generation, write through the mounted `project_root`. Do not treat `agentic9 exec` stdin piping as the primary file-upload path; it is less reliable than editing files directly in the mounted tree.

Workspaces are intended to persist across builds and editing sessions. Do not treat `workspace delete` as the default final step after a successful build. Use it only when you explicitly want to remove the remote project root and clear the associated local metadata.

## Cleanup

If you need to reconnect to an existing workspace later, use `workspace path` or `mount` rather than creating a new one. Only run:

```bash
go run ./cmd/agentic9 workspace delete --profile <name> --agent-id agent-123 --json
```

when you deliberately want to destroy that project root on the Plan 9 host.

## More detail

- CLI usage details and automation guidance: [SKILL.md](/home/me1on/proj/agentic9/SKILL.md)
- current implementation backlog: [TODO.md](/home/me1on/proj/agentic9/TODO.md)
