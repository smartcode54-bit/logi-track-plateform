# LogiTrack: Database Migration & Platform Scaling Plan
# Firestore → SQL (Hybrid Architecture) + Platform Management

> **สถานะ:** 📋 วางแผนไว้ — ยังไม่เริ่ม  
> **อัปเดตล่าสุด:** 18 มิ.ย. 2026  
> **หลักการ:** Migrate เฉพาะเมื่อมี driver ที่ชัดเจน — ไม่ migrate เพราะคิดว่าควร

> **ขอบเขตเอกสาร:** ครอบคลุม 2 เรื่องที่แยกกันแต่เกี่ยวข้อง
> 1. **Database migration** (Firestore → SQL hybrid) — เมื่อ data layer ไม่ตอบโจทย์
> 2. **Platform management** (IAM, secrets, IaC, observability) — ความพร้อมด้าน infra เมื่อ scale
>
> เรื่องที่ 2 **ทำได้ก่อน-อิสระจากการ migrate DB** และควรเริ่มเร็วกว่า เพราะเป็นรากฐานที่การ migrate จะพึ่งพา

---

## 🎯 Driver ของการ Migrate (ต้องตอบก่อนเริ่ม)

| Driver | Action |
|--------|--------|
| **ค่าใช้จ่าย Firestore สูงเกินไป** | ดู Firebase Console → Usage ก่อน อาจไม่ต้องทำอะไร |
| **ตัวเลขการเงินผิดพลาด / ACID ไม่พอ** | ทำเฉพาะ Phase 1 |
| **ทำ Report / Analytics ไม่ได้** | เพิ่ม BigQuery ข้างๆ Firestore (ไม่ต้อง migrate) |
| **ทั้งหมด** | ทำตามลำดับ Phase 1 → BigQuery → Phase 2+ |

---

## 📊 Architecture เป้าหมาย (Hybrid)

```
┌─────────────────────────────────────────────────────┐
│                   LogiTrack Platform                │
│                                                     │
│  ┌─────────────────┐      ┌─────────────────────┐  │
│  │   Firestore     │      │   Cloud SQL          │  │
│  │  (NoSQL)        │      │   (PostgreSQL)       │  │
│  │                 │      │                      │  │
│  │ • vehicle_      │      │ • customer_rate_     │  │
│  │   locations     │      │   entries            │  │
│  │ • chats         │      │ • vehicle_expenses   │  │
│  │ • security_     │      │ • transactions       │  │
│  │   events        │      │ • billing_snapshots  │  │
│  │ • mobile_       │      │ • tasks              │  │
│  │   installations │      │ • trip_records       │  │
│  │ • settings      │      │ • trucks             │  │
│  │ • users (auth)  │      │ • drivers            │  │
│  │ • permissions_  │      │ • customers          │  │
│  │   config        │      │ • hubs               │  │
│  └─────────────────┘      └─────────────────────┘  │
│           │                        │                │
│           └──────────┬─────────────┘                │
│                      │                              │
│              ┌───────────────┐                      │
│              │   BigQuery    │                      │
│              │  (Analytics)  │                      │
│              │               │                      │
│              │ • Reports     │                      │
│              │ • Dashboard   │                      │
│              │ • Export      │                      │
│              └───────────────┘                      │
└─────────────────────────────────────────────────────┘
```

---

## 🔴 Phase 1 — Financial Data (ทำก่อน, ความเสี่ยงต่ำสุด)

> **เป้าหมาย:** แก้ปัญหา ACID ของข้อมูลการเงิน  
> **ประมาณเวลา:** 2–4 สัปดาห์

### Collections ที่ย้าย

| Collection (Firestore) | Table (SQL) | เหตุผลหลัก |
|------------------------|-------------|------------|
| `customer_rate_entries` | `customer_rate_entries` | Rate ต้องสอดคล้องกับ billing เสมอ — ACID required |
| `customer_fuel_rate_adjustments` | `customer_fuel_rate_adjustments` | ส่วนหนึ่งของ rate computation — ต้อง atomic กับ rate |
| `customer_service_fees` | `customer_service_fees` | ค่าธรรมเนียมต้องสอดคล้องกับ invoice |
| `vehicle_expenses` | `vehicle_expenses` | ต้อง FK ไป `trucks` เพื่อป้องกัน orphan record |
| billing snapshots (`trip_records.billingSnapshot`) | `billing_snapshots` | ตัวเลขวางบิลต้องไม่เปลี่ยนย้อนหลัง |
| `transactions` | `transactions` | Double-entry — ต้องการ atomic multi-row write |
| `fuel_monthly_snapshots` | `fuel_monthly_snapshots` | Aggregate ที่ต้องการ SUM/GROUP BY ที่แม่นยำ |

