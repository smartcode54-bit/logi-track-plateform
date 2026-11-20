# LogiTrack Web (`logi-track`)

Next.js admin app ของ LogiTrack — หน้าจัดการงาน, Driver Monitor, บัญชี/วางบิล, fleet, HR และ Security Center

วิธีรันและ deploy ทุก env (local, staging VM, Firebase dev/prod) อยู่ที่ [`shared-docs/runbook-run-deploy.md`](../shared-docs/runbook-run-deploy.md)

## รันบนเครื่อง

เว็บต่อ backend คนละแบบตาม branch:

| Branch | Backend | วิธีรัน |
| --- | --- | --- |
| `mv-go` | Go API ผ่าน BFF (`app/api/*`, `proxy.ts`) | เปิด Go stack ก่อน (`make up` ใน `logitrack-api/`) แล้ว `make web` ใน `logitrack-api/` หรือ `pnpm web` ที่ root พร้อม `logitrack-web/.env.development.local` |
| `main` | Firebase dev (`logi-track-wrt-dev`) | `pnpm dev` ที่ root — copy `envs/.env.dev.web` เป็น `logitrack-web/.env.local` แล้วเปิด dev server |

จากนั้นเปิด <http://localhost:3000>

## ตรวจโค้ด

ใน `logitrack-web/`:

- `pnpm test` — Vitest
- `pnpm lint` — ESLint + เช็ค masked money
- `pnpm test:e2e` — Playwright (`tests/`)
- `pnpm build` — production build (`next build --webpack`)

## Deploy

- `main` → Firebase: dev deploy อัตโนมัติหลัง CI เขียว, prod กดรัน workflow **Deploy** ใน GitHub Actions (`.github/workflows/deploy.yml`) — รายละเอียด env ของ Hosting/Functions: [`docs/ENV_AND_DEPLOY.md`](docs/ENV_AND_DEPLOY.md)
- `mv-go` → staging VM: `logitrack-api/deploy/vm/deploy.sh` (ดู runbook)
