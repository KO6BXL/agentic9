# TODO

This file is the current implementation backlog for `agentic9`.

It is intended to be handed to other agents or engineers directly. Every item below reflects the current repo state as of the latest local inspection, not the earlier scaffold-only state.

## Current state

Project stage:

- `agentic9` is currently in **Alpha**
- the core auth, remote exec, exportfs mount, detached mount helper, and workspace lifecycle flows are implemented
- local automated coverage is good for the current codebase, but real-host filesystem and durability coverage are still incomplete

Implemented and testable locally:

- config parsing and secret loading in [config.go](/home/me1on/proj/agentic9/internal/config/config.go)
- `dp9ik` auth flow in [client.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/client.go)
- AuthPAK and auth wire helpers in [pak.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/pak.go), [wire.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/wire.go), and [dp9ik.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/dp9ik.go)
- native TLS-PSK `rcpu` client in [client.go](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go)
- remote exec framing and sentinel parsing in [runner.go](/home/me1on/proj/agentic9/internal/remoteexec/runner.go)
- 9P codec and client in [codec.go](/home/me1on/proj/agentic9/internal/ninep/codec.go), [client.go](/home/me1on/proj/agentic9/internal/ninep/client.go), and [dir.go](/home/me1on/proj/agentic9/internal/ninep/dir.go)
- exportfs client with listing support in [client.go](/home/me1on/proj/agentic9/internal/exportfs/client.go)
- Linux FUSE adapter in [fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go)
- detached mount helper lifecycle with runtime PID/state tracking in [main.go](/home/me1on/proj/agentic9/cmd/agentic9/main.go) and [runtime.go](/home/me1on/proj/agentic9/internal/fusefs/runtime.go)
- CLI workspace lifecycle commands: `profile verify`, `workspace create`, `workspace path`, `mount`, `unmount`, `exec`, and `workspace delete`
- local fake-stack tests for auth, TLS-PSK rcpu, exec, and exportfs

Already validated:

- `go build ./cmd/agentic9` succeeds locally
- `go test ./...` passes locally
- there are opt-in real-host integration tests for:
  - `Verify` + simple `Exec`
  - exportfs create/read/stat/rename/list round-trip
  - `workspace create` -> mounted `project_root` file access -> `workspace path` -> `workspace delete` when `AGENTIC9_IT_WORKSPACE=1`

Still not broadly validated:

- repeated or long-lived mount traffic against a real 9front host
- exportfs disconnect handling under real mount traffic
- symlink behavior against a real 9front host
- failure-path coverage for real-host exec and workspace operations

## Testing levels available right now

Use this matrix when deciding what another agent can safely validate.

### Level 1: Local unit and fake-stack tests

Command:

```bash
go test ./...
```

What this proves:

- auth packet handling
- AuthPAK flow against a fake server
- TLS-PSK rcpu client against a fake server
- exec output streaming and callback cancellation
- 9P message encoding/decoding
- exportfs directory listing logic against a fake 9P stream

What it does not prove:

- compatibility with a real 9front host
- FUSE stability under real workloads
- workspace lifecycle correctness

### Level 2: Real-host auth, exec, and exportfs round-trip

Command:

```bash
export AGENTIC9_IT_CPU_HOST='...'
export AGENTIC9_IT_AUTH_HOST='...'
export AGENTIC9_IT_USER='...'
export AGENTIC9_IT_AUTH_DOMAIN='...'
export AGENTIC9_IT_SECRET='...'
go test ./integration -v
```

What this proves:

- real `dp9ik` authentication
- real TLS-PSK `rcpu` handshake
- simple remote command execution
- exportfs-backed create/read/stat/rename/list behavior against a real host

What it does not prove:

- FUSE correctness
- detached workspace durability under repeated real workloads
- disconnect and symlink behavior

### Level 3: Manual foreground mount testing

Run `agentic9 mount` in one terminal without `--json`, then use the mounted `project_root` from another terminal.

What this proves:

- exportfs transport is at least alive enough for mount-backed access
- file browsing and simple file operations can be tried manually

