# LogiTrack — เอกสารสรุปสถาปัตยกรรมระบบและฟีเจอร์

> อัปเดตล่าสุด: 2026-05-21 | เวอร์ชัน Web: V2.4.0-web  
> เอกสารนี้เป็นภาพรวม **อ่านง่าย** สำหรับทีมและ AI ใหม่ — รายละเอียดและ Change Log ฉบับเต็มอยู่ใน `.vibe-rules.md`

---

## 1. ภาพรวมระบบ (System Overview)

**LogiTrack** คือ SaaS Fleet Management Platform สำหรับบริหารจัดการรถบรรทุก คนขับ ผู้รับเหมาช่วง (subcontractor) และงานขนส่ง (task) ครบวงจร

```
logitrack-platform/          ← Monorepo root (git)
├── logitrack-web/           ← Next.js Admin Dashboard (Web)
├── logitrack-mobile/        ← Flutter Driver App (iOS/Android)
└── shared-docs/             ← Single Source of Truth (SSOT)
    ├── schemas/             ← Zod schemas (shared ระหว่าง Web & Mobile)
    └── .vibe-rules.md       ← กฎโปรเจกต์ + Change Log ฉบับเต็ม
```

### แพลตฟอร์มหลัก

| แพลตฟอร์ม | กลุ่มผู้ใช้ | Deploy | เวอร์ชันปัจจุบัน |
|-----------|------------|--------|----------------|
| **Web Admin** (Next.js) | Admin, Customer (scoped), Partner | Firebase Hosting (GitHub Actions) | V2.4.0-web |
| **Mobile App** (Flutter) | คนขับ (Driver) | APK จาก CI | prod build ล่าสุด |
| **Cloud Functions** (Firebase/Node v2) | Backend shared | Firebase Functions (asia-southeast1) | — |

---

## 2. Tech Stack

### Web (logitrack-web)

| Layer | Technology | หมายเหตุ |
|-------|------------|---------|
| Framework | **Next.js 14** (App Router) | `app/app/` = protected routes (เดิม `app/admin/`) |
| Language | **TypeScript** strict | `validate/` schemas, shared-docs |
| UI | **React** + **Tailwind CSS** | dark mode, utility-first |
| Components | **shadcn/ui** (Radix primitives) | `@/components/ui/` |
| Icons | **Lucide React** | tree-shakeable imports |
| Forms | **react-hook-form** + **Zod** | `@hookform/resolvers` |
| i18n | Custom context | `context/locales/{en,th}/*.ts` — `t("key")` |
| Auth | **Firebase Auth** | JWT custom claims (`admin`, `customerScopeId`, `partnerScopeId`) |
| Database | **Firestore SDK** | client: read/subscribe; writes ผ่าน callable เมื่อต้องการ validate |
| Server logic | **Firebase Callables** | `httpsCallable(functions, "name")` → `functions/src/*.ts` |
| Dates | **date-fns** | `format(date, "PPP", { locale: th })` |
| Validation | **Zod** | SSOT ใน `shared-docs/schemas/` |
| HTTP client | native **fetch** | ไม่ใช้ axios |

### Mobile (logitrack-mobile)

| Layer | Technology | หมายเหตุ |
|-------|------------|---------|
| Framework | **Flutter** (Dart) | `lib/features/` — feature-based |
| Architecture | **Clean Architecture** | Data → Domain → Presentation |
| State | **BLoC** (ใหม่) / **Provider** (legacy/DI) | BLoC บังคับสำหรับ feature ใหม่ |
| Backend | **Firebase** (Auth, Firestore, FCM, Storage) | |
| i18n | **easy_localization** | `assets/translations/{en,th}.json` |
| Version compare | **pub_semver** | Semantic Versioning สำหรับ force-update check |

### Backend / Shared

| Layer | Technology | หมายเหตุ |
|-------|------------|---------|
| Cloud Functions | **Firebase Functions Node v2** | region `asia-southeast1` |
| Auth server | **Firebase Admin** | custom claims, token revoke |
| Database server | **Firestore Admin** | ไม่มี security rules, ใช้ใน callables |
| Schemas (SSOT) | **Zod** ใน shared-docs | Web import ตรง; Mobile ซิงค์เป็น Dart model |

---

## 3. โครงสร้างไฟล์สำคัญ (Key File Structure)

### Web Admin

