# agentic9

`agentic9` is a Linux-hosted Go tool for driving agent work on a 9front machine.

The intended model is:

- use the CLI to create one remote workspace per agent
- mount that workspace locally through FUSE
- let the agent edit files through the mount
- run builds and tests remotely through `agentic9 exec`

## Current status

The project now has a working end-to-end 9front connection path:

- real `dp9ik` authentication, including AuthPAK, ticket exchange, authenticator exchange, and TLS session-secret derivation
- a native TLS-PSK `rcpu` client for `profile verify`, remote `exec`, remote directory setup, and `exportfs`
- detached mount helpers with runtime PID/state tracking for `workspace create` and `mount --json`
- local fake-stack tests for auth, exec streaming, callback cancellation, and exportfs open/stat

Remaining work is mostly outside the initial auth/transport bring-up: broader exportfs coverage, more 9P hardening, and fuller integration coverage against real hosts.

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
- `agent-id` must start with an ASCII letter or digit, may contain only ASCII letters, digits, `.`, `_`, and `-`, and must be at most 64 characters

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

- this performs the real `dp9ik` plus TLS-PSK `rcpu` handshake against the configured host

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
{"ok":true,"agent_id":"agent-123","remote_root":"/usr/glenda/agentic9/workspaces/agent-123/root","mountpoint":"/run/user/1000/agentic9/default/agent-123","mounted":true,"pid":12345}
```

Intended behavior:

- ensure the remote workspace directory exists
- start a detached mount helper and wait for it to become ready
- copy the local source tree into the mounted tree
- write local workspace metadata under `~/.local/state/agentic9/workspaces`

### Find a workspace path

```bash
./agentic9 workspace path --profile default --agent-id agent-123 --json
```

When the workspace is mounted, JSON output includes `mounted: true`, `mountpoint`, `remote_root`, and `pid`.
If the workspace metadata exists but no live mount is present, the command returns `{"ok":false,...,"mounted":false,"error":"workspace is not mounted"}`.

### Mount an existing workspace

```bash
./agentic9 mount \
  --profile default \
  --agent-id agent-123 \
  --mountpoint /tmp/agentic9-agent-123 \
  --json
```

Behavior:

- with `--json`, `mount` starts a detached helper process, waits for the mount to become ready, and returns the helper PID
- without `--json`, `mount` stays in the foreground and exits when the mount is unmounted

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

`workspace delete --json` now reports per-step status for metadata lookup, local unmount, remote delete, and final metadata cleanup so partial failures are visible to callers.

## Intended workflow

1. Configure a `default` profile.
2. Verify the profile.
3. Create a per-agent workspace from a local source tree.
4. Point the agent at the mounted workspace path.
5. Use `agentic9 exec` to run `mk`, tests, or other 9front-side commands.
6. Delete the workspace when the task is finished.

## Integration test

The real-host integration tests are opt-in.

You can run them either by setting explicit `AGENTIC9_IT_*` variables:

```bash
export AGENTIC9_IT_CPU_HOST='cpu.example.net'
export AGENTIC9_IT_AUTH_HOST='auth.example.net'
export AGENTIC9_IT_USER='glenda'
export AGENTIC9_IT_AUTH_DOMAIN='example.net'
export AGENTIC9_IT_SECRET='your-9front-secret'
go test ./integration -v
```

Or by using a configured profile from `~/.config/agentic9/config.toml`:

```bash
export AGENTIC9_SECRET='your-9front-secret'
export AGENTIC9_IT_PROFILE='local'
go test ./integration -v
```

If `AGENTIC9_IT_PROFILE` is unset, the integration package defaults to profile `local`.
The workspace lifecycle test is additionally gated behind `AGENTIC9_IT_WORKSPACE=1` because it depends on local FUSE availability.

## Repository layout

```text
cmd/agentic9                CLI entrypoint
internal/config             config parsing and secret loading
internal/auth/dp9ik         9front dp9ik auth client, wire types, and AuthPAK
internal/transport/rcpu     transport interfaces
internal/transport/tlsrcpu  native 9front rcpu-over-TLS-PSK client
internal/remoteexec         rc script generation and sentinel parsing
internal/ninep              9P message codec and client
internal/exportfs           exportfs-backed remote filesystem client
internal/fusefs             Linux FUSE adapter
internal/workspace          workspace metadata and path rules
internal/sync               local tree copy logic
integration/                integration test placeholder
```

## What still needs to be finished

- broader exportfs coverage, especially symlink behavior and disconnect handling
- more 9P hardening under sustained or concurrent filesystem traffic
- broader integration coverage against a real 9front host
