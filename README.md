# lns

`lns` gives local HTTP services stable HTTPS names without making you coordinate development ports.

Run `lns` in a repo. It discovers runnable services, leases free ports, starts or reloads Caddy, and runs the services at names such as `https://my-app.localhost`. The checked-in `lns.json` keeps names and deployment metadata stable; process-owned runtime leases keep local ports disposable.

## Installation

```bash
go install ./cmd/lns
brew install caddy # macOS
```

On Ubuntu or Debian, install Caddy with `sudo apt install caddy`.

## Quick Start

```bash
cd ~/projects/my-app
lns
```

The first run creates `lns.json` when needed. Bare `lns` runs every detected service with a runnable script or command.

Run one service or override its command for one invocation:

```bash
lns run web
lns run api -- pnpm run server
```

Services use stable public names:

```text
single-service repo:  https://<project>.localhost
multi-service repo:   https://<project>-<service>.localhost
```

Local HTTPS uses Caddy's internal CA. The proxy defaults to port `443`; `8443` is the unprivileged fallback. Run `lns setup` to change it or `lns start --no-tls` when plain HTTP is specifically required.

## Configuration

`lns.json` is the checked-in description of a repo. Development and deployment ports are deliberately separate:

```json
{
  "name": "my-app",
  "services": {
    "web": {
      "root": "web",
      "script": "dev",
      "container_port": 5173,
      "profile": "hmr",
      "status": "resolved"
    },
    "api": {
      "root": "api",
      "command": ["uv", "run", "uvicorn", "app:app", "--port", "{port}"],
      "container_port": 8000,
      "profile": "standard",
      "docker": true,
      "container_name": "api",
      "status": "resolved"
    }
  }
}
```

Required top-level fields:

- `name`
- `services`

A resolved service needs `root`, `profile`, and one way to run or route:

- `script`: a `package.json` script such as `dev`
- `command`: an argument array; `{port}` becomes the leased development port
- `port`: a fixed legacy/static host port

Optional deployment fields:

- `container_port`: stable internal port used by Docker exports
- `docker`
- `container_name`
- `hostname`

`hmr` is appropriate for browser dev servers. `standard` is appropriate for APIs and other normal HTTP servers.

For manual setup:

```bash
lns init
lns service add api api --script dev --container-port 8000 --profile standard
lns sync
```

## Runtime Behavior

`lns` chooses a free loopback port for each child process, registers a process-owned route, and removes it when the process exits. Dead owners are pruned after crashes.

Each child receives:

- `LNS_PORT`: its leased development port
- `PORT` and `<SERVICE>_PORT` for normal HTTP servers
- `VITE_PORT` for scripts that start Vite
- `HOST=127.0.0.1`
- `LNS_URL`: its public local URL
- `LNS_<SERVICE>_URL`: the URL of every service in the project
- `VITE_LNS_<SERVICE>_URL`: the same values exposed to Vite clients

For example, a `web` service in a project with an `api` service receives `LNS_API_URL` and `VITE_LNS_API_URL`.

Simple Vite, Next.js, and Nuxt scripts receive explicit loopback host and leased-port arguments, replacing fixed development flags when present. Compound scripts such as a dashboard that starts both an API and Vite receive `VITE_PORT`, leaving the API's private port alone. A service named `server` receives `SERVER_PORT`, which matches projects such as Peyra. Use `command` with `{port}` when a server needs a different custom argument or variable.

## Worktrees

Linked Git worktrees automatically receive a branch subdomain:

```text
main checkout:       https://my-app.localhost
fix-auth worktree:   https://fix-auth.my-app.localhost
```

Every worktree receives independent runtime ports. The branch prefix and leased ports never modify `lns.json` or Docker metadata.

## Workspaces and Detection

`lns init` and first-run bootstrapping inspect:

- `package.json` and `pnpm-workspace.yaml` workspaces
- `dev` plus common sibling `server`/`api` scripts
- Vite, Next.js, Nuxt, Vue, Hono, Express, Fastify, and Koa signals
- `pyproject.toml`, `requirements.txt`, and common Python server files
- `Gemfile`
- `.env*` and common framework configuration files

Packages with a `dev` script become runnable services when an HTTP profile can be identified. Ambiguous services remain unresolved until you add a profile, script, command, or fixed port.

## Commands

```bash
lns                              # bootstrap and run all services
lns up                           # run all configured services
lns run [service]                # run one service
lns run [service] -- <command>   # one-time command override
lns sync                         # validate and compile; auto-reload if running
lns status                       # repo-local status
lns status --global              # global registry
lns start                        # start Caddy only
lns stop                         # stop Caddy
lns reload                       # manually reload Caddy
lns doctor                       # diagnose local setup
lns config                       # show state paths and proxy settings
```

Use `lns run <service> --port <port>` when you intentionally need a fixed development port for one run.

## Sync and State

`lns sync` validates `lns.json`, rejects unresolved services and hostname conflicts, updates the global registry, and regenerates Caddyfiles. Fixed host ports still receive conflict checks; dynamically run services do not reserve port zero.

If Caddy is already running, sync reloads it automatically. Invalid changes do not silently pick a different checked-in port or mutate deployment metadata.

Global state lives under `~/.lns`:

- `registry.json`: compiled repo definitions
- `runtime.json`: active process leases
- `Caddyfile`: global generated configuration
- `projects/*.caddy`: project and runtime routes
- `settings.json`: HTTPS, proxy port, and Caddy admin settings

Registry and runtime mutations use a shared lock and atomic file replacement so concurrent worktrees do not overwrite one another.

## Docker and Deployment Ports

Development leases do not change Docker behavior. `lns run` uses a dynamic host port, while Docker exports use `container_port`, falling back to legacy `port` for older manifests.

```bash
lns export my-app -o Caddyfile --upstream docker
lns export my-app --docker-compose
```

Exports listen on port `80` by default, independent of the local HTTPS proxy setting. Use `--proxy-port` to choose a different deployment listener port.

This lets a Vite service use any free local port while its container continues to listen on `5173`, and lets an API lease any local port while staging and production continue to use container port `8000`.

`lns` does not rewrite Compose files or dynamically change Postgres, Redis, or other non-HTTP backing-service ports. Keep those in Compose or environment-specific infrastructure configuration.

## Troubleshooting

If a service is unresolved, open `lns.json` and add a `script`, `command`, `port`, or missing `profile`, then run `lns` again.

If port `443` cannot be bound without extra setup, run `lns setup` and select `8443`. Check the whole setup with:

```bash
lns doctor
```

## Development

```bash
env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

## License

MIT
