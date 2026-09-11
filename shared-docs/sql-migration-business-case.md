# SQL Migration: Business Case & Trigger Thresholds
# "คุ้มไหม" และ "เมื่อไหร่ถึงต้องย้าย"

> **สถานะ:** 📊 วิเคราะห์ — ยังไม่ตัดสินใจ
> **สร้าง:** 11 ก.ย. 2026
> **คู่กับ:** `database-migration-plan.md` (แผน *วิธีย้าย*) — เอกสารนี้ตอบคนละคำถาม คือ ***ควรย้ายไหม / เมื่อไหร่***
> **ขอบเขต:** เฉพาะการย้ายข้อมูล **ที่มีอยู่แล้ว** จาก Firestore → SQL
> ไม่เกี่ยวกับ ADR 0024 (Buzzebee distribution) ซึ่งเป็น greenfield ไม่ย้ายอะไรเลย

---

## 🎯 บทสรุปผู้บริหาร

**ค่าใช้จ่ายไม่ใช่เหตุผลที่จะย้าย — และแทบเป็นไปไม่ได้ที่จะเป็น**
การย้ายไป Supabase **เพิ่ม** ต้นทุนคงที่ ~$35/เดือน ทันที ในขณะที่ Firestore ที่ขนาดนี้น่าจะยังอยู่ในโควตาฟรีหรือใกล้เคียง
จุดคุ้มทุนด้านค่าใช้จ่ายอยู่ที่ประมาณ **1.94 ล้าน read/วัน** ซึ่งห่างจากขนาดงานจริงหลายเท่าตัว

**เหตุผลที่แท้จริง (ถ้ามี) คือ "ความถูกต้องของข้อมูลเชิงโครงสร้าง"**
หลักฐานในโปรเจกต์: backfill Cloud Function **4 ตัว**, ops script ซ่อมข้อมูล **~11 ตัว**, commit แนวซ่อมข้อมูล **30 ครั้ง**
แต่ **ครึ่งหนึ่งของงานซ่อมเหล่านี้ SQL แก้ไม่ได้** เพราะเป็น business-logic bug ไม่ใช่ integrity bug — ดูตารางแยกใน §6

**คำแนะนำ:** ยังไม่ย้าย ทำ 3 อย่างที่ถูกกว่ามากก่อน (§5) แล้วเฝ้าดู trigger ที่วัดได้ (§4)

---

## 1. ต้นทุนฝั่ง Supabase (ตัวเลขยืนยันแล้ว)

| รายการ | Free | Pro |
|---|---|---|
| ราคา org | $0 | **$25/เดือน** |
| จำนวน project | 2 | ไม่จำกัด (จ่าย compute ต่อ project) |
| Database ต่อ project | 500 MB | 8 GB disk |
| Compute | รวมอยู่ | **+$10/project** (Micro) — มี credit $10 คืนให้ 1 ตัว |
| **หยุดทำงานเมื่อไม่ใช้ 1 สัปดาห์** | ✅ ใช่ | ❌ ไม่ |
| Egress | 5 GB | 250 GB (เกินคิด $0.09/GB) |
| Database size เกิน | — | $0.125/GB |

**ต้นทุนจริงของเรา (dev + prod):**
- prod ใช้ Free **ไม่ได้** เพราะ project จะถูก pause หลังไม่มี traffic 1 สัปดาห์
- dev + prod บน Pro org = `$25 + $10 + $10 − $10 credit` = **$35/เดือน ≈ $420/ปี**

