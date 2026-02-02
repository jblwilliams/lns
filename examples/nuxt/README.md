# Nuxt Example

This example shows how to use lns with a Nuxt 3 application.

## Setup

```bash
# 1. Create your Nuxt app (if you haven't already)
npx nuxi@latest init my-nuxt-app
cd my-nuxt-app

# 2. Register with lns
lns init my-nuxt-app
lns add my-nuxt-app frontend --framework nuxt

# 3. Start the proxy
lns start

# 4. Start your dev server
npm run dev

# 5. Access at http://my-nuxt-app-frontend.localhost
```

## With Custom Port

```bash
# Check what port was assigned
lns status

# Start Nuxt on that port
npm run dev -- --port 3100
```

## Nuxt Configuration

For best results, update your `nuxt.config.ts`:

```typescript
export default defineNuxtConfig({
  devServer: {
    host: '0.0.0.0',  // Listen on all interfaces
    port: 3100,       // Match your registered port
  },
})
```
