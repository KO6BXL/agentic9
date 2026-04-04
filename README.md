# agentic9

`agentic9` is a Go CLI for driving agent work on a 9front machine from Linux.

It is currently in **Alpha**: the core auth, remote exec, exportfs mount, and workspace lifecycle paths exist and test locally, but real-host filesystem coverage and workflow hardening are still incomplete.

## What it does

- verifies a configured 9front profile using real `dp9ik` and TLS-PSK `rcpu`
- creates one remote workspace per agent
- mounts that workspace locally through FUSE
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

Rules:

- each profile must set `cpu_host`, `auth_host`, `user`, and `auth_domain`
- set exactly one of `secret_env` or `secret_command`
- `remote_base` defaults to `/usr/$user/agentic9/workspaces`

## Typical workflow

```bash
go run ./cmd/agentic9 profile verify --profile default --json
go run ./cmd/agentic9 workspace create --profile default --agent-id agent-123 --source /path/to/src --json
go run ./cmd/agentic9 exec --profile default --agent-id agent-123 --json -- rc -lc 'mk test'
go run ./cmd/agentic9 workspace delete --profile default --agent-id agent-123 --json
```

`workspace create --json` returns the mounted workspace path in both `mountpoint` and `edit_path`. The source tree is copied into that mount once; after creation, edit files under `edit_path` if you want later changes to be visible on the remote host.

`exec --json` emits NDJSON with `start`, `output`, and `exit` events. Known benign non-interactive `/mnt/term/dev` bind warnings are suppressed from `output` events and reported as structured `warnings` on the final `exit` event instead.

## More detail

- CLI usage details and automation guidance: [SKILL.md](/home/me1on/proj/agentic9/SKILL.md)
- current implementation backlog: [TODO.md](/home/me1on/proj/agentic9/TODO.md)
