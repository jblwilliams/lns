# FastAPI + Vite Example

This example shows how to use lns with a FastAPI backend and Vite frontend.

## Setup

```bash
# 1. Register project
lns init my-fullstack-app

# 2. Add both services
lns add my-fullstack-app api --framework fastapi
lns add my-fullstack-app frontend --framework vite

# 3. Check assigned ports
lns status
# my-fullstack-app  api       8000  fastapi  http://my-fullstack-app-api.localhost/
# my-fullstack-app  frontend  5173  vite     http://my-fullstack-app-frontend.localhost/

# 4. Start the proxy
lns start

# 5. Start your servers
# Terminal 1:
cd backend && uvicorn main:app --reload --host 0.0.0.0 --port 8000

# Terminal 2:
cd frontend && npm run dev
```

## Vite Configuration

Update `vite.config.ts` to work with the proxy:

```typescript
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: true,  // Listen on all interfaces
    port: 5173,
    hmr: {
      host: 'my-fullstack-app-frontend.localhost',
      protocol: 'ws',
    },
    proxy: {
      '/api': {
        target: 'http://localhost:8000',
        changeOrigin: true,
      },
    },
  },
})
```

## Access

- Frontend: http://my-fullstack-app-frontend.localhost
- API: http://my-fullstack-app-api.localhost
- API Docs: http://my-fullstack-app-api.localhost/docs
