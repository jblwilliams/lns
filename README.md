# lns

`lns` gives local development services stable names while leaving their ports disposable.

```bash
cd ~/projects/my-app
lns
```

There is no required project configuration and no repository write on first run. LNS inspects existing development scripts, workspaces, environment examples, and the canonical local Compose file in memory. Developers who do not run LNS are unaffected.

Typical routes are plain HTTP names with no visible port. The proxy binds only to loopback, and child processes receive `HOST=127.0.0.1`:

```text
http://my-app.localhost
http://my-app-web.localhost
http://my-app-api.localhost
```

## Install

```bash
go install ./cmd/lns
brew install caddy # macOS
```

On Linux, install Caddy and allow it to bind port 80. For example:

```bash
sudo setcap 'cap_net_bind_service=+ep' "$(command -v caddy)"
```

On macOS or Linux without that capability, the first interactive proxy start asks for `sudo` and leaves Caddy running in the background. Non-interactive runs fail with an actionable message instead of hanging on a password prompt.

Run `lns doctor` to check the local setup.

## What bare `lns` does

Bare `lns`:

1. Build the same read-only plan shown by `lns plan`.
2. Select the main browser app and the backend it links to, rather than launching every workspace package.
3. Reuse or start required Postgres, Redis, and validated private worker services from the repository's normal local Compose project.
4. Allocate every application listener before starting any child process.
5. Start or reload Caddy and route the stable local names to those listeners.
6. Clean up processes and routes, then stop only the Compose services that this run started.

Inventory services that are not part of the default app remain available explicitly:

```bash
lns run marketing-site
lns run api -- pnpm run custom-api
```

If LNS cannot identify one safe default, it starts nothing and `lns plan` explains how to choose a service.

## Dynamic ports

Each child receives the variables its existing development setup already uses, plus LNS's generic variables:

- `LNS_PORT`, `LNS_URL`, and `HOST=127.0.0.1`
- `PORT` for normal servers
- `VITE_PORT` for Vite
- inferred variables such as `SERVER_PORT`, `CLIENT_PORT`, `VITE_API_URL`, and `CORS_ORIGIN`
- `LNS_<SERVICE>_URL` and `VITE_LNS_<SERVICE>_URL` for every discovered public service

Compound scripts are one lifecycle with multiple listeners. For example, a `concurrently` script that starts an API and Vite receives different dynamic `PORT` and `VITE_PORT` values, while Vite's local proxy target points to the private API listener. Auxiliary ports explicitly exposed by a retained root wrapper are dynamic too.

All listener allocations happen before the first development command is prepared, so cross-service links use the final values.

## Docker dependencies

LNS reads only the canonical local file (`compose.yml`, `compose.yaml`, `docker-compose.yml`, or `docker-compose.yaml`). It does not inspect or alter staging and production variants.

For recognized Postgres and Redis dependencies, LNS:

- asks Docker for loopback-only ephemeral host ports;
- keeps container ports such as `5432` and `6379` unchanged;
- writes its temporary Compose override under `~/.lns/dependencies`, never in the repository;
- reuses already-running local dependencies when they expose a usable host port;
- starts only the validated dependency closure and required private workers, never the selected foreground application;
- rewrites only local database/Redis connection values in child-process memory;
- preserves credentials, paths, queries, and the normal project's named development volumes;
- stops only services it started and never deletes their containers or volumes.

External database hosts, TLS Redis URLs, test database URLs, host networking, published worker ports, global Compose resources, and unsafe dependency graphs are not silently rewritten or started.

LNS deliberately does not invent a project-specific database bootstrap. Existing development volumes continue to work. A fresh database still needs the repository's normal migrations, role provisioning, tenant choices, or seed commands.

## Opt-in by design

LNS does not require a checked-in file, install a package hook, replace a project's normal scripts, or change Docker metadata. A developer can try it in an existing checkout and stop using it without leaving repository changes behind.

An `lns.json` file is still accepted as a strict local-run override for unusual repositories, but it is not the normal setup path:

```json
{
  "name": "my-app",
  "services": {
    "web": { "root": "apps/web", "script": "dev" }
  }
}
```

It contains local command intent only. Docker, staging, production, and fixed-port fields are deliberately not part of this file.

## Worktrees

Linked Git worktrees get an additional branch label and independent application ports:

```text
main checkout:       http://my-app.localhost
fix-auth worktree:   http://fix-auth.my-app.localhost
```

If multiple worktrees intentionally share one explicitly named Compose project, LNS allows only one of them to own that dependency stack at a time. The second run fails with the owning PID instead of recreating or stopping the first worktree's database.

## Useful commands

```bash
lns                         # plan and run the default local app graph
lns plan                    # explain the plan without changing state
lns plan --json             # machine-readable plan only
lns run <service>           # explicitly run one inventory service
lns run <service> -- <cmd>  # one-run command override
lns doctor                  # check Caddy and Docker requirements
lns config                  # show global state paths
lns stop                    # stop the shared Caddy proxy
```

`lns run <service> --port <port>` is available when a fixed application port is intentionally required for one run.

## Global state

LNS keeps machine-local state under `~/.lns`:

- `runtime.json`: process-owned routes
- `Caddyfile` and `projects/00-runtime.caddy`: generated process-owned proxy configuration
- `dependencies/`: short-lived Compose overrides and ownership records

The route contract is always `http://*.localhost` on port 80. Caddy control is fixed to `127.0.0.1:20190`.

## Development

```bash
env GOCACHE=/tmp/lns-go-build-cache go test ./...
env GOCACHE=/tmp/lns-go-build-cache go vet ./...
```

## License

MIT
