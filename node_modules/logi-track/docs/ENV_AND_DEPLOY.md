# Environment and production deploy (Firebase)

How to prepare and use `.env.prod` (production env) across the full Firebase stack: **Hosting** (Next.js static app) and **Cloud Functions**.

## 1. Template: `.env.prod.example`

In the project root (`logitrack-web/`):

- **`.env.prod.example`** lists every variable used in production.
- Copy it and fill in real values; never commit those files.

```bash
cp .env.prod.example .env.production
# Edit .env.production with real values (keep it out of git)
```

Use the same set of values in CI as environment variables or secrets (e.g. GitHub Actions secrets, then export them before `npm run build`).

---

## 2. Web app (Firebase Hosting)

The app is a **Next.js static export**. All `NEXT_PUBLIC_*` variables are baked in at **build time**.

### Required variables (from `.env.prod.example`)

| Variable | Where to get it |
|----------|------------------|
| `NEXT_PUBLIC_FIREBASE_API_KEY` | Firebase Console → Project settings → General → Your apps |
| `NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN` | `{projectId}.firebaseapp.com` |
| `NEXT_PUBLIC_FIREBASE_PROJECT_ID` | Firebase / Google Cloud project ID |
| `NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET` | `{projectId}.appspot.com` |
| `NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID` | Project settings → General |
| `NEXT_PUBLIC_FIREBASE_APP_ID` | Project settings → General |
| `NEXT_PUBLIC_FIREBASE_MEASUREMENT_ID` | Optional; Analytics |

### How to run a production build

**Option A – Local build with a file**

- Copy `.env.prod.example` → `.env.production`.
- Fill in values. Next.js loads `.env.production` when `NODE_ENV=production` (e.g. during `next build`).
- From `logitrack-web/`:

  ```bash
  npm run build
  ```

**Option B – CI (e.g. GitHub Actions)**

- Store the same keys as repo/org **secrets**.
- Before `npm run build`, export them (e.g. `NEXT_PUBLIC_FIREBASE_API_KEY`, etc.).
- Do **not** rely on a committed `.env.production` in CI; use secrets and env.

**Option C – Use a custom file name (e.g. `.env.prod`)**

- Next.js does not load `.env.prod` by default. To use it:
  - Either copy `.env.prod` to `.env.production` before building, or
  - Use a wrapper that loads `.env.prod` and then runs the build, e.g.:
    ```bash
    set -a && source .env.prod && set +a && npm run build
    ```
    (On Windows PowerShell, set each variable from your secret store, then run `npm run build`.)

### Deploy

**Production deploy (แนะนำ):** รันจาก **root ของ repo** (`logitrack-platform/`) เพื่อให้คัดลอก env ถูกต้อง:

```bash
cd /path/to/logitrack-platform
npm run deploy:prod
```

(เปลี่ยนเป็น path จริงของโปรเจกต์ — โฟลเดอร์ root ที่มี `envs/` และ `logitrack-web/`)

คำสั่งนี้จะ copy `envs/.env.prod.web` → `logitrack-web/.env.production` แล้ว build และ deploy

**หมายเหตุ:** ห้ามรัน `npm run deploy:prod` จากภายในโฟลเดอร์ `logitrack-web/` โดยตรง — จะทำให้ path ของ env ผิดพลาด ต้องรันจาก root (`logitrack-platform/`)

---

## 3. Cloud Functions

Functions **do not** read `.env.prod` or `.env.production` at runtime. They run on Google Cloud and get config from:

- **Environment variables** set in Cloud Console, or  
- **Firebase params** (e.g. `defineString` / `defineSecret`) with values from local `.env` at deploy time (see [Firebase: Configure your environment](https://firebase.google.com/docs/functions/config-env)).

### Variables used by this project

| Variable | Used in | How to set in production |
|----------|---------|---------------------------|
| `GOOGLE_MAPS_API_KEY` | `functions` (e.g. distances) | Cloud Console or Firebase params |

### Option A – Google Cloud Console

1. Open [Google Cloud Console](https://console.cloud.google.com) → your project.
2. **Functions** → select the function (e.g. from codebase `logi-truck`) → **Configuration** (or **Edit**).
3. **Runtime environment variables** → Add `GOOGLE_MAPS_API_KEY` (and any others you need).

### Option B – Firebase params (recommended for consistency)

1. In `functions/`, create a `.env` (or use existing) with:
   ```bash
   GOOGLE_MAPS_API_KEY=your_key_here
   ```
2. Use Firebase params in code and deploy; the CLI can load from `.env` and store per-project (e.g. `.env.{projectId}`). See [Firebase config-env](https://firebase.google.com/docs/functions/config-env).

After setting the variable, redeploy functions:

```bash
firebase deploy --only functions
```

---

## 4. Checklist before production deploy

- [ ] **Web:** Production Firebase config (all `NEXT_PUBLIC_*`) set when running `npm run build` (via `.env.production` or CI secrets).
- [ ] **Functions:** `GOOGLE_MAPS_API_KEY` set in Cloud Console or via Firebase params.
- [ ] **Secrets:** No real keys in repo; `.env.production`, `.env.prod`, and `.env` in `functions/` are gitignored (only example files like `.env.prod.example` are committed).
- [ ] **Firebase project:** Deploy target in `.firebaserc` is the correct production project.
- [ ] **Auth – Authorized domains:** After the first deploy, add your Hosting domain to Firebase Auth so sign-in works. If you see `auth/unauthorized-domain` in the browser console, add the domain (e.g. `logitrack-prod.web.app`) in **Firebase Console → Authentication → Settings → Authorized domains**.

---

## 5. Quick reference

| Part of stack | Env source for prod | When it’s read |
|---------------|---------------------|----------------|
| Next.js (Hosting) | `.env.production` or CI env | Build time (`next build`) |
| Cloud Functions | Cloud Console or Firebase params | Runtime in Google Cloud |

Using `.env.prod` for the whole infrastructure means: **use it (or the same values) as the source for the web app’s build-time env**, and **configure Functions separately** in Cloud Console or Firebase params as above.
