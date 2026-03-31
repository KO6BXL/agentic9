# TODO

This file is the implementation backlog for turning the current scaffold into a working 9front host tool.

The repo already has:

- CLI wiring in [cmd/agentic9/main.go](/home/me1on/proj/agentic9/cmd/agentic9/main.go)
- config and secret loading in [internal/config/config.go](/home/me1on/proj/agentic9/internal/config/config.go)
- workspace metadata in [internal/workspace/state.go](/home/me1on/proj/agentic9/internal/workspace/state.go)
- remote exec script generation and sentinel parsing in [internal/remoteexec/runner.go](/home/me1on/proj/agentic9/internal/remoteexec/runner.go)
- a 9P codec/client in [internal/ninep/types.go](/home/me1on/proj/agentic9/internal/ninep/types.go), [internal/ninep/codec.go](/home/me1on/proj/agentic9/internal/ninep/codec.go), and [internal/ninep/client.go](/home/me1on/proj/agentic9/internal/ninep/client.go)
- an exportfs-backed filesystem abstraction in [internal/exportfs/client.go](/home/me1on/proj/agentic9/internal/exportfs/client.go)
- a Linux FUSE adapter in [internal/fusefs/fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go)
- basic unit tests and a placeholder integration test

The repo does not yet have a functioning 9front connection stack. The main blockers are `dp9ik`, native TLS `rcpu`, and real exportfs session plumbing.

## 1. Implement real `dp9ik` authentication

Status:

- [internal/auth/dp9ik/dp9ik.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/dp9ik.go) only defines a few packet structs and transcript parsing helpers.
- No actual auth server dial, AuthPAK exchange, ticket request, authenticator exchange, or secret derivation exists yet.

Why this matters:

- Nothing can talk to a real 9front auth server until this is done.
- `profile verify`, `exec`, and exportfs all depend on it.

What to implement:

- Add a real client for the 9front auth server ticket service.
- Marshal and unmarshal the auth protocol structures from `authsrv(6)` completely, not just `TicketReq`.
- Implement the AuthPAK path used by modern 9front/drawterm auth.
- Derive the shared secret material required for the TLS-PSK stage used by `rcpu`.
- Return a typed result that the transport layer can consume directly.

Recommended shape:

- Add an explicit API in [internal/auth/dp9ik/dp9ik.go](/home/me1on/proj/agentic9/internal/auth/dp9ik/dp9ik.go) such as:
  - `DialAuthServer(ctx, profile)`.
  - `Authenticate(ctx, profile, secret) (SessionKeys, error)`.
- Split low-level encoding into separate files if the auth code grows:
  - `wire.go`
  - `pak.go`
  - `client.go`

Implementation context:

- The secret loaded from config is a user secret, not a TLS secret yet.
- The actual handshake logic should follow 9front source behavior, not an inferred approximation.
- Use 9front/drawterm source as the compatibility oracle for:
  - `Ticketreq`
  - `Ticket`
  - `Authenticator`
  - AuthPAK message order
  - shared secret derivation

Completion criteria:

- `dp9ik` can talk to a real 9front auth server.
- It returns the key material needed by the rcpu TLS stage.
- Add transcript tests from captured or fixture protocol data.
- Add at least one integration test that verifies auth against a real host.

## 2. Implement native TLS `rcpu` transport

Status:

- [internal/transport/tlsrcpu/client.go](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go) validates that the loaded secret is non-empty and then returns `ErrUnimplemented`.

Why this matters:

- The CLI cannot verify profiles, run remote commands, create remote directories, or open exportfs sessions until this exists.

What to implement:

- Dial the 9front `cpu` service.
- Perform the `dp9ik` authentication stage.
- Establish the TLS-PSK session the 9front `rcpu` flow expects.
- Implement the remote script execution path used by `Exec`.
- Implement the exportfs session opener used by `OpenExportFS`.

Specific methods to finish:

- [Verify](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go:25)
- [Exec](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go:33)
- [OpenExportFS](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go:47)

Implementation context:

- `remoteexec.Runner` already expects an `rcpu.Executor`.
- `workspace create` depends on `EnsureRemoteDir`, which calls `Exec`.
- `exportfs.New(client, remoteRoot)` expects `client.OpenExportFS` to return a live `io.ReadWriteCloser`.

Important design constraints:

