# AGENTIC9 Test Notes

## Profile Verification

Initial verification failed:

```sh
agentic9 profile verify --profile local --json
```

Error:

```text
secret env "AGENTIC9_SECRET" is empty for profile "local"; export AGENTIC9_SECRET or update the profile to use secret_command
```

After supplying `AGENTIC9_SECRET=locallocal`, verification succeeded.

## Workspace Creation Issue

Remote validation is currently blocked by `agentic9 workspace create`.

Command used:

```sh
XDG_RUNTIME_DIR=/tmp/a9rt-serial AGENTIC9_SECRET=locallocal \
agentic9 workspace create --profile local --agent-id calc-20260405c \
  --seed-path /home/me1on/proj/9thing --json
```

Observed error:

```text
mkdir /tmp/a9rt-serial/agentic9/local/calc-20260405c: file exists
```

Follow-up status check still reported:

- no local metadata
- no mounted workspace
- no remote workspace created

This happened even with a clean `XDG_RUNTIME_DIR`, so the failure appears to be
inside the CLI workspace creation path rather than stale user state alone.

## Local Plan 9 Toolchain Wrapper Noise

Local builds succeeded with:

```sh
mk
```

But both wrapper scripts printed shell errors during successful compilation and
linking:

```text
/usr/lib/plan9/bin/9c: line 60: -v: command not found
/usr/lib/plan9/bin/9l: line 156: -v: command not found
/usr/lib/plan9/bin/9l: line 341: -v: command not found
```

Despite that noise, the calculator built and ran correctly locally.