```
logitrack-web/
├── app/
│   ├── (auth)/              # Login, forgot-password, waitlist
│   ├── app/                 # Protected routes (ทุก role)
│   │   ├── dashboard/
│   │   ├── driver-monitor/
│   │   ├── first-mile/
│   │   ├── line-haul/
│   │   ├── trucks/
│   │   ├── drivers/
│   │   ├── accounting/
│   │   │   ├── fuel/
│   │   │   ├── other/
│   │   │   ├── income/
│   │   │   ├── rate-card/
│   │   │   └── audit/
│   │   ├── maintenance/
│   │   ├── security-center/
│   │   │   ├── users/
│   │   │   ├── roles/
│   │   │   └── mobile-clients/
│   │   ├── standby-records/
│   │   └── utilities/
│   └── join-network/        # Public pages
├── components/              # Shared React components
│   └── ui/                  # shadcn/ui primitives
├── context/
│   ├── auth.tsx             # Firebase Auth context
│   ├── language.tsx         # i18n context
│   └── locales/{en,th}/     # Translation files per namespace
├── features/
│   ├── drivers/             # Driver Monitor components
│   ├── tasks/               # Task dialogs, services
│   ├── maintenance/         # Maintenance components
│   └── customers/           # Customer API/components
├── functions/src/           # Cloud Functions
│   ├── index.ts             # Export all functions
│   ├── users.ts             # User management
│   ├── auth.ts              # Auth hooks (forceLogout)
│   ├── distances.ts         # Hub-SOC Matrix
│   ├── tripBillingOnDelivered.ts  # Billing compute
│   ├── backfillCustomerLinks.ts   # Backfill utility
│   └── core/
│       ├── billingCompute.ts
│       └── distances.ts
├── lib/
│   ├── collections.ts       # COLLECTIONS constants (SSOT)
│   ├── capabilities.ts      # RBAC capabilities
│   ├── roles.ts             # Role → capability mapping
│   ├── permissions.ts       # canAccessRoute, getDefaultRouteForRole
│   ├── billingCompute.ts    # Billing pure logic (sync กับ functions/core)
│   ├── hubDisplay.ts        # Hub/SOC display label helpers
│   └── app-version.ts       # WEB_APP_VERSION จาก package.json
├── validate/                # Zod schemas (Web-specific)
│   ├── hubSchema.ts         # Hub/SOC network group logic
│   └── taskSchema.ts        # SOC_DESTINATIONS, normalizeSocIdToKey
└── firestore.rules          # Firestore Security Rules (SSOT)
```

### Mobile Driver App

```
logitrack-mobile/lib/
├── core/
│   ├── services/
│   │   ├── mobile_install_id_service.dart    # Stable install ID
│   │   ├── mobile_client_heartbeat_service.dart  # Heartbeat → Firestore
│   │   └── mobile_app_version_service.dart   # Force-update via minAllowedVersion
│   └── theme/, fcm_service.dart, ...
└── features/
    ├── auth/                # Google Sign-In, email auth
    ├── home/                # Dashboard, MainLayout, Check-in, QR scan
    │   └── data/repositories/
    │       ├── trip_records_repository.dart
    │       ├── task_repository.dart
    │       ├── hubs_repository.dart
    │       └── hub_soc_distances_repository.dart
    ├── loading_phase/       # บันทึกรับงาน (Pick up)
    ├── delivery_phase/      # บันทึกส่งงาน (Single & Multi-stop)
    │   └── delivery_phase_page_multi.dart  # J&T multi-stop
    ├── vehicle_expense/     # ค่าน้ำมัน, ค่าซ่อม, maintenance
    ├── chat/                # แชทกับ Admin ต่อเที่ยว
    ├── broadcast/           # ประกาศจาก Admin
    ├── leave_request/       # ขอลา (SICK/BUSINESS)
    ├── working_holiday_calendar/  # ปฏิทินวันทำงาน/วันหยุด
    ├── job_record/          # ประวัติงาน
    └── trip_history/        # ประวัติเที่ยว
```

---

## 4. สถาปัตยกรรม RBAC (Role-Based Access Control)

### Roles และ Default Route

| Role | Default Route | ขอบเขตข้อมูล |
|------|--------------|-------------|
| **Admin** | `/app/dashboard` | ทุกอย่าง |
| **Customer** | `/app/driver-monitor` | เฉพาะ `billingCustomerId` ตรงกับ `customerScopeId` |
| **Partner** | `/app/driver-monitor` | เฉพาะ `partnerScopeId` ของตัวเอง |

