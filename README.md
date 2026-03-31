# agentic9

`agentic9` is a Linux-hosted Go tool for driving agent work on a 9front machine.

The intended model is:

- use the CLI to create one remote workspace per agent
- mount that workspace locally through FUSE
- let the agent edit files through the mount
- run builds and tests remotely through `agentic9 exec`

## Current status

The project has working local structure, config loading, workspace metadata handling, sync logic, remote-exec framing, a 9P codec/client, and a FUSE adapter.

The real 9front network path is not finished yet:

- `dp9ik` authentication is scaffolded but not implemented end-to-end
- the native TLS `rcpu` client currently returns `ErrUnimplemented`
- remote `profile verify`, `exec`, and `mount` flows will stop there today

So the examples below document the intended UX, but the actual 9front connection layer still needs to be completed in `internal/auth/dp9ik` and `internal/transport/tlsrcpu`.

## Install

Build the CLI:

```bash
go build ./cmd/agentic9
```

Run the test suite:

```bash
go test ./...
```

## Configuration

Create `~/.config/agentic9/config.toml`:

```toml
[profiles.default]
cpu_host = "cpu.example.net"
auth_host = "auth.example.net"
user = "glenda"
auth_domain = "example.net"
remote_base = "/usr/glenda/agentic9/workspaces"
secret_env = "AGENTIC9_SECRET"
```

Then export the secret source:

```bash
export AGENTIC9_SECRET='your-9front-secret'
```

You can also use a command instead of an environment variable:

```toml
[profiles.default]
cpu_host = "cpu.example.net"
auth_host = "auth.example.net"
user = "glenda"
auth_domain = "example.net"
secret_command = ["pass", "show", "9front/glenda"]
```

Rules:

- exactly one of `secret_env` or `secret_command` must be set
- `remote_base` defaults to `/usr/$user/agentic9/workspaces`
- plaintext secrets in the config file are not supported

## Commands

### Verify a profile

```bash
./agentic9 profile verify --profile default --json
```

Intended JSON shape:

```json
{"ok":true,"profile":"default","cpu_host":"cpu.example.net","auth_host":"auth.example.net","user":"glenda","error":"","configPath":"/home/me/.config/agentic9/config.toml"}
```

Current behavior:

- this validates local config and secret loading
- the remote auth/transport step currently returns an unimplemented error

### Create a workspace

```bash
./agentic9 workspace create \
  --profile default \
  --agent-id agent-123 \
  --source /home/me/src/project \
  --json
```

Intended result:

```json
{"ok":true,"agent_id":"agent-123","remote_root":"/usr/glenda/agentic9/workspaces/agent-123/root","mountpoint":"/run/user/1000/agentic9/default/agent-123"}
```

Intended behavior:

- ensure the remote workspace directory exists
- mount the remote tree locally
- copy the local source tree into the mounted tree
- write local workspace metadata under `~/.local/state/agentic9/workspaces`

### Find a workspace path

```bash
./agentic9 workspace path --profile default --agent-id agent-123 --json
```

### Mount an existing workspace

```bash
./agentic9 mount \
  --profile default \
  --agent-id agent-123 \
  --mountpoint /tmp/agentic9-agent-123 \
  --json
```

### Unmount it

```bash
./agentic9 unmount --mountpoint /tmp/agentic9-agent-123 --json
```

### Run a remote command

```bash
./agentic9 exec \
  --profile default \
  --agent-id agent-123 \
  --json \
  -- mk test
```

Intended NDJSON stream:

```json
{"type":"start","workspace":"/usr/glenda/agentic9/workspaces/agent-123/root","command":["mk","test"]}
{"type":"output","stream":"remote","data":"...combined stdout/stderr chunk..."}
{"type":"exit","ok":true,"exit_code":0,"remote_status":"","duration_ms":1234}
```

Notes:

- remote stdout and stderr are intentionally modeled as a single combined stream
- `exit_code` is `0` when remote `rc` status is empty, otherwise `1`
- the raw 9front `rc` status string is returned as `remote_status`

### Delete a workspace

```bash
./agentic9 workspace delete --profile default --agent-id agent-123 --json
```

## Intended workflow

1. Configure a `default` profile.
2. Verify the profile.
3. Create a per-agent workspace from a local source tree.
4. Point the agent at the mounted workspace path.
5. Use `agentic9 exec` to run `mk`, tests, or other 9front-side commands.
6. Delete the workspace when the task is finished.

## Repository layout

```text
cmd/agentic9                CLI entrypoint
internal/config             config parsing and secret loading
internal/auth/dp9ik         auth packet types and scaffolding
internal/transport/rcpu     transport interfaces
internal/transport/tlsrcpu  native 9front client scaffold
internal/remoteexec         rc script generation and sentinel parsing
internal/ninep              9P message codec and client
internal/exportfs           exportfs-backed remote filesystem client
internal/fusefs             Linux FUSE adapter
internal/workspace          workspace metadata and path rules
internal/sync               local tree copy logic
integration/                integration test placeholder
```

## What still needs to be finished

The major missing piece is the real 9front wire implementation:

- `dp9ik` auth exchange
- TLS `rcpu` session setup
- `exportfs` session plumbing over that transport
- full remote directory listing and symlink support
- integration tests against a real 9front host

Once that is in place, the CLI surface in this repo is already structured to expose the intended workflow.