- `Exec` should stream remote output incrementally into the callback.
- If the callback returns an error, close the remote session immediately.
- `Verify` should be a cheap end-to-end auth and connection check.
- Timeouts and cancellation should be driven by `context.Context`.

Completion criteria:

- `profile verify --json` succeeds on a real 9front host.
- `exec --json -- echo ok` streams output and returns a valid exit event.
- `OpenExportFS` returns a working stream that the 9P client can talk to.

## 3. Make remote `exec` wire-compatible and robust

Status:

- [internal/remoteexec/runner.go](/home/me1on/proj/agentic9/internal/remoteexec/runner.go) builds a remote rc script and parses a sentinel line.
- The parser tests pass.
- The current script uses simple string joining for the command line.

Why this matters:

- Once transport exists, `exec` becomes the main way agents run tests and builds.
- Shell quoting bugs here would turn into incorrect or unsafe remote commands.

What to implement:

- Replace naive `strings.Join(command, " ")` with rc-safe argument quoting for every argument.
- Confirm the exit sentinel cannot collide with typical command output.
- Confirm remote errors before the sentinel are surfaced as `client_error`.
- Add timeout and cancellation tests once transport exists.

Specific code to change:

- [BuildScript](/home/me1on/proj/agentic9/internal/remoteexec/runner.go:52)

Implementation context:

- rc quoting rules are not POSIX shell rules.
- Do not assume Go’s `shlex`-style helpers apply here.
- The sentinel parser already handles split frames; keep that behavior intact.

Completion criteria:

- Arbitrary arguments round-trip safely through `agentic9 exec -- ...`.
- Commands containing spaces, quotes, and metacharacters are executed correctly.
- Integration tests cover success, failure, and mid-stream disconnect.

## 4. Finish the exportfs-backed filesystem client

Status:

- [internal/exportfs/client.go](/home/me1on/proj/agentic9/internal/exportfs/client.go) supports:
  - `Stat`
  - `Open`
  - `Create`
  - `Mkdir`
  - `Remove`
  - `Rename`
  - `Chmod`
  - `Truncate`
- It does not support:
  - directory listing
  - symlink creation
  - symlink reads
- Several behaviors are too naive for real use.

Why this matters:

- The FUSE layer depends on `List` for directory browsing.
- Sync and normal editor behavior often rely on directory traversal and symlinks.

What to implement:

- Implement `List` by opening a directory and decoding directory entries from 9P `read` responses.
- Implement `Symlink` and `Readlink` if exportfs/remote fs supports them.
- Confirm the right mode bits for directories, regular files, and symlinks.
- Make `Rename` correct for cross-directory moves if 9front `wstat` semantics allow it.
- Make `walkTo` fid allocation deterministic and collision-safe.

Specific methods to finish:

- [List](/home/me1on/proj/agentic9/internal/exportfs/client.go:58)
- [Symlink](/home/me1on/proj/agentic9/internal/exportfs/client.go:148)
- [Readlink](/home/me1on/proj/agentic9/internal/exportfs/client.go:154)

Implementation context:

- Right now `walkTo` derives fids from `time.Now().UnixNano()`. That is not a good long-term allocator.
- The client holds one cached `*ninep.Client`. If the stream dies, the code currently does not reconnect or mark the mount unhealthy explicitly.
- `Create` currently returns the directory fid reused as a file handle. Verify that matches 9P semantics for the server you are talking to.

Completion criteria:

- `ls`, `find`, editors, and `cp -r` work against the FUSE mount.
- Symlinks behave correctly or are explicitly mapped to `ENOSYS`.
- Remote disconnects are turned into stable, understandable errors.

## 5. Harden the 9P client for real exportfs traffic

Status:

- [internal/ninep/client.go](/home/me1on/proj/agentic9/internal/ninep/client.go) implements a synchronous request/response model with a single mutex.
- The codec in [internal/ninep/codec.go](/home/me1on/proj/agentic9/internal/ninep/codec.go) covers the subset needed so far.

Why this matters:

- FUSE can issue concurrent operations.
- exportfs sessions will expose protocol edge cases that the current minimal client does not handle yet.

What to implement:

- Add a proper fid allocator.
- Add a proper tag allocator.
- Decide whether to keep the client fully serialized or support concurrent in-flight RPCs with demultiplexing by tag.
- Validate and negotiate `msize` based on the server response.
- Improve error reporting on malformed or truncated responses.
- Add helpers for directory read decoding.

Implementation context:

