# Configuration

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

Make sure the secret is available to non-interactive shell execution too. Do not rely on a late `~/.bashrc` export that only runs for interactive shells.

Rules:

- each profile must set `cpu_host`, `auth_host`, `user`, and `auth_domain`
- set exactly one of `secret_env` or `secret_command`
- `remote_base` defaults to `/usr/$user/agentic9/workspaces`