### JWT Custom Claims

```
admin: true                   ← Admin role
customerScopeId: "cust_abc"  ← Customer role (scoped)
partnerScopeId: "sub_xyz"    ← Partner role (scoped)
```

### Capabilities

ทุก capability อยู่ใน [lib/capabilities.ts](../logitrack-web/lib/capabilities.ts) และ mapping role → capability อยู่ใน [lib/roles.ts](../logitrack-web/lib/roles.ts)

Key capabilities:
- `security_view_mobile_clients` — ดูหน้า Mobile Client Monitor
- `security_manage_users` — จัดการ Users
- `security_manage_roles` — แก้ไข Role Matrix

### Page Permission Guard

ทุก protected page ถูก wrap ด้วย `PagePermissionGuard` ([components/page-permission-guard.tsx](../logitrack-web/components/page-permission-guard.tsx)) ที่ตรวจ capability และ redirect ไปยัง default route ถ้าไม่มีสิทธิ์

---

## 5. สถาปัตยกรรม Firestore (Database)

### Collections หลัก

| Collection | ผู้ read | ผู้ write | หมายเหตุ |
|-----------|---------|---------|---------|
| `users` | เจ้าของ / Admin | เจ้าของ / Admin | Profile, FCM tokens, `forceLogout` flag |
| `drivers` | Auth | Admin / เจ้าของ | Driver profiles |
| `drivers/{id}/mobile_installations` | Admin / เจ้าของ | App Check + เจ้าของ | Heartbeat per device |
| `tasks` | Auth | Admin / Driver (check-in) | First Mile & Line Haul tasks |
| `trip_records` | Auth | Admin / Driver | บันทึกเที่ยว + billing snapshot |
| `hubs` | Auth | Auth create / Admin update | จุดรับงาน (Hub/SOC) |
| `hub_soc_distances` | Auth | Admin | Google Distance Matrix cache |
| `vehicle_expenses` | Admin / เจ้าของ | Admin / เจ้าของ | ค่าน้ำมัน, ค่าซ่อม, ค่าผ่านทาง |
| `maintenance` | Admin | Admin | งานซ่อมบำรุง |
| `leave_requests` | เจ้าของ / Admin | เจ้าของ create / Admin approve | คำขอลา |
| `standby_records` | เจ้าของ / Admin | Auth | บันทึก Standby |
| `customers` | Admin | Admin | ข้อมูลลูกค้า (CJSF, TTP ฯลฯ) |
| `settings` | Auth | Admin | `mobile_app.minAllowedVersion`, `apkDownloadUrl` |
| `permissions_config` | Admin | Admin | Role-permission config |

> Firestore Security Rules ฉบับเต็ม: [logitrack-web/firestore.rules](../logitrack-web/firestore.rules)

### Trip Record Schema (สำคัญ)

```typescript
trip_records/{tripId} {
  driverId: string          // Auth UID
  truckId: string
  taskId: string
  status: "loading" | "delivering" | "delivered" | "cancelled"
  photos: [{ type: string, url: string }]   // 8 ประเภท (Loading 4 + Delivery 4)
  deliveryStops?: [{                         // J&T multi-stop เท่านั้น
    sequence: number
    destinationName: string
    photos: [...]
    arrivedAt?: Timestamp
    completedAt?: Timestamp
  }]
  // Billing snapshot (คำนวณหลัง delivered)
  billingSnapshot?: {
    billingAmountTHB: number
    rateCardId: string
    ...
  }
  billingCustomerId?: string   // สำหรับ Customer scope filter
}
```

---

## 6. สถาปัตยกรรม Cloud Functions

### Pattern บังคับ: onCall + httpsCallable

```typescript
// Server (functions/src/feature.ts)
export const myFunction = onCall({ region: "asia-southeast1" }, async (request) => {
  // auth check, business logic
});

// Client (logitrack-web)
const fn = httpsCallable(functions, "myFunction");
const result = await fn({ param });
```

> ห้ามสร้าง API routes ใหม่ใน `app/api/` — ใช้ Cloud Functions เท่านั้น

### Functions ที่มีอยู่ (Index)