What it does not prove:

- persistent detached mount lifecycle
- agent automation compatibility

### Level 4: Full agent workflow

There is now an opt-in real-host workspace lifecycle test for create/path/delete plus mounted file access when `AGENTIC9_IT_WORKSPACE=1`, but broader long-lived and failure-path validation is still missing.

## Priority 0: top blockers

These are the most important remaining items to hand off first.

### 0.3 Expand real-host integration beyond the current happy-path coverage

Problem:

- real-host coverage now exists for verify/exec, exportfs round-trip, and a gated workspace lifecycle path, but it is still concentrated on happy paths
- mount-backed file operations, disconnect behavior, symlink behavior, and more failure cases are not covered against a real 9front box

Why it matters:

- fake-stack tests are useful, but they do not establish wire compatibility for the filesystem path
- the current real-host tests are enough to prove basic bring-up, not operational durability

What to do:

- add opt-in real-host integration tests for:
  - mount-backed file operations if feasible in CI/manual runs
  - exportfs disconnect and retry/failure behavior
  - symlink behavior, or explicit assertion of `ENOSYS`
  - workspace failure paths and cleanup behavior

Acceptance criteria:

- real-host failures in the filesystem path are caught by tests before manual debugging

## Priority 1: filesystem correctness

### 1.1 Implement symlink support or return `ENOSYS` cleanly

Problem:

