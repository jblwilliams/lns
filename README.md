# lns

**Local Name System** – development reverse proxy manager using Caddy. Avoid port conflicts across projects.

## The Problem

You're working on multiple projects:
- Project A: Next.js on port 3000
- Project B: Nuxt on port 3000
- Project C: FastAPI on port 8000, Vite on port 5173

Only one can bind to each port at a time. You end up with:
- Constantly changing ports
- Forgetting which project uses which port
- Broken bookmarks and muscle memory
- Docker services clashing with local dev servers

## The Solution

**lns** manages a single Caddy reverse proxy that routes by hostname:

```
http://project-a.localhost:8888  →  localhost:3000
http://project-b.localhost:8888  →  localhost:3001  (auto-assigned)
http://project-c-api.localhost:8888  →  localhost:8000
http://project-c-frontend.localhost:8888  →  localhost:5173
```

One proxy entry point (default `:8888`; optional `:80`). No `/etc/hosts` editing (`.localhost` auto-resolves). Global registry prevents conflicts.

## Installation

### Go Install

```bash
go install ./cmd/lns
```

### Build from Source

```bash
git clone <your-repo-url>
cd lns
make build
sudo make install
```

### Prerequisites

1. **Caddy** must be installed:
   ```bash
   # macOS
   brew install caddy

   # Ubuntu/Debian
   sudo apt install caddy

   # Other: https://caddyserver.com/docs/install
   ```

2. **Optional: use port 80**:
   ```bash
   # Default proxy port is 8888 (no elevated privileges needed).
   # Note: binding to :80 is a "privileged port" on most systems. If Caddy is running on the host,
   # you'll typically need sudo/admin privileges (common on macOS), or to run Caddy via a privileged
   # service manager. On Linux you can usually avoid running as root with setcap:
   # If you choose port 80 on Linux, allow Caddy to bind without root:
   sudo setcap 'cap_net_bind_service=+ep' $(which caddy)
   ```

## Quick Start

```bash
# 1. Initialize your project
cd ~/projects/my-app
lns init my-app

# 2. Add services
lns add my-app frontend --framework nextjs
lns add my-app api --framework fastapi --port 8000

# 3. Start the global proxy
lns start  # runs interactive setup on first run

# 4. Start your dev servers and access via (default):
#    http://my-app-frontend.localhost:8888
#    http://my-app-api.localhost:8888
# (If you configure port 80, you can omit :8888.)
```

## Commands

### Project Management

```bash
# Initialize a new project
lns init <project-name> [--path /path/to/project] [--docker]

# Add a service to a project
lns add <project> <service> --framework <type> [--port <port>]

# Remove a service or project
lns remove <project> [service]

# List all projects and services
lns status
```

### Port Management

```bash
# Check if a port is available
lns check 3000

# Get suggested port for a framework
lns suggest nextjs

# View all port assignments
lns ports
```

### Caddy Control

```bash
# Interactive setup (port + hostnames + admin address)
lns setup

# Start the global Caddy proxy
lns start

# Reload after changes
lns reload

# Stop Caddy
lns stop

# Check system requirements
lns doctor
```

### Export & Configuration

```bash
# Export standalone Caddyfile for a project (default upstream: host)
lns export my-app -o Caddyfile

# Export standalone Caddyfile using docker upstreams (container DNS)
lns export my-app -o Caddyfile --upstream docker

# Export docker-compose snippet for a project
lns export my-app --docker-compose

# Show configuration paths
lns config
```

## Supported Frameworks

| Framework | Default Port | Port Range |
|-----------|-------------|------------|
| Next.js (`nextjs`) | 3000 | 3000-3099 |
| Nuxt (`nuxt`) | 3000 | 3100-3199 |
| Vite (`vite`) | 5173 | 5173-5272 |
| Vue CLI (`vue-cli`) | 8080 | 8080-8099 |
| Create React App (`react-cra`) | 3000 | 3200-3299 |
| FastAPI (`fastapi`) | 8000 | 8000-8079 |
| Django (`django`) | 8000 | 8100-8179 |
| Express (`express`) | 3000 | 4000-4099 |
| Rails (`rails`) | 3000 | 4100-4199 |
| Flask (`flask`) | 5000 | 5000-5099 |
| Generic (`generic`) | 8080 | 9000-9099 |

