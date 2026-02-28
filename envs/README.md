# Production environment (envs)

โฟลเดอร์นี้เก็บเทมเพลตและ (ถ้ามี) ค่าจริงของ environment สำหรับ production ของแต่ละแอป **ห้าม commit ไฟล์ที่มีค่าจริง**

## โครงสร้าง

| ไฟล์ | ใช้กับ | คำอธิบาย |
|------|--------|----------|
| `.env.prod.web.example` | **Next.js** (logitrack-web) | เทมเพลตสำหรับ production web |
| `.env.prod.mobile.example` | **Flutter** (logitrack-mobile) | เทมเพลตสำหรับ production mobile |

ไฟล์ที่มีค่าจริง (`envs/.env.prod.web`, `envs/.env.prod.mobile`) ถูก ignore ใน `.gitignore` ของ repo

## วิธีใช้

### 1. เตรียมค่าสำหรับ production (ครั้งแรก)

```bash
# จาก root ของ repo
cp envs/.env.prod.web.example   envs/.env.prod.web
cp envs/.env.prod.mobile.example envs/.env.prod.mobile
# แก้ไข envs/.env.prod.web และ envs/.env.prod.mobile ให้ใส่ค่าจริง (Firebase, API keys ฯลฯ)
```

### 2. Build production — Web (Next.js)

Next.js อ่าน `.env.production` ตอน build ดังนั้นให้ copy จาก `envs/.env.prod.web` ก่อน build:

```bash
# จาก root
cp envs/.env.prod.web logitrack-web/.env.production
cd logitrack-web && npm run build
```

หรือใน CI: ตั้งค่า environment variables จาก secrets แล้วรัน `npm run build` ใน `logitrack-web` (ไม่ต้องมีไฟล์ .env ถ้า inject ผ่าน CI ได้)

### 3. Build production — Mobile (Flutter)

Flutter โหลด `.env` ในโฟลเดอร์แอป (`logitrack-mobile/.env`) ดังนั้นให้ copy ก่อน build:

```bash
# จาก root
cp envs/.env.prod.mobile logitrack-mobile/.env
cd logitrack-mobile && flutter build apk
# หรือ flutter build ios
```

ใน CI: copy `envs/.env.prod.mobile` → `logitrack-mobile/.env` (จาก secret/store) แล้วรัน `flutter build ...`

## โครงสร้าง monorepo (สรุป)

```
logitrack-platform/
├── envs/
│   ├── .env.prod.web.example   # เทมเพลต Next.js Production
│   ├── .env.prod.mobile.example # เทมเพลต Flutter Production
│   ├── .env.prod.web            # ค่าจริง (gitignored)
│   └── .env.prod.mobile         # ค่าจริง (gitignored)
├── logitrack-web/               # Next.js App
└── logitrack-mobile/            # Flutter App
```