### SQL Schema (Phase 1)

```sql
-- Rate Card
CREATE TABLE customer_rate_entries (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id     TEXT NOT NULL,        -- FK ไป customers (Phase 2)
  origin_hub_id   TEXT NOT NULL,
  destination_code TEXT NOT NULL,
  truck_type      TEXT NOT NULL,
  rate_thb        NUMERIC(12, 2) NOT NULL,
  effective_from  TIMESTAMPTZ NOT NULL,
  created_at      TIMESTAMPTZ DEFAULT NOW(),
  created_by      TEXT NOT NULL         -- Firebase uid
);

-- Fuel Adjustment
CREATE TABLE customer_fuel_rate_adjustments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id     TEXT NOT NULL,
  rate_multiplier NUMERIC(6, 4) NOT NULL,  -- e.g. 1.05 = +5%
  effective_from  TIMESTAMPTZ NOT NULL,
  note            TEXT,
  created_at      TIMESTAMPTZ DEFAULT NOW(),
  created_by      TEXT NOT NULL
);

-- Vehicle Expenses
CREATE TABLE vehicle_expenses (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  truck_id        TEXT NOT NULL,        -- FK ไป trucks (Phase 2)
  driver_id       TEXT,                 -- FK ไป drivers (Phase 2)
  type            TEXT NOT NULL,        -- 'fuel' | 'toll' | 'other'
  category        TEXT,                 -- 'toll' สำหรับค่าผ่านทาง
  amount_thb      NUMERIC(12, 2) NOT NULL,
  date            TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'PENDING',  -- 'PENDING' | 'APPROVED'
  note            TEXT,
  created_at      TIMESTAMPTZ DEFAULT NOW(),
  created_by      TEXT NOT NULL
);

-- Billing Snapshots
CREATE TABLE billing_snapshots (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  trip_record_id  TEXT NOT NULL UNIQUE, -- Firestore doc ID (Phase 1 bridge)
  customer_id     TEXT NOT NULL,
  rate_entry_id   UUID REFERENCES customer_rate_entries(id),
  base_rate_thb   NUMERIC(12, 2) NOT NULL,
  fuel_adjustment NUMERIC(6, 4),
  final_amount_thb NUMERIC(12, 2) NOT NULL,
  computed_at     TIMESTAMPTZ DEFAULT NOW(),
  computation_version INT NOT NULL DEFAULT 1
);

-- Transactions (Double-entry)
CREATE TABLE transactions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  type            TEXT NOT NULL,        -- 'income' | 'expense'
  reference_id    TEXT,                 -- billing_snapshot_id หรือ expense_id
  amount_thb      NUMERIC(12, 2) NOT NULL,
  description     TEXT,
  transaction_date TIMESTAMPTZ NOT NULL,
  created_at      TIMESTAMPTZ DEFAULT NOW(),
  created_by      TEXT NOT NULL
);
```

### สิ่งที่ต้องทำ (Phase 1 Checklist)

- [ ] สร้าง Cloud SQL instance (PostgreSQL 15+)
- [ ] เขียน migration script ดึงข้อมูลจาก Firestore → SQL
- [ ] สร้าง Cloud Function / API layer สำหรับ write ไป SQL แทน Firestore
- [ ] อัปเดต `billingCompute.ts` ให้อ่าน rate จาก SQL
- [ ] อัปเดต `actions.client.ts` (accounting) ให้ write ไป SQL
- [ ] ทำ dual-write ชั่วคราว (Firestore + SQL) ระหว่าง transition
- [ ] ตรวจสอบยอดเงินทั้งสอง DB ให้ตรงกันก่อนตัด Firestore ออก
- [ ] อัปเดต Firestore Rules ลบ rules ของ collections ที่ย้ายออก