- A single-mutex client is acceptable for v1 correctness if performance is not the goal.
- If serialization is kept, document the tradeoff and make sure it does not deadlock under FUSE access patterns.
- The client currently assumes 9P2000. Confirm that is exactly what 9front exportfs expects in this flow.

Completion criteria:

- The client is stable under repeated file operations from a mounted workspace.
- Fid/tag leaks do not occur.
- Large reads/writes work up to negotiated `msize`.

## 6. Make the FUSE layer correct for real filesystem workloads

Status:

- [internal/fusefs/fs.go](/home/me1on/proj/agentic9/internal/fusefs/fs.go) mounts a filesystem and wires most expected operations.
- Several backend operations still rely on incomplete exportfs support.
- Health handling is minimal.

Why this matters:

- Editors, build tools, and sync operations will stress the mount harder than the current tests do.

What to implement:

- Verify `Getattr`, `Lookup`, `Readdir`, `Open`, `Create`, `Setattr`, and `Rename` against real workloads.
- Ensure failures from a dead exportfs stream are returned as `EIO`.
- Decide whether the mount should self-poison after stream failure so subsequent ops fail consistently.
- Audit file handle lifecycle and release semantics.
- Add explicit `Release` support if needed for cleanup.

Implementation context:

- `Unmount` shells out to `fusermount3` or `fusermount`; that is acceptable on Linux, but error messages should be improved.
- `fileHandle.Read` treats `io.EOF` as a normal short read. Keep that behavior.
- Cache settings are conservative, which is fine for v1.

Completion criteria:

- Mounted workspaces behave predictably under `cp`, `mv`, `rm`, editors, and test runners.
- On forced remote disconnect, new operations fail with `EIO`.
- Unmount is reliable and idempotent.

## 7. Fix workspace lifecycle edge cases

Status:

- [cmd/agentic9/main.go](/home/me1on/proj/agentic9/cmd/agentic9/main.go) implements `workspace create`, `workspace delete`, and `workspace path`.
- The basic flow exists, but error handling is incomplete and there are some unsafe shortcuts.

Why this matters:

- This is the primary UX agents will depend on.

What to implement:

- Validate `agent-id` centrally using [ValidateAgentID](/home/me1on/proj/agentic9/internal/workspace/state.go:66) instead of ad hoc string checks.
- Make `workspace create` clean up partially created mounts and metadata on failure.
- Make `workspace delete` distinguish:
  - local unmount failure
  - remote delete failure
  - missing metadata
- Decide whether `mount` should record metadata or remain a transient operation.
- Consider adding `workspace list` for local state inspection.

Implementation context:

- `workspace delete` currently ignores `ErrUnimplemented` from remote delete, which is only there because transport is unfinished.
- Once transport works, that exception should probably be removed.
- `workspace create` defers `handle.Close()`, which means the mount is torn down before returning. That is wrong if the intent is a persistent mount after creation.

Important bug to fix:

- In [workspaceCreate](/home/me1on/proj/agentic9/cmd/agentic9/main.go:87), `defer handle.Close()` unmounts the workspace as soon as the command exits. That defeats the intended workflow and must be redesigned.

Recommended fix:

- Decide whether `workspace create` should:
  - leave the mount alive in a background process, or
  - create the workspace and sync without persisting the mount.
- The current code mixes those two models and needs one consistent choice.

Completion criteria:

- `workspace create` produces a usable workspace outcome.
- Metadata always reflects reality.
- Partial failures do not leave confusing local state behind.

## 8. Decide and implement mount process ownership

Status:

- `mount` currently mounts in-process and either returns JSON or blocks in `handle.Wait()`.
- `workspace create` also mounts in-process, then closes the mount on return.

Why this matters:

- The current code does not provide a stable long-lived mount lifecycle.
- Agents need a mount that survives the initial CLI process if the workflow is “create then edit”.

What to implement:

- Choose one ownership model:
  - foreground `mount` only, and `workspace create` does not leave a mount behind
  - background daemonized mount process with PID/state tracking
  - separate helper binary for the long-lived FUSE server
- Update metadata to record enough information to unmount reliably.
- Make `unmount` work whether the mount was created directly or via `workspace create`.

Implementation context:

- The current `Handle.PID()` just returns `os.Getpid()`, which is not enough if a detached mount process is introduced.
- If daemonizing, store pid/socket/control metadata under the runtime root.

Completion criteria:

