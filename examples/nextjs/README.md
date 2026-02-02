# Next.js Example

This example shows how to use lns with a Next.js application.

## Setup

```bash
# 1. Create your Next.js app (if you haven't already)
npx create-next-app@latest my-nextjs-app
cd my-nextjs-app

# 2. Register with lns
lns init my-nextjs-app
lns add my-nextjs-app frontend --framework nextjs

# 3. Start the proxy
lns start

# 4. Start your dev server
npm run dev

# 5. Access at http://my-nextjs-app-frontend.localhost
```

## With Custom Port

If port 3000 is taken:

```bash
# lns will auto-assign next available port
lns add my-nextjs-app frontend --framework nextjs
# Output: Assigned port 3001

# Start Next.js on that port
npm run dev -- -p 3001
```

## With API Backend

```bash
# Add both frontend and API
lns add my-nextjs-app frontend --framework nextjs --port 3000
lns add my-nextjs-app api --framework fastapi --port 8000

# Access:
# - http://my-nextjs-app-frontend.localhost (Next.js)
# - http://my-nextjs-app-api.localhost (FastAPI)
```

## Docker Setup

See the `Caddyfile` and `docker-compose.yml` in this directory for a complete Docker example.

```bash
# Export standalone Caddyfile
lns export my-nextjs-app -o Caddyfile

# Start with Docker
docker compose up
```