อ้างอิง: [Supabase Pricing](https://supabase.com/pricing) · [Billing docs](https://supabase.com/docs/guides/platform/billing-on-supabase)

---

## 2. ต้นทุนฝั่ง Firestore และจุดคุ้มทุน

เรตอ้างอิงจากตัวอย่างทางการของ Firebase (multi-region `nam5`; region `asia-southeast3` ของเราต่างออกไป — ต้องอ่านค่าจริงจาก console):

| หน่วย | ราคา | โควตาฟรี/วัน |
|---|---|---|
| Document read | $0.06 / 100,000 | **50,000** |
| Document write | $0.18 / 100,000 | **20,000** |
| Document delete | $0.02 / 100,000 | 20,000 |
| Storage | $0.18 / GiB/เดือน | 1 GiB |

**จุดคุ้มทุน — Firestore ต้องแพงถึงเท่าไหร่ถึงจะเท่ากับ Supabase $35/เดือน:**

| ถ้าค่าใช้จ่ายมาจาก | ต้องใช้เท่าไหร่ถึงจะถึง $35/เดือน |
|---|---|
| Read ล้วน | **58.3 ล้าน read/เดือน ≈ 1.94 ล้าน read/วัน** |
| Write ล้วน | **19.4 ล้าน write/เดือน ≈ 648,000 write/วัน** |
| Storage ล้วน | **195 GiB** |

**ตัวเลขที่เรารู้จริงจากโค้ด:**
- `functions/src/cartrack.ts:86` → `schedule: "every 3 minutes"` = 480 รอบ/วัน × จำนวนรถ
  → รถ 40 คัน ≈ **19,200 write/วัน** (เกือบชนโควตาฟรี 20,000 พอดี — นี่คือตัวกินหลัก)
- web มี `onSnapshot` **52 จุด**, mobile มี `.snapshots()` **9 จุด** → read ขึ้นกับจำนวนแอดมินที่เปิดหน้าจอค้างไว้

👉 แม้สมมติว่า read สูงถึง 100,000/วัน (2 เท่าของโควตาฟรี) ค่าใช้จ่ายส่วนเกินจะอยู่แค่ราว **$0.03/วัน ≈ $0.90/เดือน**
เทียบกับ Supabase $35/เดือน → **ย้ายเพราะค่าใช้จ่าย = ขาดทุนแน่นอน**

อ้างอิง: [Firestore billing example](https://firebase.google.com/docs/firestore/billing-example) · [Understand billing](https://firebase.google.com/docs/firestore/pricing)

> ⚠️ **ต้องทำก่อนเชื่อตัวเลขนี้:** อ่านค่าใช้จ่ายจริง 2 นาที →
> Firebase Console → โปรเจกต์ `logitrack-prod` → **Usage and billing** → ดู read/write/storage ต่อวัน
> หรือ GCP Console → **Billing → Reports** → filter service = *Cloud Firestore* ย้อนหลัง 3 เดือน
> ถ้าตัวเลขจริงเกิน $50/เดือนต่อเนื่อง ให้กลับมาคิดใหม่ทั้งหมด

---

## 3. ต้นทุนแรงงาน (ส่วนที่มักถูกลืม)

จาก `database-migration-plan.md` เอง:

| Phase | เวลาที่แผนประเมิน |
|---|---|
| Phase 1 (การเงิน) | 2–4 สัปดาห์ |
| Phase 2 (operations) | 4–8 สัปดาห์ (+ ต้องรอ Phase 1 นิ่ง 4 สัปดาห์) |
| **รวม** | **10–16 สัปดาห์** |

บวกงานที่ต้องแบกถาวรหลังย้าย: reconciliation job, RLS policy + test (ของใหม่ที่เทียบเท่า `firestore.rules`),
backup/DR ของ vendor ที่สอง, cost monitoring อีกชุด, connection pool tuning

**และงานที่ต้องทำเพิ่มถ้าย้าย mobile ด้วย:** ต้องซื้อ/ติดตั้ง offline layer (PowerSync/Brick) เพราะ Supabase ไม่มี native offline
→ vendor ที่สาม + ค่าใช้จ่าย + sync/conflict logic ที่ต้องดูแลเอง

---

## 4. Trigger ที่วัดได้ — "เมื่อไหร่ถึงย้าย"

ห้ามย้ายด้วยความรู้สึก ให้ย้ายเมื่อ **อย่างน้อย 1 ข้อ** ต่อไปนี้เป็นจริง และวัดได้:

| # | Trigger | เกณฑ์ที่วัดได้ | วิธีวัด |
|---|---|---|---|
| **T1** | **ค่าใช้จ่าย Firestore แซง** | บิล Firestore > **$50/เดือน** ต่อเนื่อง **3 เดือน** | GCP Billing → Reports → filter Cloud Firestore |
| **T2** | **หนี้ integrity โตเร็วกว่าที่ซ่อมไหว** | ต้องเขียน backfill/repair script ใหม่ **≥ 2 ตัว/ไตรมาส ติดกัน 2 ไตรมาส** | `ls functions/scripts/` + `git log --grep=backfill` |
| **T3** | **มี query ที่ธุรกิจต้องการแต่ Firestore ทำไม่ได้** | คำขอรายงานที่ทำไม่ได้ **≥ 3 เรื่อง** *และ* BigQuery แก้ไม่ได้ | รายการคำขอจาก ops/บัญชี |
| **T4** | **ตัวเลขเงินผิดถึงมือลูกค้า** จากเหตุที่ constraint/transaction จะกันได้ | **≥ 1 ครั้ง** ที่ invoice ออกผิดเพราะข้อมูลอ้างอิงหาย/ไม่ตรง (ไม่ใช่ logic bug) | post-mortem ต่อ incident |

**Counter-trigger — สัญญาณว่า *ห้าม* ย้ายส่วนนั้น** (เกณฑ์เต็มใน `learning-log.md` entry 11 ก.ย. 2026):
- `firestore.rules` อ่าน collection นั้นด้วย `get()`/`exists()` → ย้ายไม่ได้ (`drivers` 25 จุด, `permissions_config` 2, `chats` 2)
- คนขับแตะ collection นั้นตอนออฟไลน์ → ย้ายไม่ได้ (`tasks`, `trip_records`, `trucks`, `hubs`)

---

## 5. ทำ 3 อย่างนี้ก่อน — ถูกกว่ามากและแก้ปัญหาเดียวกัน

| ทางเลือก | แก้อะไรได้ | ต้นทุน |
|---|---|---|
| **BigQuery export** (Firebase Extension) | T3 ทั้งหมด — รายงาน/analytics ด้วย SQL เต็มรูปแบบ โดยไม่แตะ production | 1–3 วัน + จ่ายตาม data scanned (แผนเดิมประเมินไว้) |
| **Zod validate ตอนเขียน + Firestore transaction** | T2/T4 บางส่วน — กันข้อมูลอ้างอิงหายตั้งแต่ต้นทาง | มี `validate/*.ts` อยู่แล้ว ขยายต่อ |
| **เลิก denormalize ที่ไม่จำเป็น** | T2 โดยตรง — `driverName`, `truckLicensePlate` ที่ copy ไว้หลายที่คือที่มาของ backfill ส่วนใหญ่ | refactor ทีละจุด |

---

## 6. ความจริงที่ต้องยอมรับ: SQL แก้บั๊กเราได้แค่ครึ่งเดียว

ไล่จากประวัติจริงของโปรเจกต์ ว่า **ถ้าตอนนั้นอยู่บน SQL แล้วจะไม่เกิดไหม**:

| ปัญหาที่เคยเกิด | SQL ช่วยไหม | เพราะอะไร |
|---|---|---|
| `backfillTaskCustomerLinks` — งานเก่าไม่มี `sourceHubLinkedCustomerId` | ✅ **ช่วย** | `NOT NULL` + FK ทำให้สถานะ "ไม่มีลูกค้า" เขียนลงไปไม่ได้ตั้งแต่แรก |
| `backfillTripTruckData` — `truckId`/ทะเบียนไม่ครบทุก collection | ✅ **ช่วย** | JOIN เอาได้ ไม่ต้อง denormalize → ไม่มีอะไรให้ backfill |
| "No rate" — map `nameToCode`/`codeToName` ถูก merge รวมกัน (บันทึก 15 มิ.ย.) | ❌ **ไม่ช่วย** | เป็นบั๊กใน logic ของโค้ด ไม่ใช่เรื่อง schema |
| เสริมโดนคิด fuel — `jobCategory` default เป็น PRIMARY ตอนสร้าง | ❌ **ไม่ช่วย** | เป็นค่า default ผิดในโค้ด SQL ก็ default ผิดได้เหมือนกัน |
| `effectiveFrom` เก็บเป็น UTC midnight → ราคาข้ามคืนผิดรอบ | 🟡 **ช่วยบางส่วน** | `TIMESTAMPTZ` ชัดกว่า แต่ต้นเหตุคือการแปลง timezone ในโค้ด |
| `billingEstimateThb` ค้างค่าเก่าหลัง recompute ล้ม | 🟡 **ช่วยบางส่วน** | transaction ช่วยได้ แต่ Firestore ก็มี transaction เหมือนกัน |

**สรุป: ~2 จาก 6 เรื่องที่ SQL แก้ได้จริง** — ถ้าคาดหวังว่าย้าย DB แล้วบั๊ก billing จะหาย จะผิดหวัง
สิ่งที่ SQL ให้จริงคือ **"สถานะที่ผิดจะเขียนลง DB ไม่ได้"** เฉพาะกรณีที่ผิดเชิงโครงสร้าง (referential integrity) เท่านั้น

---

## 7. คำตัดสิน ณ วันนี้

| คำถาม | คำตอบ |
|---|---|
| ย้ายข้อมูลเดิมไป SQL ตอนนี้คุ้มไหม? | ❌ **ไม่คุ้ม** — จ่ายเพิ่ม $420/ปี + แรงงาน 10–16 สัปดาห์ เพื่อแก้ปัญหาได้ ~1/3 |
| แล้ว ADR 0024 (Buzzebee บน Supabase) ล่ะ? | ✅ **คนละเรื่อง ยังเดินหน้าได้** — greenfield ไม่ย้ายข้อมูล ได้ทดลอง SQL จริงโดยไม่เสี่ยง แต่ต้องยอมรับ $35/เดือน เป็นต้นทุนของ "โมดูลใหม่" ไม่ใช่ต้นทุน migration |
| ต้องทำอะไรต่อ? | วัดของจริง (§2 กล่องเตือน) → ทำ §5 → เฝ้า trigger §4 |

---

## 📋 Decision Log

| วันที่ | การตัดสินใจ | เหตุผล |
|---|---|---|
| 11 ก.ย. 2026 | **ยังไม่ย้ายข้อมูลเดิม** — ตั้ง trigger T1–T4 ที่วัดได้แทน | จุดคุ้มทุนค่าใช้จ่ายห่างจากขนาดงานจริง ~100 เท่า; งานซ่อมข้อมูลที่ SQL แก้ได้จริงมีแค่ ~1/3 |