### ⚠️ จุดพังคลาสสิก: Cloud Functions ↔ Cloud SQL connection

Serverless (Cloud Functions Gen 2 / Cloud Run) + SQL มีปัญหา connection ที่ต้องวางแผนตั้งแต่ต้น:

- **Connection exhaustion:** ทุก instance เปิด connection pool ของตัวเอง พอ functions scale ออกหลาย instance พร้อมกัน → connection ทะลุ `max_connections` ของ Cloud SQL → query เริ่ม fail
  - ตั้ง **pool เล็กต่อ instance** (เช่น `max: 1–5`) ไม่ใช่ค่า default ของ ORM
  - ใช้ **Cloud SQL Connector** (IAM auth) หรือ connection ผ่าน Unix socket แทน public IP
  - พิจารณา **PgBouncer / Cloud SQL connection pooling** ถ้า instance เยอะ
- **Cold start latency:** การเปิด connection ครั้งแรกช้า — reuse connection ข้าม invocation (global scope) อย่าเปิด/ปิดต่อ request
- **Least privilege:** สร้าง SA เฉพาะสำหรับ functions เข้าถึง Cloud SQL (`roles/cloudsql.client`) ไม่ใช้ deployer SA
- **Private IP + VPC:** prod ควรให้ Cloud SQL อยู่ใน VPC (private IP) ไม่เปิด public — functions เชื่อมผ่าน VPC connector / Direct VPC egress

### 🔙 Rollback & Reconciliation (Phase 1)

- **Dual-write ต้องมี feature flag** (`BILLING_WRITE_TARGET = firestore | sql | both`) เพื่อสลับกลับได้ทันทีถ้า SQL มีปัญหา โดยไม่ต้อง redeploy
- **Reconciliation job:** เขียน scheduled job เทียบยอด `billing_snapshots` ระหว่าง Firestore vs SQL ทุกวัน — alert ถ้าไม่ตรง **ก่อน** ตัด Firestore ออก
- **Cut-over criteria:** ตัด Firestore ออกได้ก็ต่อเมื่อ reconciliation ตรง 100% ติดต่อกัน ≥ 2 สัปดาห์
- **Backup ก่อนทุก migration run:** `gcloud sql backups create` + export Firestore ที่จุด cut-over

---

## 🟡 Phase 2 — Operations Core (หลัง Phase 1 stable)

> **เป้าหมาย:** ย้าย Master Data และ Operations เพื่อ referential integrity  
> **ประมาณเวลา:** 4–8 สัปดาห์  
> **เงื่อนไข:** Phase 1 ต้อง stable อย่างน้อย 4 สัปดาห์ก่อน

### Collections ที่ย้าย

| Collection (Firestore) | Table (SQL) | เหตุผลหลัก |
|------------------------|-------------|------------|
| `customers` | `customers` | rate card, billing, scope ล้วน FK มาที่นี่ |
| `trucks` | `trucks` | FK ใน expenses, trip_records, assignments |
| `drivers` | `drivers` | FK ใน trips, tasks, assignments |
| `hubs` | `hubs` | origin/destination ของ tasks และ rate card |
| `hub_soc_distances` | `hub_soc_distances` | Lookup table — SQL index เร็วกว่า Firestore |
| `soc_hub_distances` | `soc_hub_distances` | เหตุผลเดียวกัน |
| `tasks` | `tasks` | JOIN กับ customer, hub, driver บ่อย |
| `trip_records` | `trip_records` + `delivery_stops` + `trip_photos` | แตก nested array ออกเป็นตาราง |
| `truckAssignment` | `truck_assignments` | Junction table — ต้องการ FK ทั้ง 2 ฝั่ง |
| `standby_records` | `standby_records` | เชื่อมกับ driver และ trip |
| `incidentReport` | `incident_reports` | เชื่อมกับ truck, driver, trip |
| `maintenance` | `maintenance_records` | SUM cost GROUP BY truck ต้องการ SQL |

### ⚠️ ข้อควรระวัง Phase 2

**`trip_records` มี nested data ที่ต้องแตก:**