- A mount can be created, discovered, and unmounted reliably across CLI invocations.
- `workspace create` and `mount` do not fight each other.

## 9. Improve sync behavior for bootstrap and mirror mode

Status:

- [internal/sync/sync.go](/home/me1on/proj/agentic9/internal/sync/sync.go) copies directories, regular files, and symlinks, and skips `.git`.

Why this matters:

- `workspace create` uses this code to seed the remote tree.

What to implement:

- Add more configurable ignore rules.
- Preserve executable bits and validate chmod behavior through the mounted remote fs.
- Improve mirror mode so it is safe and predictable over FUSE.
- Handle symlink creation errors explicitly if the remote backend does not support them.
- Add tests around partial sync failures and cleanup behavior.

Implementation context:

- Copying via the mounted filesystem means sync correctness depends on FUSE plus exportfs support.
- Once the mount model is fixed, revisit whether bootstrap should use the mount or a direct remote copy API for better reliability.

Completion criteria:

- A realistic local source tree can be pushed into a new remote workspace.
- Mirror mode does not accidentally destroy files due to transient mount errors.

## 10. Add real integration tests

Status:

- [integration/integration_test.go](/home/me1on/proj/agentic9/integration/integration_test.go) is only a skipped placeholder.

Why this matters:

- The missing pieces are network/protocol heavy. Unit tests alone are not enough.

What to implement:

- Add test configuration via env vars, for example:
  - `AGENTIC9_TEST_CPU_HOST`
  - `AGENTIC9_TEST_AUTH_HOST`
  - `AGENTIC9_TEST_USER`
  - `AGENTIC9_TEST_AUTH_DOMAIN`
  - `AGENTIC9_TEST_SECRET`
- Spin up test profiles from env in the integration package.
- Add end-to-end tests for:
  - profile verify
  - exec success
  - exec failure
  - workspace create
  - mount file read/write/rename/delete
  - remote disconnect handling
  - workspace delete

Implementation context:

- The tests should skip cleanly when env is absent.
- Use a dedicated remote test base path so cleanup is safe.
- Prefer unique agent IDs per test run.

Completion criteria:

- Integration tests validate the full intended workflow against a real 9front machine.

## 11. Tighten CLI behavior and JSON contracts

Status:

- CLI subcommands exist, but some runtime semantics are placeholders because transport is missing.

Why this matters:

- Agents depend on stable machine-readable output.

What to implement:

- Make all `--json` outputs stable and documented.
- Ensure command failures return correct local exit codes.
- Emit structured client-side transport errors consistently.
- Add tests around flag parsing and JSON output contracts.

Implementation context:

- `exec` already emits NDJSON-style events.
- `profile verify`, `workspace create`, and `unmount` return single JSON objects.
- Once transport is real, confirm stderr noise never corrupts JSON output.

Completion criteria:

- Machine consumers can rely on every `--json` command.
- Output shape changes are covered by tests.

## 12. Document the real protocol and operational assumptions

Status:

- [README.md](/home/me1on/proj/agentic9/README.md) documents intended usage and current limitations.

Why this matters:

- The next engineer implementing the transport needs precise protocol and deployment notes.

What to implement:

- Add an engineering doc, for example `docs/protocol.md`, that records:
  - auth flow
  - rcpu session sequence
  - exportfs session sequence
  - expected ports
  - failure cases
  - quoting and sentinel rules
- Add an ops doc with:
  - required host dependencies on Linux
  - expected services on the 9front side
  - example profile setup

Completion criteria:

- A new engineer can implement or debug transport without re-deriving the whole design from source spelunking.

## Suggested implementation order

1. `dp9ik` authentication
2. native TLS `rcpu` client
3. exportfs session plumbing
4. exportfs directory listing and symlink support
5. mount ownership redesign
6. workspace lifecycle fixes
7. integration tests
8. CLI hardening and docs

## Immediate high-priority bugs

- [workspaceCreate](/home/me1on/proj/agentic9/cmd/agentic9/main.go:125) defers `handle.Close()`, which tears down the mount as the command exits.
- [tlsrcpu.Client](/home/me1on/proj/agentic9/internal/transport/tlsrcpu/client.go) is still entirely stubbed.
- [exportfs.Client.List](/home/me1on/proj/agentic9/internal/exportfs/client.go:58) is missing, so `Readdir` cannot work.
- [integration/integration_test.go](/home/me1on/proj/agentic9/integration/integration_test.go) does not validate anything yet.