When you add a service without specifying a port, lns auto-assigns the next available port in the framework's range.

## Docker Support

### With Global Proxy

For Docker services that should be accessible via the global proxy, publish the container port to your host and register that host port with lns (the global proxy always targets `localhost:<port>`):

```bash
# Register a Docker service (make sure its port is published to localhost)
lns add my-app db --framework generic --port 5432 --docker --container postgres_container
```

### Caddy in Docker (Optional)

If you want Caddy to run in the same docker-compose network (so it can proxy to container DNS names), export a Caddyfile with docker upstreams and add a Caddy service:

```bash
# 1) Export a Caddyfile that proxies to containers by name
lns export my-app -o Caddyfile --upstream docker

# 2) Print a docker-compose snippet for Caddy
lns export my-app --docker-compose
```

Add the snippet to your docker-compose.yml and ensure it mounts `./Caddyfile`:

```yaml
services:
  caddy:
    image: caddy:2-alpine
    ports:
      - "8888:8888"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
    networks:
      - app_network
```

## How It Works

### Global Registry

All port assignments are stored in `~/.lns/registry.json`:

```json
{
  "version": "1.0",
  "projects": {
    "my-app": {
      "name": "my-app",
      "services": [
        {"name": "frontend", "port": 3000, "framework": "nextjs"},
        {"name": "api", "port": 8000, "framework": "fastapi"}
      ]
    }
  },
  "port_assignments": {
    "3000": "my-app:frontend",
    "8000": "my-app:api"
  }
}
```

### Caddyfile Generation

Each project gets its own config in `~/.lns/projects/`:

```caddyfile
# ~/.lns/projects/my-app.caddy
http://my-app-frontend.localhost:8888 {
    reverse_proxy localhost:3000 {
        header_up Host {host}
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}

http://my-app-api.localhost:8888 {
    reverse_proxy localhost:8000
}
```

The global Caddyfile imports all project configs:

```caddyfile
# ~/.lns/Caddyfile
{
    auto_https off
    admin 127.0.0.1:20190
    http_port 8888
}

import ~/.lns/projects/*.caddy
```

## Why .localhost?

The `.localhost` TLD is [reserved by RFC 6761](https://www.rfc-editor.org/rfc/rfc6761) and automatically resolves to `127.0.0.1`. This means:

- No `/etc/hosts` editing required
- Works out of the box on all modern systems
- Each subdomain is a separate "site" (useful for cookies, localStorage)
- No conflicts with real domains

## Troubleshooting

### Port 80 permission denied (Linux)

```bash
sudo setcap 'cap_net_bind_service=+ep' $(which caddy)
```

### Caddy not found

Ensure Caddy is installed and in your PATH:
```bash
which caddy
caddy version
```

### Service not accessible

1. Check your dev server is running and bound to the correct port
2. Ensure the port matches what's registered: `lns status`
3. Reload Caddy: `lns reload`
4. Check Caddy logs: `journalctl -u caddy` or `caddy run --config ~/.lns/Caddyfile`

### HMR/WebSocket not working

For Vite, configure `vite.config.ts`:

```typescript
export default defineConfig({
  server: {
    host: true,
    hmr: {
      host: 'your-app-frontend.localhost',
      protocol: 'ws',
    },
  },
})
```

## Configuration Files

| File | Purpose |
|------|---------|
| `~/.lns/registry.json` | Global port registry |
| `~/.lns/settings.json` | Global settings (proxy port, admin address) |
| `~/.lns/Caddyfile` | Main Caddy config (imports all projects) |
| `~/.lns/projects/*.caddy` | Per-project Caddy configs |

## Development

```bash
# Build
make build

# Run tests
make test

# Build for all platforms
make build-all

# Install locally
make install
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License - see [LICENSE](LICENSE) for details.
