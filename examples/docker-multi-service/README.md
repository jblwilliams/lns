# Docker Multi-Service Example

This example shows how to use lns with a full Docker Compose stack containing multiple services.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Docker Network                          │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐ │
│  │   Next.js   │  │   FastAPI   │  │      Postgres       │ │
│  │  Port 3000  │  │  Port 8000  │  │     Port 5432       │ │
│  └──────┬──────┘  └──────┬──────┘  └─────────────────────┘ │
│         │                │                                  │
│         └────────┬───────┘                                  │
│                  │                                          │
│           ┌──────┴──────┐                                   │
│           │    Caddy    │                                   │
│           │   Port 80   │                                   │
│           └──────┬──────┘                                   │
└──────────────────┼──────────────────────────────────────────┘
                   │
                   ▼
    http://app.localhost  →  nextjs:3000
    http://api.localhost  →  fastapi:8000
```

## Setup

```bash
# 1. Clone this example
cp -r examples/docker-multi-service my-project
cd my-project

# 2. Start the stack
docker compose up

# 3. Access services:
#    http://app.localhost       - Next.js frontend
#    http://api.localhost       - FastAPI backend
#    http://api.localhost/docs  - Swagger UI
```

## Using with Global Proxy

If you prefer to use the global lns instead of per-project Caddy:

```bash
# Register the Docker services
lns init my-docker-app --docker
lns add my-docker-app frontend --framework nextjs --docker --container my-project-nextjs-1
lns add my-docker-app api --framework fastapi --docker --container my-project-fastapi-1

# Remove Caddy from docker-compose.yml and expose ports
# Then start global proxy
lns start
```

## Files

- `docker-compose.yml` - Full stack configuration
- `Caddyfile` - Caddy reverse proxy configuration
