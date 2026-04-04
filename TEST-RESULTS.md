# agentic9 skill feedback

These notes are about the `agentic9-cli` skill/workflow, based on using it to create a simple Plan 9 hello-world program and run it on the remote 9front host.

## What worked

- The skill correctly pushed me toward `profile verify`, `workspace create`, `exec`, and `workspace delete`.
- The JSON-first guidance was useful and matched the actual CLI behavior closely enough to automate the flow.
- The config shape and high-level command model were clear.

## Problems worth fixing

- Missing secret-recovery guidance.
  The first real blocker was `secret env "AGENTIC9_SECRET" is empty`. The skill says the profile must define `secret_env` or `secret_command`, but it does not say what the agent should do when the env var is missing. It should explicitly instruct the agent to stop and ask the user for the secret, or to use the configured `secret_command` if one exists.

- The “prefer `go run ./cmd/agentic9 ...`” instruction is too repo-specific.
  That advice only makes sense inside the `agentic9` repo. In this repo, the correct action was to use the installed `agentic9` binary. The skill should say: use `go run ./cmd/agentic9` only when the current repo is actually the `agentic9` source tree; otherwise use the installed CLI.

- The mounted-workspace editing model is underspecified.
  The skill says to create a workspace from `--source <local-path>` and then “operate on files through that mounted path”, but it does not clearly warn that later edits must target the mountpoint, not the original source directory, if you want those edits reflected remotely. I initially edited the local repo, then had to manually copy the changed file into the mounted workspace. The skill should state this explicitly.

- Common non-fatal `exec` warnings should be documented.
  Remote `exec` printed:
  `bind: /mnt/term/dev/cons: file does not exist: '/mnt/term/dev'`
  `bind: /mnt/term/dev: file does not exist: '/mnt/term/dev'`
  The command still worked. The skill should mention these warnings can appear in non-interactive runs and are not necessarily fatal, so agents do not waste time debugging them.

- There should be a minimal “remote command cookbook”.
  For a first-time use, it would help to include one or two concrete examples like:
  `agentic9 exec --profile local --agent-id test --json -- rc -lc 'pwd; ls -l'`
  and
  `agentic9 exec --profile local --agent-id test --json -- rc -lc 'mk && ./hello'`
  The current skill describes the command model, but a concrete example would reduce ambiguity around quoting and shell choice.

## Suggested edits

- Add a short “If verify fails” section covering empty/missing secret env vars.
- Narrow the `go run ./cmd/agentic9` recommendation so it only applies in the `agentic9` repo.
- Add an explicit note that `workspace create --source` copies the source tree once, and subsequent edits should be made in the mounted workspace path if the agent wants the remote host to see them.
- Add a note about the benign `/mnt/term/dev` bind warnings during `exec`.
- Add 2-3 copy-pasteable `exec` examples using `rc -lc`.
