# Tooling Issues

This file records the problems encountered while building and testing this project on Plan 9 through the local `agentic9` workflow.

## Skill discovery

- The `agentic9` skill was installed at `/home/me1on/.agents/skills/agentic9`, but it was not listed in the session's advertised available skills.

## Shell environment

- `AGENTIC9_SECRET` was added in `~/.bashrc`, but `~/.bashrc` returns early for non-interactive shells before that export line runs.
- As a result, `agentic9` commands launched through non-interactive shell execution could not see the secret unless the export line was loaded explicitly.

## agentic9 workspace issues

- `agentic9 workspace create --seed-path ...` repeatedly failed during copy-once seeding with `input/output error`.
- The failures happened on different files in different attempts, including `bin/ssg` and `content/index.md`.
- These failures left partially copied remote workspaces behind.

## agentic9 metadata issues

- After a workspace reported successful creation and mounting, `agentic9 workspace path --json` failed because the expected metadata file under `~/.local/state/agentic9/workspaces/...` did not exist.
- In practice, the mount state reported by `workspace create` was more reliable than `workspace path`.

## agentic9 exec transport issues

- Large `agentic9 exec --json` commands could fail with `remote session ended before exit sentinel`.
- Piping stdin through `agentic9 exec` was not reliable enough to use as a file upload path.
- The stable workaround was to upload content in smaller `rc` heredoc chunks.

## Remote command noise

- Successful remote commands frequently emitted benign bind warnings about `/mnt/term/dev`:
  - `bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'`
  - `bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'`
- These warnings did not block successful builds or tests, but they did add noise while diagnosing real failures.
