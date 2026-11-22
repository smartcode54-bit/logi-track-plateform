# LogiTrack API — Bruno collection

The HTTP contract of `logitrack-api` as a [Bruno](https://www.usebruno.com) collection: one request per route of
[`api/routes.txt`](../api/routes.txt) (the route table `cmd/api` generates), one folder per route group. Each request has
its method, URL, path/query params, an example body, its auth, a `docs` tab (capability, listeners, success status,
response fields) and an `assert` on the success status.

## Open it

1. Install the Bruno app, then **Open Collection** and pick this folder (`logitrack-api/bruno`).
2. Copy `.env.example` to `.env` in this folder and fill it in. `.env` is git-ignored; never commit a value.
   Locally the password of the seeded users (`*@logitrack.test`) is `SEED_DEFAULT_PASSWORD` of your
   `logitrack-api/.env`; `make seed` (in `logitrack-api`) resets them.
3. Pick an environment (top right):

   | Environment | `baseUrl` (internal routes) | `publicBaseUrl` (`/v1/auth`, `/v1/mobile`, `/evidence`, `/media`, `/healthz`) |
   |---|---|---|
   | `local` | `http://127.0.0.1:8080` | `http://127.0.0.1:8081` (`docker compose` in `deploy/`) |
   | `staging` | `https://logi.showkhun.co/api/go` (through the web BFF) | `https://logi-api.showkhun.co` |

4. Run **auth → Login**. Its post-response script stores `accessToken` and `refreshToken` as runtime variables;
   every request with auth *inherit* sends `Authorization: Bearer {{accessToken}}`. **auth → Refresh tokens**
   rotates them. A user with several tenants switches with **auth → Switch tenant context**.

Driver-app routes (`mobile/*`, **auth → Issue SSE ticket**) need a driver session: set `loginPlatform` to
`android` in the environment (or `--env-var loginPlatform=android`) and sign in as a driver.

## Auth kinds

- **Bearer** (most routes): from Login, as above.
- **API key** (`GET /v1/roles`, `POST /v1/app-releases`, `POST /v1/app-releases/presign`): set the request's auth to
  *No Auth* and enable its `X-Api-Key: {{apiKey}}` header (`LOGITRACK_API_KEY`). Sending both is refused.
- **Cloud Functions shim** (`GET /v1/mobile/me`, `POST /v1/mobile/{tasks,trips,standby}/{id}/…`, broadcast read):
  `X-Api-Key: {{shimApiKey}}` plus `Authorization: Bearer {{firebaseIdToken}}`.
- **Anonymous**: login, refresh, password forgot/reset, Google nonce, health, the version gate, the public forms.

## Staging

Staging holds a copy of production data. Use it for **reads only** — never run a folder there, and never send a
write. On staging the internal routes go through the BFF, which takes the token from the `lt_at` cookie (the collection
pre-request script sets it when `viaBff` is `true`) and refuses cross-origin writes. `/healthz`, `/readyz` and
`/.well-known/*` of the internal listener are not reachable there.

## CLI

```bash
npx @usebruno/cli@4.3.0 run auth/Login.bru "me/Get my profile.bru" trucks --env local
```

Run single requests or read-only folders. A whole-collection run sends every write and ends the session
(`auth/Logout all sessions`).

## Keeping it current

When a route changes, `make gen` updates `api/routes.txt`; add or edit the matching `.bru` file here in the same PR
(the Bruno app writes the format). Success is `{"data": …}` (`nextCursor` on keyset lists); errors are
`{"error": {"code", "message", "details", "requestId"}}`.

> The repository `.gitignore` ignores `*.md`: add this README with `git add -f`.
