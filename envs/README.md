# Environment files (envs)

โฟลเดอร์นี้เก็บเทมเพลตและ (ถ้ามี) ค่าจริงของ environment สำหรับ **dev** และ **production** ของแต่ละแอป **ห้าม commit ไฟล์ที่มีค่าจริง**

## โครงสร้าง

| ไฟล์ | ใช้กับ | คำอธิบาย |
|------|--------|----------|
| `.env.dev.web.example` | Next.js | เทมเพลต dev web (logi-track-wrt-dev) |
| `.env.dev.web` | Next.js | ค่าจริง dev web (gitignored) |
| `.env.dev.mobile.example` | Flutter | เทมเพลต dev mobile |
| `.env.dev.mobile` | Flutter | ค่าจริง dev mobile (gitignored) |
| `.env.prod.web.example` | Next.js | เทมเพลต prod web (logitrack-prod) |
| `.env.prod.web` | Next.js | ค่าจริง prod web (gitignored) |
| `.env.prod.mobile.example` | Flutter | เทมเพลต prod mobile |
| `.env.prod.mobile` | Flutter | ค่าจริง prod mobile (gitignored) |

ไฟล์ `.env.*` ที่ไม่มี `.example` ถูก ignore ใน `.gitignore` — ใส่ค่าจริงในไฟล์นั้นแล้วอย่า commit

## วิธีใช้

### 1. เตรียมค่าสำหรับ production (ครั้งแรก)

```bash
# จาก root ของ repo
cp envs/.env.prod.web.example   envs/.env.prod.web
cp envs/.env.prod.mobile.example envs/.env.prod.mobile
# แก้ไข envs/.env.prod.web และ envs/.env.prod.mobile ให้ใส่ค่าจริง (Firebase, API keys ฯลฯ)
```

### 2. Deploy — Web (Next.js): Dev vs Prod

| สิ่งที่ทำ | คำสั่ง (จาก root) |
|-----------|--------------------|
| **Deploy ไป Dev** (Firebase: logi-track-wrt-dev) | `pnpm run deploy:dev` |
| **Deploy ไป Prod** (Firebase: logitrack-prod) | `pnpm run deploy:prod` |

ครั้งแรกต้องมีไฟล์ env ก่อน:
- Dev: `cp envs/.env.dev.web.example envs/.env.dev.web` แล้วแก้ค่าให้ตรงกับ Firebase project **logi-track-wrt-dev**
- Prod: ใช้ `envs/.env.prod.web` (มีอยู่แล้ว / จาก secret ใน CI)

Next.js อ่าน `.env.production` ตอน build — สคริปต์ `deploy:dev` / `deploy:prod` จะ copy จาก `envs/.env.dev.web` หรือ `envs/.env.prod.web` ให้ก่อน build แล้วค่อย deploy

### 3. Build production — Mobile (Flutter)

Flutter โหลด `.env` ในโฟลเดอร์แอป (`logitrack-mobile/.env`) ดังนั้นให้ copy ก่อน build:

```bash
# จาก root
cp envs/.env.prod.mobile logitrack-mobile/.env
cd logitrack-mobile && flutter build apk
# หรือ flutter build ios
```

ใน CI: copy `envs/.env.prod.mobile` → `logitrack-mobile/.env` (จาก secret/store) แล้วรัน `flutter build ...`

### 4. Deploy Firebase Storage / Firestore

- **Dev:** `cd logitrack-web && firebase use default && firebase deploy --only storage` (หรือ firestore)
- **Prod:** `firebase use prod && firebase deploy --only storage` (หรือ firestore)

เมื่อ deploy เสร็จแล้ว กลับไปใช้ default ได้: `firebase use default`

### 5. GitHub Actions

| Workflow | Trigger | Project | Secret สำหรับ env |
|----------|---------|---------|---------------------|
| **Deploy Development** | Manual (workflow_dispatch) | logi-track-wrt-dev | `ENV_DEV_WEB` |
| **Deploy Production** | Manual (workflow_dispatch) | logitrack-prod | `ENV_PROD_WEB` |

ทั้งสองใช้ `FIREBASE_TOKEN` สำหรับ deploy — ตั้งใน repo Secrets แล้วรัน workflow จาก Actions tab

## โครงสร้าง monorepo (สรุป)

```
logitrack-platform/
├── envs/
│   ├── .env.dev.web.example     # เทมเพลต Next.js Dev
│   ├── .env.dev.web             # ค่าจริง dev web (gitignored)
│   ├── .env.dev.mobile.example  # เทมเพลต Flutter Dev
│   ├── .env.dev.mobile          # ค่าจริง dev mobile (gitignored)
│   ├── .env.prod.web.example    # เทมเพลต Next.js Prod
│   ├── .env.prod.web            # ค่าจริง prod web (gitignored)
│   ├── .env.prod.mobile.example # เทมเพลต Flutter Prod
│   └── .env.prod.mobile         # ค่าจริง prod mobile (gitignored)
├── logitrack-web/
└── logitrack-mobile/
```
