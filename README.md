# lns

`lns` is a repo-first local reverse proxy manager for Caddy.

Each repo keeps its canonical service definition in `lns.json`. `lns sync` validates that file and compiles it into the global registry and generated Caddy config. No silent `generic` fallback. No opportunistic port reassignment.

## Why

Local development usually breaks down around ports:

- multiple repos want `3000`
- web and backend services drift from what the repo actually expects
- Caddy or proxy config gets out of sync with the codebase
- the real source of truth ends up buried in a global registry instead of the repo

`lns` fixes that by making the repo own its service config and making sync explicit.

## Installation

```bash
go install ./cmd/lns
```

You also need Caddy installed:

```bash
# macOS
brew install caddy

# Ubuntu / Debian
sudo apt install caddy
```

## Quick Start

```bash
# 1. Bootstrap lns.json from the current repo
cd ~/projects/my-app
lns init

# 2. Review lns.json
#    If anything is unresolved, fill it in manually or add services explicitly
lns service add api --root api --port 8000 --profile standard

# 3. Compile repo config into registry + Caddy state
lns sync

# 4. Start the proxy
lns start

# 5. Reload Caddy after later syncs
lns reload
```

After sync, services are available at:

```text
single-service repo:  http://<prefix>.localhost:8888
multi-service repo:   http://<prefix>-<service>.localhost:8888
```

If you configure the proxy HTTP port to `80`, the `:8888` suffix disappears.

## Canonical Config

`lns.json` is the source of truth for a repo.

Example:

```json
{
  "name": "my-app",
  "prefix": "my-app",
  "services": {
    "web": {
      "root": ".",
      "port": 5179,
      "profile": "hmr",
      "source": "detected",
      "status": "resolved"
    },
    "api": {
      "root": "api",
      "port": 8000,
      "profile": "standard",
      "source": "manual",
      "status": "resolved"
    }
  }
}
```

Required top-level fields:

- `name`
- `services`

Required resolved service fields:

- `root`
- `port`
- `profile`

Optional service fields:

- `hostname`
- `source`
- `status`
- `docker`
- `container_name`

Service profiles:

- `hmr`: use proxy headers needed by HMR-style web dev servers
- `standard`: use a normal reverse proxy block

`lns init` may write unresolved stubs when detection is ambiguous. `lns sync` refuses to compile unresolved services.

## Command Model

Primary workflow:

```bash
lns init
lns sync
lns status
lns reload
lns start
lns stop
lns doctor
lns service add <name> --port <port>
```

Useful supporting commands:

```bash
# Show the repo-local view if lns.json exists in the current directory
lns status

# Force the global registry view
lns status --global

# Check whether a canonical port is already owned
lns check 5179

# Export compiled Caddy config for a synced project
lns export my-app -o Caddyfile

# Show config paths
lns config

# Interactive proxy setup (proxy port + admin address)
lns setup
```

## Detection and Validation

`lns init` and `lns status` inspect common repo signals:

- `package.json`
- `vite.config.*`
- `next.config.*`
- `nuxt.config.*`
- `vue.config.js`
- `pyproject.toml`
- `requirements.txt`
- `Gemfile`
- `.env*`

Detection is used for:

- bootstrapping `lns.json`
- choosing a default profile for `lns service add` when you omit `--profile`
- reporting drift between repo config and detectable repo signals

Detection is not used as steady-state routing truth after `lns.json` exists.

## Sync Semantics

`lns sync` is a read / validate / compile step.

It:

1. loads `lns.json`
2. validates required fields
3. blocks on unresolved services
4. blocks on canonical port conflicts
5. blocks on hostname conflicts
6. updates the global registry
7. regenerates the project and global Caddyfiles

It does not:

- mutate `lns.json`
- silently reassign ports
- silently choose a fallback framework or generic port range

If repo signals disagree with `lns.json`, sync reports drift but still compiles from the canonical config.

## Status Output

Inside a repo with `lns.json`, `lns status` shows:

- service status (`resolved` or `unresolved`)
- source (`detected`, `manual`, or `config`)
- root
- port
- profile
- explicit hostname
- drift against detectable repo signals

Outside a repo, or with `--global`, `lns status` shows the compiled global registry view.

## Caddy

`lns` writes:

- `~/.lns/registry.json`
- `~/.lns/Caddyfile`
- `~/.lns/projects/*.caddy`
- `~/.lns/settings.json`

The global Caddyfile imports project-level generated files. `lns start` and `lns reload` operate on that compiled state.

## Docker Export

Compiled projects can still be exported for Docker-based use:

```bash
lns export my-app -o Caddyfile --upstream docker
lns export my-app --docker-compose
```

For Docker upstreams, service entries can include:

- `docker`
- `container_name`

## Troubleshooting

Service unresolved after `lns init`:

- open `lns.json`
- fill in the missing `port` or `profile`
- rerun `lns sync`

Port conflict on `lns sync`:

- run `lns check <port>`
- inspect `lns status --global`
- change the canonical repo port in `lns.json`

Drift reported by `lns status` or `lns sync`:

- either update the repo so it matches `lns.json`
- or update `lns.json` and run `lns sync` again

Caddy not running:

```bash
lns doctor
lns start
```

## Development

```bash
env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

## License

MIT