```sql
-- แทนที่ deliveryStops[] array
CREATE TABLE delivery_stops (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  trip_record_id  UUID NOT NULL REFERENCES trip_records(id),
  sequence        INT NOT NULL,
  destination_name TEXT,
  lat             DOUBLE PRECISION,
  lng             DOUBLE PRECISION,
  arrived_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ
);

-- แทนที่ photos[] array
CREATE TABLE trip_photos (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  trip_record_id  UUID REFERENCES trip_records(id),
  delivery_stop_id UUID REFERENCES delivery_stops(id),  -- NULL ถ้าเป็น loading phase
  type            TEXT NOT NULL,    -- 'loading_step_1' | 'delivery_step_1' ฯลฯ
  url             TEXT NOT NULL,
  uploaded_at     TIMESTAMPTZ DEFAULT NOW()
);
```

**`users` — ยังคงใช้ Firebase Auth เป็น source of truth:**
- เก็บแค่ `firebase_uid` เป็น FK ใน `drivers` และ `customers`
- Profile (`displayName`, `email`) อ่านจาก Firebase Auth token เสมอ
- ไม่ duplicate ข้อมูล auth ลง SQL

---

## 🟢 BigQuery — Analytics Layer (ทำคู่ขนาน, ไม่บล็อก Phase ใด)

> **เป้าหมาย:** ให้ทำ Report และ Dashboard ได้ด้วย SQL โดยไม่กระทบ production  
> **ประมาณเวลา:** 1–3 วัน (Firebase Extension + setup)

### วิธีทำงาน

```
Firestore ──[Firebase Extension: firestore-bigquery-export]──→ BigQuery
                  (sync อัตโนมัติ real-time)

SQL (Cloud SQL) ──[Scheduled Query / Datastream]──→ BigQuery
                  (sync ทุก N นาที หรือ real-time)

BigQuery ← Admin/Analytics queries (SQL เต็มรูปแบบ)
         ← Looker Studio / Dashboard
         ← Export Excel
```

### Collections ที่ควร sync ไป BigQuery (ทันที)

- `trip_records` — รายงานเที่ยว, billing summary
- `tasks` — ปริมาณงานต่อลูกค้า, ต่อคนขับ
- `vehicle_expenses` — ต้นทุนรวมต่อรถ
- `maintenance` — ค่าซ่อมสะสม

### ข้อจำกัด BigQuery

- ข้อมูลอาจ delay 1–2 นาทีจาก real-time (near real-time)
- ใช้สำหรับ **อ่านอย่างเดียว** — ห้าม write กลับ production ผ่าน BigQuery
- คิดเงินตาม data scanned — ต้องระวัง query ที่ scan ข้อมูลเยอะโดยไม่จำเป็น

---

## 🔵 คงไว้ใน Firestore (ไม่ย้าย)

| Collection | เหตุผล |
|-----------|--------|
| `vehicle_locations` | Write ทุก 5–10 วินาที/คัน — SQL ไม่รองรับ throughput นี้ |
| `chats` | Real-time listener, โครงสร้างยืดหยุ่น |
| `security_events` / audit logs | Append-only, ปริมาณมาก, ไม่ต้อง JOIN |
| `mobile_installations` | Heartbeat บ่อย, อ่านเฉพาะ Admin |
| `settings` | Key-value config ไม่กี่แถว |
| `permissions_config` | Firestore Rules ใช้ `get()` อ่านระหว่าง rule evaluation โดยตรง |
| `users` (profile) | Firebase Auth คือ source of truth — ห้าม duplicate |

---

## 📏 Integration Pattern (วิธีเชื่อมสองระบบ)

```typescript
// ใช้ firebase_uid เป็นสะพานเชื่อม
// SQL: drivers.firebase_uid = "abc123"
// Firebase Auth: uid = "abc123"
// ไม่ duplicate email/displayName ลง SQL

// ตัวอย่าง query ข้ามระบบ:
// 1. อ่าน uid จาก Firebase Auth token
// 2. Query SQL: SELECT * FROM trip_records WHERE driver_firebase_uid = uid
// 3. แสดงชื่อจาก Firebase Auth displayName (ไม่ต้อง JOIN กับ users table)
```

---

## 🚦 Trigger สำหรับเริ่ม Phase