| Function | ประเภท | หน้าที่ |
|---------|--------|--------|
| `createUser` | onCall | สร้าง user พร้อม role/scope |
| `updateUserRole` | onCall | เปลี่ยน role + `customerScopeId` |
| `revokeUserRefreshTokens` | onCall | Force logout (Security Center) |
| `computeHubSocDistances` | onCall | Google Distance Matrix cache |
| `computeTripBillingSnapshot` | onCall | คำนวณ billing ต่อเที่ยว |
| `backfillTripBillingSnapshots` | onCall | Backfill billing งานเก่า |
| `backfillTaskCustomerLinks` | onCall | เติม customer links ให้งานเก่า |
| Cartrack sync | scheduled | sync ตำแหน่งรถ + odometer |
| bangchakOilPrice | scheduled | ดึงราคาน้ำมัน Bangchak |

---

## 7. ฟีเจอร์หลัก (Feature Summary)

### Web Admin

| หมวด | ฟีเจอร์ | Route |
|-----|---------|-------|
| **Operations** | Driver Monitor — real-time tracking, edit trip, share LINE | `/app/driver-monitor` |
| **Operations** | First Mile Task management + Hub dialog | `/app/first-mile` |
| **Operations** | Line Haul Task management | `/app/line-haul` |
| **Operations** | Standby Records view + backfill | `/app/standby-records` |
| **Fleet** | Trucks, compliance (tax/insurance) | `/app/trucks` |
| **Fleet** | Drivers, profiles | `/app/drivers` |
| **Fleet** | Maintenance (PM/CM) + cost tracking | `/app/maintenance` |
| **Accounting** | Fuel expenses | `/app/accounting/fuel` |
| **Accounting** | Other expenses + Toll import (Excel) | `/app/accounting/other` |
| **Accounting** | Income (billing) + pagination + export | `/app/accounting/income` |
| **Accounting** | Rate Card + Fuel Adjustment + CRUD | `/app/accounting/rate-card` |
| **Accounting** | Audit log | `/app/accounting/audit` |
| **Security** | Users + Edit/Enable/Disable | `/app/security-center/users` |
| **Security** | Role & Permission Matrix | `/app/security-center/roles` |
| **Security** | Mobile Client Monitor | `/app/security-center/mobile-clients` |
| **Utilities** | Backfill tools | `/app/utilities/backfill` |
| **Admin** | Hub/SOC sources + Distance Matrix | `/app/sources` |
| **Admin** | Holidays, Leave Requests | `/app/holidays`, `/app/leave-requests` |

### Mobile Driver App

| Feature | หน้าที่ | Notes |
|---------|--------|-------|
| **Check-in** | QR scan / manual → รับงาน | `home/check_in_page.dart` |
| **Loading Phase** | OCR รันชีท, ถ่ายรูป 4 ขั้น, seal code | `loading_phase/` |
| **Delivery Phase** | ส่งงาน, ถ่ายรูป 4 ขั้น, OCR ตรวจ LT Trip ID | `delivery_phase/` |
| **Multi-stop Delivery** | หลายจุดส่ง (J&T เท่านั้น) | `delivery_phase_page_multi.dart` |
| **Vehicle Expenses** | เติมน้ำมัน (OCR ใบเสร็จ), ค่าซ่อม, maintenance | `vehicle_expense/` |
| **Chat** | แชทกับ Admin ต่อเที่ยว (real-time) | `chat/` |
| **Broadcast** | ประกาศจาก Admin | `broadcast/` |
| **Leave Request** | SICK/BUSINESS, หลักฐาน upload | `leave_request/` |
| **Working Calendar** | ปฏิทินวันหยุด (Month/List view) | `working_holiday_calendar/` |
| **Force Update** | บล็อก dialog ถ้า version < `minAllowedVersion` | `mobile_app_version_service.dart` |
| **Heartbeat** | บันทึก version/platform ลง Firestore ทุก 45 วินาที | `mobile_client_heartbeat_service.dart` |

---

## 8. Billing System Architecture

```
task (First Mile / Line Haul)
  └── trip_record (ตอน delivered)
        └── computeTripBillingSnapshot (Cloud Function)
              ├── อ่าน sourceHubLinkedCustomerId, destinationLinkedCustomerId
              ├── normalizeDestinationCode (strip SPK suffix)
              ├── selectBillingRateEntry → fallback to oldest rate
              └── เขียน billingSnapshot ลงใน trip_record
```

**ไฟล์ที่ต้อง sync ทั้งคู่:**
- [logitrack-web/lib/billingCompute.ts](../logitrack-web/lib/billingCompute.ts)
- [logitrack-web/functions/src/core/billingCompute.ts](../logitrack-web/functions/src/core/billingCompute.ts)