- [`Symlink`](/home/me1on/proj/agentic9/internal/exportfs/client.go#L174) and [`Readlink`](/home/me1on/proj/agentic9/internal/exportfs/client.go#L181) are still unimplemented

Why it matters:

- editors, language tools, and source trees sometimes rely on symlinks
- current FUSE wiring advertises symlink operations via [fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go), but the backend does not support them

What to do:

- determine whether 9front `exportfs` plus the mounted remote filesystem support symlink create/read in the way this client expects
- if yes, implement both operations
- if no, map them to stable, explicit errors at the FUSE boundary

Acceptance criteria:

- symlink operations either work correctly against a real host or fail predictably with documented behavior

### 1.2 Harden exportfs disconnect handling

Problem:

- the exportfs client caches a single `*ninep.Client` in [client.go](/home/me1on/proj/agentic9/internal/exportfs/client.go#L22)
- if the underlying stream dies, the code does not explicitly poison the mount or turn subsequent filesystem ops into consistent `EIO`

Why it matters:

- network failures and server disconnects are normal
- current behavior is likely to surface as inconsistent low-level errors

What to do:

- define mount health behavior
- when the exportfs stream dies:
  - mark the backend unhealthy
  - fail subsequent operations consistently
- wire that into the FUSE errno mapping

Acceptance criteria:

- after a forced remote disconnect, new file operations fail consistently and understandably

### 1.3 Verify rename, create, and file-handle semantics against real 9front exportfs

Problem:

- [`Create`](/home/me1on/proj/agentic9/internal/exportfs/client.go#L109) reuses the walked directory fid after `TCREATE`
- [`Rename`](/home/me1on/proj/agentic9/internal/exportfs/client.go#L141) relies on `wstat` name mutation and may not be correct for cross-directory rename semantics

Why it matters:

- these behaviors may compile and pass fake tests while still being wrong against the actual server

What to do:

- validate create/open/rename against a real 9front host
- especially verify:
  - file handle validity after create
  - cross-directory rename behavior
  - truncate/chmod semantics

Acceptance criteria:

- create, overwrite, rename, and truncate behave correctly in manual and integration testing

## Priority 2: CLI and workflow hardening

### 2.2 Centralize `agent-id` validation and workspace invariants

Problem:

- the CLI now uses [ValidateAgentID](/home/me1on/proj/agentic9/internal/workspace/state.go) consistently, but the accepted format is still only locally enforced and not validated against real workflow needs

Why it matters:

- workspace paths and remote paths are derived from `agent-id`
- weak validation invites path and state bugs

What to do:

- define allowed `agent-id` format
- validate centrally in one place
- apply it consistently across:
  - `workspace create`
  - `workspace delete`
  - `workspace path`
  - `mount`
  - `exec`

Acceptance criteria:

- all commands reject invalid IDs consistently

### 2.3 Tighten JSON output contracts

Problem:

- CLI JSON output exists, but the shapes are not fully test-locked yet

Why it matters:

- agents depend on stable machine-readable output

What to do:

- define and test stable JSON shapes for:
  - `profile verify`
  - `workspace create`
  - `workspace delete`
  - `workspace path`
  - `mount`
  - `unmount`
  - `exec` NDJSON events
- verify that no stray stderr output corrupts machine-readable mode

Acceptance criteria:

- all `--json` commands have explicit shape tests

## Priority 3: protocol and 9P hardening

### 3.1 Decide whether the 9P client stays serialized or becomes concurrent

Problem:

- the 9P client in [client.go](/home/me1on/proj/agentic9/internal/ninep/client.go) still uses a synchronous request/response model with a single mutex

Why it matters:

- FUSE can drive concurrent operations
- serialization may be acceptable for correctness, but that needs to be intentional and documented

What to do:

- choose one:
  - keep the client serialized and document the tradeoff
  - add concurrent request handling with tag demultiplexing
- in either case:
  - validate `msize` negotiation
  - tighten malformed-response handling
  - verify fid/tag cleanup

Acceptance criteria:

- the client behaves predictably under repeated filesystem traffic

### 3.2 Add more real-host exec coverage

Problem:

- real-host exec coverage is currently just `echo marker`

Why it matters:

- exec is the main test/build path for agents

What to do:

- add real-host tests for:
  - failing command with non-empty rc status
  - context cancellation
  - large output streaming
  - quoting of arguments with spaces and quotes

Acceptance criteria:

- `exec` behavior is validated under both success and failure cases on a real 9front host

## Priority 4: docs and ops

### 4.1 Update docs to match the real current state

Problem:

- docs improved, but the project has changed fast and the remaining gaps are now different from the original plan

What to do:

- keep [README.md](/home/me1on/proj/agentic9/README.md) aligned with:
  - what is genuinely implemented
  - what requires manual foreground mount testing
  - what is not yet durable for agent workflows
- add a protocol/ops doc if transport debugging becomes frequent

Acceptance criteria:

- another engineer can tell from docs exactly what is safe to rely on today

## Suggested order for parallel agent work

These tasks can be given to different agents with minimal overlap:

1. Mount lifecycle redesign
   Scope:
   - [main.go](/home/me1on/proj/agentic9/cmd/agentic9/main.go)
   - [fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go)
   - workspace runtime metadata

2. Real-host integration expansion
   Scope:
   - [integration_test.go](/home/me1on/proj/agentic9/integration/integration_test.go)
   - helper fixtures only

3. Exportfs symlink and disconnect handling
   Scope:
   - [client.go](/home/me1on/proj/agentic9/internal/exportfs/client.go)
   - [fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go)

4. CLI JSON and workspace hardening
   Scope:
   - [main.go](/home/me1on/proj/agentic9/cmd/agentic9/main.go)
   - [state.go](/home/me1on/proj/agentic9/internal/workspace/state.go)

## Immediate manual checks

Before handing tasks off, these are the most useful manual checks to run against a real host:

1. `agentic9 profile verify --profile <name> --json`
2. `agentic9 exec --profile <name> --agent-id test --json -- echo ok`
3. Foreground `agentic9 mount --profile <name> --agent-id test --project-root /tmp/a9mnt`
4. From another shell: `ls`, `cat`, `touch`, `mv`, `rm` under `/tmp/a9mnt`
5. Confirm whether symlink operations work or fail

## Short bug list

- real symlink support is still unimplemented; the current client now returns stable `ENOSYS` behavior through FUSE instead of generic errors.
- real-host integration now covers verify/exec, exportfs round-trip, and a gated workspace lifecycle path, but long-lived mount behavior and failure-path coverage are still thin.