| Phase | เริ่มเมื่อ |
|-------|----------|
| **BigQuery** | ต้องการ report ที่ Firestore ทำไม่ได้ → ทำได้เลย |
| **Phase 1** | พบปัญหาตัวเลขการเงินผิด หรือ Firestore bill เกิน $50/เดือน |
| **Phase 2** | Phase 1 stable 4 สัปดาห์ + ทีมพร้อม + มี schema review |

---

## 🛠️ Platform Management (ทำได้ก่อน — ไม่ต้องรอ migrate DB)

> **ที่มา:** การ deploy CI/CD รอบ 18 มิ.ย. 2026 ต้องไล่แก้ IAM/API/secret/billing ทีละตัวด้วยมือ (ActAs appspot SA → compute SA, serviceUsageConsumer, firebase.admin, cloudscheduler.admin, เปิด Cloud Billing API, เขียน functions `.env` จาก secret) — สะท้อนว่า platform config ยังเป็น **manual & undocumented** ซึ่งจะเป็นคอขวดเมื่อ scale หรือเพิ่มคนในทีม

### 1. Infrastructure as Code (IaC) — สำคัญสุด
ปัญหาปัจจุบัน: IAM/API ที่แก้ไปจำได้จาก log เท่านั้น ทำซ้ำบน prod / โปรเจกต์ใหม่ไม่ได้แบบ reproducible

- [ ] ใช้ **Terraform** (หรือ `gcloud` script ที่ commit ไว้) นิยาม: service accounts, IAM bindings, enabled APIs, Cloud SQL instance, scheduler jobs
- [ ] เก็บ IAM grants ที่แก้ไปวันนี้เป็นโค้ด เพื่อ apply บน `logitrack-prod` ได้เหมือนกันโดยไม่ต้องไล่ error ใหม่
- [ ] State file เก็บใน GCS bucket (remote backend)

**ชุด IAM ขั้นต่ำของ deployer SA (อ้างอิงจากที่แก้จริง — ใช้เป็น checklist เวลา setup โปรเจกต์ใหม่/prod):**

| Role / API | ทำไม |
|------------|------|
| `roles/iam.serviceAccountUser` บน **appspot SA** | deploy functions (ActAs runtime SA) |
| `roles/iam.serviceAccountUser` บน **compute SA** | Gen 2 functions รันด้วย compute SA |
| `roles/serviceusage.serviceUsageConsumer` | เช็ก/เปิด API ตอน deploy |
| `roles/firebase.admin` | hosting + firestore rules + functions |
| `roles/cloudscheduler.admin` | scheduled functions (onSchedule) |
| Enable `cloudbilling.googleapis.com` | Gen 2 ต้องเช็ก billing |

### 2. Secret Management
- ปัจจุบัน: GitHub Secrets + `functions/.env.<project>` (gitignored) — โอเคสำหรับตอนนี้
- เมื่อ scale: ย้าย secret ที่อ่อนไหว (Cartrack password, Maps key) ไป **Google Secret Manager** + ใช้ `defineSecret` แทน `defineString` ใน functions → หมุน key ได้โดยไม่ต้องแก้ secret หลายที่
- [ ] เอกสารรวมว่ามี secret อะไรบ้าง อยู่ที่ไหน ใครหมุนได้ (ตอนนี้กระจัดกระจาย)

### 3. Environment Separation
- ปัจจุบัน: dev (`logi-track-wrt-dev`) auto-deploy, prod (`logitrack-prod`) manual — ดีแล้ว
- [ ] เพิ่ม **staging** ถ้าทีมโตขึ้น (ทดสอบ migration/feature เสี่ยงก่อนแตะ prod)
- [ ] PROD ยังไม่ได้ทำ IAM hardening ชุดเดียวกับ dev — **ต้องทำก่อน deploy functions ขึ้น prod ครั้งแรก**

### 4. CI/CD Maturity
- [ ] อัปเดต GitHub Actions versions (checkout/setup-node/artifact/auth) ออกจาก Node 20 (ดู warning — ยังไม่ critical)
- [ ] เพิ่ม step ตรวจ deploy แบบ dry-run / preview channel ก่อน deploy จริง
- [ ] Notify (Slack/email) เมื่อ deploy fail