---

## 9. Hub–SOC Network Groups

ระยะทาง Hub↔SOC คำนวณแยกตามกลุ่มเครือข่าย — ห้ามจับคู่ข้ามกลุ่ม:

| กลุ่ม | เครือข่าย | รหัส Hub |
|------|---------|---------|
| **SPX** | Shopee | ลงท้าย `-SPX` หรือตามกฎ `hubSourceIdHasSpxSuffix` |
| **SPK** | J&T / SPK | ขึ้นต้น `SPK` หรือ linkedCustomer code = `SPK`/`J&T`/`JT` |

SOC dedup ด้วย `normalizeSocIdToKey` (เช่น `SOCE` = `SOCE (…)`)

---

## 10. CI/CD Pipeline

```
GitHub Actions
├── ci.yml   (trigger: push to main, PR)
│   └── pnpm install → tsc --noEmit → eslint → vitest
└── deploy.yml  (trigger: push to main)
    └── Firebase Hosting deploy (dev/prod)
```

> ห้าม `firebase deploy` จาก local — ใช้ GitHub Actions เท่านั้น (ยกเว้นกรณีฉุกเฉิน CI ล่ม)

---

## 11. กฎสำคัญที่ต้องจำ (Key Rules)

### การ Implement

1. **Cloud Functions เท่านั้น** สำหรับ server logic ใหม่ — ห้ามใช้ `app/api/`
2. **SSOT schemas** — แก้ `shared-docs/schemas/` ก่อน แล้วค่อย sync Web/Mobile
3. **COLLECTIONS constants** — ห้าม hardcode ชื่อ collection
4. **i18n บังคับ** — UI text ทุกอย่างต้องผ่าน `t("key")` (Web) หรือ locale files (Mobile)
5. **BLoC** — Feature ใหม่ใน Flutter ต้องใช้ BLoC ไม่ใช่ setState
6. **API versioning** — parameter ใหม่ต้อง optional + server default (Mobile อาจยังใช้ version เก่า)

### Radix Select ใน Dialog

`SelectContent` ค่าเริ่มต้น `z-[1000]` ต่ำกว่า `DialogOverlay` `z-[1001]` → เมนูเลือกไม่ได้

**แก้:** ใส่ `className="z-[1005]"` และ `position="popper"` บน `SelectContent` ทุกตัวที่อยู่ใน Dialog

### Immutable Status History

```typescript
await updateDoc(ref, {
  status: "active",
  statusHistory: arrayUnion({ status, changedAt, changedBy, previousStatus }),
});
// ห้าม delete หรือแก้ไข entry เดิม
```

### Compliance Thresholds

| ระดับ | วันหมดอายุ | ระยะทาง |
|------|-----------|--------|
| Red (Overdue) | < 0 วัน | < 0 km |
| Orange (Due soon) | 0–30 วัน | 0–2,000 km |
| Blue (Incoming) | 31–60 วัน | 2,001–5,000 km |

---

## 12. สิ่งที่ค้างและ Roadmap

### ค้างอยู่ (Pending)

| # | งาน | Priority |
|---|-----|---------|
| P1 | RBAC: กำหนด `security_view_mobile_clients` ให้ role ใน Firestore (ผ่าน Role Matrix UI) | สูง |
| P2 | CI: เขียน `latestAndroidBuild` จาก GitHub Actions ลง `settings/mobile_app` | กลาง |
| P3 | Standby: Mobile flow (StandbyPage + ปุ่ม "Standby งานหมด"), billing line item, rate | กลาง |
| P4 | OTA / APK Auto-update | ต่ำ |

### Roadmap (ยังไม่ implement)

| Platform | งาน |
|---------|-----|
| Mobile | Firebase Crashlytics integration |
| Mobile | BLoC migration (loading_phase, delivery_phase, vehicle_expense) |
| Web | React Error Boundaries สำหรับ critical pages |
| Web | `develop` branch + DEV deploy workflow |
| CI/CD | Flutter CI (flutter analyze + flutter test) |
| CI/CD | APK build + sign pipeline |
| Security | Google Cloud Secret Manager สำหรับ production secrets |

---

> **อ่านเพิ่มเติม:** [shared-docs/.vibe-rules.md](./.vibe-rules.md) — กฎฉบับเต็ม, Change Log, Confirmed Patterns, Pending Decisions
