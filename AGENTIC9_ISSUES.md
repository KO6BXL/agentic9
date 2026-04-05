# agentic9 CLI / Skill Issues

Observed during a real run on April 5, 2026 using profile `local`.

## Bugs

### 1. `workspace path --json` failed immediately after successful `workspace create --json`

`agentic9 workspace create --profile local --agent-id webfs --seed-path . --json`
returned success, including:

- `ok: true`
- `mounted: true`
- `project_root: /run/user/1000/agentic9/local/webfs`

But the follow-up:

`agentic9 workspace path --profile local --agent-id webfs --json`

failed with:

`open /home/me1on/.local/state/agentic9/workspaces/local/webfs.json: no such file or directory`

That looks like a metadata persistence bug or a race between mount success and local state write.

### 2. Mounted workspace accepted some writes but rejected nested file creation

Writing files at the mounted workspace root worked:

- `webfs.c`
- `mkfile`
- `README.md`
- `index.html`

But creating `/run/user/1000/agentic9/local/webfs/public/index.html` consistently failed even after the `public/` directory existed.

That suggests either:

- a bug in the mount layer for nested file creation, or
- an incompatibility between the mounted filesystem and certain local write/edit operations.

Either way, it is surprising behavior from the mounted-path workflow.

### 3. Repeated benign bind warnings on remote `exec`

Many successful `agentic9 exec` calls emitted warnings like:

- `bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'`
- `bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'`

The skill correctly says these can be benign, but from a CLI perspective this is still noisy and looks like failure unless the caller already knows to ignore it.

## Potential Features

### 1. Detached or managed remote process support

Running a long-lived remote process for testing worked, but cleanup was awkward after interruption. A more explicit primitive would help, for example:

- `exec --detach`
- `exec ps`
- `exec kill <id>`
- named background jobs scoped to a workspace

That would make server testing much easier and safer.

### 2. Better workspace health/introspection command

It would help to have a single command that reports:

- whether the remote workspace exists
- whether the mount is active
- whether metadata exists locally
- the canonical local `project_root`

Right now `workspace create` can say the workspace is mounted while `workspace path` still fails, which is confusing.

### 3. Optional smoke-test helpers for remote networking

In this run, verifying the HTTP server from inside the remote exec environment was harder than expected because `hget` depended on `/mnt/web`, which was unavailable there. A lightweight helper or documented pattern for network smoke tests would improve the workflow.

## UX Flaws

### 1. Skill examples assume `default` profile

The skill text repeatedly uses `--profile default`, but the real configured profile here was `local`. That is not wrong, but it nudges the operator toward a profile name that may not exist.

A better pattern would be:

- say "use the configured profile"
- show `default` only as an example
- explicitly remind the agent to inspect `~/.config/agentic9/config.toml` for the actual profile name

### 2. The mounted path is usable even when metadata is broken, but that recovery path is implicit

In practice, the `project_root` returned by `workspace create --json` was enough to continue despite the missing metadata file. That was workable, but not obvious from the CLI behavior itself.

The CLI could surface a clearer message when `workspace path` fails but a previously mounted path is still known or detectable.

### 3. Successful `mk` workflow was not obvious from first-pass build setup

The remote host’s native `mk` conventions differed from the plan9port-style `mkfile` I first used. This is not exactly an `agentic9` bug, but it does expose a workflow gap: when the tool is explicitly for real Plan 9 work, a short note about "plan9port vs native 9front mkfile differences" would reduce friction.

### 4. Interruptions leave ambiguity about remote process state

After an interrupted smoke test, I had to manually check whether `webfs` was still running remotely. The tool should ideally make that state easier to inspect or clean up after interrupted commands.

## Summary

Most important issue:

- `workspace create --json` succeeded, but `workspace path --json` immediately failed because the local metadata file was missing.

Most important UX gap:

- remote workspace and remote process state are harder to inspect and manage than they should be during iterative testing.
