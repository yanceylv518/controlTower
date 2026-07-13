# Control Tower Webapp

Requires Node.js 20 or newer and pnpm.

```bash
pnpm install
pnpm dev
pnpm build
```

The development server proxies `/api` to `http://127.0.0.1:8080`. Production assets are built to `web/dist/desktop` and served by the Go Server under `/next/`.

Routes: `/` overview, `/customers`, `/channels`, `/models`, `/samples`, `/runtime`, `/usage`, `/alerts`, `/notifications`, `/instances`, and `/audits`. All authenticated pages share the global instance filter where the frozen contract supports it.