---

## 📡 Observability (ขาดทั้งหมดตอนนี้ — ควรมีก่อน scale)

ปัจจุบันรู้ว่าระบบมีปัญหาก็ต่อเมื่อผู้ใช้แจ้ง ควรมี:

- [ ] **Error tracking** (Sentry / Firebase Crashlytics สำหรับ mobile) — จับ exception ทั้ง web/functions/app
- [ ] **Uptime / health check** — ping endpoint หลัก, alert เมื่อ down
- [ ] **Cloud Functions metrics & alert** — error rate, latency, invocation count (Cloud Monitoring)
- [ ] **Cost alert** — Budget alert บน GCP เมื่อค่าใช้จ่ายเกิน threshold (กันบิลบานปลายแบบไม่รู้ตัว)
- [ ] **Structured logging** — functions log แบบ query ได้ (มี logger อยู่แล้ว ขยายให้ครอบคลุม)

---

## 🧯 Risk Register (Migration)

| ความเสี่ยง | ผลกระทบ | การป้องกัน |
|-----------|---------|-----------|
| ยอดเงินไม่ตรงหลัง dual-write | บิลผิด, ความเชื่อถือ | Reconciliation job + cut-over criteria 2 สัปดาห์ |
| Connection exhaustion (functions+SQL) | query fail ช่วง peak | Pool เล็ก + Cloud SQL Connector + load test ก่อน |
| migrate ระหว่างมี traffic | data loss / inconsistency | Dual-write + migrate นอกเวลาพีค + backup ก่อน |
| ทีมไม่คุ้น SQL/ops | bug, downtime | เริ่ม Phase 1 เล็กๆ, schema review, runbook |
| Lock-in กลับทาง (SQL → อยากกลับ) | ย้ายยาก | คง Firestore เป็น source ระหว่าง transition จนมั่นใจ |

---

## 📋 Decision Log

| วันที่ | การตัดสินใจ | เหตุผล |
|--------|------------|--------|
| 24 พ.ค. 2026 | ยังไม่ migrate — ใช้ Firestore ต่อ | Traffic ยังอยู่ในระดับที่ Firestore รองรับได้, ไม่มี billing accuracy ปัญหาเร่งด่วน |
| 24 พ.ค. 2026 | วางแผน Hybrid Architecture ไว้ | เพื่อให้มีแผนชัดเจนเมื่อถึงเวลา migrate จริง |
| 18 มิ.ย. 2026 | เพิ่มขอบเขต **Platform Management** เข้าแผน | คำแนะนำรุ่นพี่เรื่อง scale + ประสบการณ์ deploy CI/CD ที่ต้องไล่แก้ IAM/API ด้วยมือ → ต้องทำ IaC/observability ก่อน scale (ทำได้อิสระจากการ migrate DB) |
| 18 มิ.ย. 2026 | ยังคงไม่ migrate DB ตอนนี้ | ปัญหาที่เจอเป็นเรื่อง platform config ไม่ใช่ data layer — Firestore ยังตอบโจทย์ ให้ทำ platform hardening ก่อน |
| 25 ส.ค. 2026 | **เลือก Supabase (Postgres) เป็น SQL host ของแพลตฟอร์ม** (แทนสมมติฐาน Cloud SQL เดิม) — เริ่มด้วย **Buzzebee distribution** เป็นโมดูล SQL-native ตัวแรก (ดู [ADR 0024](adr/0024-buzzebee-distribution-on-supabase.md)) | domain ใหม่ = ไม่มีข้อมูลเก่าให้ migrate → เสี่ยงต่ำสุดในการเริ่ม SQL. Supabase ให้ built-in pooler (แก้ connection exhaustion §161-171), Firebase third-party auth + RLS (คง Firebase Auth SSOT ตาม §288-300), client SDK ลด callable. แลกกับ vendor นอก GCP. **ผลต่อแผน:** Phase 1/2 ในอนาคตให้อ่านโดยใช้ Supabase เป็น host แทน Cloud SQL |

---

> **อ่านไฟล์นี้ประกอบกับ:** `.vibe-rules.md`, `architecture.md`, `shared-docs/schemas/`
