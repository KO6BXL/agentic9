# agentic9 skill retest

Retest date: 2026-04-04

I reran the same workflow after the skill updates:

1. Read the updated `agentic9-cli` skill.
2. Created a minimal Plan 9 hello-world project.
3. Verified the `local` profile with `AGENTIC9_SECRET=locallocal`.
4. Created a workspace with `workspace create --seed-path ... --json`.
5. Ran remote commands with `exec --json`.
6. Built and ran the program on the 9front host.

## Result

The core workflow now works cleanly. The hello-world program built and ran successfully on the remote Plan 9 host and printed:

```text
hello, plan 9
```

## What is improved

- Launcher guidance is better.
  The skill now correctly says to use the installed `agentic9` binary outside the `agentic9` source tree.

- Secret handling guidance is better.
  The skill now explicitly treats an empty `secret_env` as a blocker and tells the agent to ask the user rather than guessing.

- Workspace editing guidance is better.
  The new `seed-path` plus canonical `project_root` explanation is much clearer than the old `source` wording.

- Benign warning handling is better.
  During `exec`, the old `/mnt/term/dev` noise was surfaced in the JSON `warnings` array instead of cluttering normal remote output.

- The cookbook examples helped with the successful path.
  Using `rc -lc 'pwd; ls -l'` and `rc -lc 'mk clean; mk && ./hello'` matched real behavior.

## Remaining issue

- One cookbook example is wrong for this environment.
  The skill suggests:
  `go run ./cmd/agentic9 exec --profile default --agent-id <id> --json -- rc -lc 'which mk; which 8c; which 6c'`

  In the tested `rc` environment, `which` is not available, so this fails with:

  ```text
  /rc/lib/rcmain:24 *eval*:1: which: directory entry not found: './which'
  ```

  That means the “check tool availability before building” example is currently misleading. It should be replaced with something that exists in this environment, or removed.

## Suggested fix

- Replace the `which` cookbook example with a command that is valid in 9front `rc`.
- If you want a simple availability check, test actual commands directly, for example by invoking them with a harmless flag or by checking known paths instead of assuming `which` exists.
