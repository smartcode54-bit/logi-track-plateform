# Learning Log — LogiTrack Platform

> บันทึกสิ่งที่เรียนรู้ระหว่างทำงานจริง (SWE Mentor Mode)
> แต่ละ entry: Concept → อธิบาย → Trade-off → อ้างอิง → ต่อยอด

---

## [2026-09-11] เช็ค skill / คำถาม "ทำไมต้องรันบน terminal"

**Stack:** ทั่วไป (tooling) — ผูกกับ JS/TS, Node/pnpm, Firebase CLI, Flutter

**Concept:** CLI-first tooling & ปรัชญา Unix — ทำไมงาน dev ส่วนใหญ่วางอยู่บน command line

**อธิบาย:**
เครื่องมือ dev เกือบทั้งหมด (`git`, `pnpm`, `firebase`, `flutter`, `tsc`) เป็น CLI เป็นหลัก แล้วค่อยมี GUI ครอบทีหลัง เหตุผลหลักคือ CLI ให้สิ่งที่ GUI ให้ไม่ได้:
- **Automation/Scriptable** — คำสั่งเป็นข้อความ → เก็บใน script/CI ได้ (เช่น `.github/workflows/ci.yml` รัน tsc+eslint+vitest)
- **Reproducible/Shareable** — copy คำสั่ง + commit ลง git ได้ (เช่น `package.json` scripts)
- **Composable** — pipe เครื่องมือเล็กต่อกัน (ปรัชญา Unix)
- **Remote/Headless** — server จริง (Cloud Functions, CI runner) ไม่มีหน้าจอ ต้องคุยผ่าน CLI/SSH
- **เร็ว + resource น้อย**

ประเด็นที่ต้องเข้าใจ: CLI-first ≠ "เท่กว่า" แต่ = "automate/reproduce ได้"

**ทางเลือกที่พิจารณา / Trade-off:**
GUI อ่านง่าย/สำรวจง่ายกว่า (เช่นดู git history, ดู diff) แต่คลิกเมาส์ automate ซ้ำไม่ได้ → dev เก่งใช้ทั้งสอง เลือกตามงาน ไม่ใช่เลือกข้าง

**หมายเหตุ Claude Code:** slash command interactive (`/permissions`, `/config`, `/mcp`, `/hooks`) ใช้ได้เฉพาะใน `claude` terminal จริง ไม่ทำงานใน Code tab ของ desktop app

**อ้างอิง:**
- [Command Line vs. GUI — Computer Hope](https://www.computerhope.com/issues/ch000619.htm)
- [Why CLIs Suck (and GUIs are Better) — Okta Developer](https://developer.okta.com/blog/2020/02/19/why-clis-suck-guis-are-better)
- [Reasons to Prefer CLI to GUIs — GitHub gist](https://gist.github.com/justincbagley/95cfaf9601b4af6f3afa93b4d2155abb)

**ลองต่อยอด:** ลองเปิด `package.json` ของ logitrack-web ดู `scripts` ทั้งหมด แล้วไล่ว่าแต่ละตัว (`dev`, `build`, `lint`, `release:mobile:*`) รันคำสั่ง CLI อะไรบ้าง — จะเห็นว่า "ปุ่ม" ในโปรเจกต์จริงๆ ก็คือคำสั่ง terminal ที่ห่อไว้

---

## [2026-09-11] Firestore ↔ Supabase: เลือกยังไงว่า data ไหนอยู่ store ไหน

**Stack:** Firebase/GCP (Firestore, Firebase Auth, firestore.rules) + Supabase Postgres — ผูกกับ ADR 0024

**Concept:** Polyglot persistence + consistency boundary — เกณฑ์ตัดสิน store ต่อชนิดข้อมูล (ไม่ใช่ต่อแอป)

**อธิบาย:**
เวลามีสอง store พร้อมกัน คำถามที่ถูกไม่ใช่ "ย้ายทั้งหมดหรือบางส่วน" แต่คือ "เส้นแบ่งอยู่ตรงไหน และวัดจากอะไร"
เกณฑ์เรียงตามลำดับความแข็ง — ข้อ 1–2 เป็น hard constraint (ฝืนแล้วพัง), ข้อ 3–4 เป็น design, ข้อ 5 เป็น default:

1. **firestore.rules อ่านมันตอน evaluate ไหม** (`get()`/`exists()`) → ถ้าใช่ **ย้ายไม่ได้** เพราะ rules engine อ่านได้แค่ Firestore
   วัดจริงใน repo: `drivers` 25 จุด, `permissions_config` 2, `chats` 2 → ทั้งสามตัวถูกตรึง
2. **แอปคนขับแตะมันบน path ที่ต้องทำงานตอนไม่มีเน็ตไหม** → ถ้าใช่ **ย้ายไม่ได้**
   เพราะ Firestore mobile เปิด offline persistence โดย default (queue write แล้ว sync เอง) แต่ Supabase ไม่มี native offline
3. **มันต้องเปลี่ยนพร้อมกันแบบ atomic กับข้อมูลที่อยู่ SQL แล้วไหม** → ถ้าใช่ ย้ายไป SQL
   หลัก DDD: "Transactions should not cross aggregate boundaries" — ข้าม store = ไม่มี transaction ให้ใช้
4. **SQL ต้อง FK มาหามันไหม** → ถ้าต้อง แต่โดนข้อ 1/2 ตรึงไว้ → ใช้ **bridge id** (เก็บ id เป็น column เฉยๆ ไม่ใช่ FK จริง)
   ADR 0024 ใช้วิธีนี้อยู่แล้วกับ `task_id` และ `firebase_uid`
5. **ไม่เข้าข้อไหนเลย → อย่าย้าย** (default = อยู่ที่เดิม) เพราะทุก store ที่เพิ่มมีต้นทุนถาวร

**กฎเหล็กที่มาคู่กัน:** ข้อมูลหนึ่งชิ้นต้องมี **writer เดียว** — ถ้าเขียนได้จากสอง store เมื่อไหร่คือ dual-write
ซึ่งต้องมี reconciliation + cut-over criteria ตลอดไป (แผน migration เขียนเตือนไว้แล้ว); ถ้าจำเป็นต้องมีสองที่จริง
ให้ replicate ทางเดียว (read-only replica) ไม่ใช่เขียนสองทาง

**ทางเลือกที่พิจารณา / Trade-off:**
- "ย้ายทั้งหมดไป SQL" — ตกไปเพราะข้อ 1 (25 จุดใน rules) + ข้อ 2 (คนขับ offline) + ต้อง rewrite listener 61 จุด
  ถ้าจะดันจริงต้องพ่วง PowerSync/Brick = vendor ที่สาม + ค่าใช้จ่าย + sync layer ที่ต้องดูแลเอง
- "อยู่ Firestore ทั้งหมด" — ตกไปเพราะ billing ต้องการ ACID + JOIN ซึ่งเป็นเหตุผลตั้งต้นของทั้งแผน
- **Cost:** Supabase Free = 500MB, 2 active projects, **หยุดทำงานหลังไม่ใช้ 1 สัปดาห์** → dev พอไหว แต่ prod ต้อง Pro
  ($25/เดือน; org ที่มี 2 project บน Micro ≈ $35/เดือน) — ต้องบวกเข้าไปในต้นทุนที่เดิมจ่าย Firebase อย่างเดียว
- **Security:** เพิ่ม store = เพิ่ม security surface อีกชุด — RLS กลายเป็นของสำคัญเทียบเท่า firestore.rules
  และ service-role key ต้องอยู่ Secret Manager ห้ามหลุดเข้า client bundle เด็ดขาด

**อ้างอิง:**
- [Polyglot Persistence — Martin Fowler](https://martinfowler.com/bliki/PolyglotPersistence.html)
- [DDD_Aggregate — Martin Fowler](https://martinfowler.com/bliki/DDD_Aggregate.html)
- [Enable offline data — Firestore docs](https://firebase.google.com/docs/firestore/manage-data/enable-offline)
- [Using Supabase offline — supabase discussion #357](https://github.com/orgs/supabase/discussions/357)
- [Firebase Auth with Supabase (role claim)](https://supabase.com/docs/guides/auth/third-party/firebase-auth)
- [PostgREST Authentication (role claim → SET ROLE)](https://docs.postgrest.org/en/v12/references/auth.html)

**ลองต่อยอด:** ลองรันเกณฑ์ข้อ 1 กับ collection ที่เหลือเอง —
`grep -oE "(get|exists)\(/databases/\$\(database\)/documents/[a-z_]+/" logitrack-web/firestore.rules | sort | uniq -c`
จะได้รายชื่อที่ "ย้ายไม่ได้" ครบในคำสั่งเดียว

---

## [2026-09-11] เขียน business case ของการ migrate: จุดคุ้มทุน + trigger ที่วัดได้

**Stack:** Firebase/GCP (Firestore billing) + Supabase — ต่อยอดจาก entry ก่อนหน้า

**Concept:** Break-even analysis + trigger threshold — เปลี่ยน "รู้สึกว่าควรย้าย" ให้เป็นตัวเลขที่เฝ้าดูได้

**อธิบาย:**
วิธีตัดสินว่า migration คุ้มไหม ไม่ใช่เทียบ "ราคา DB A vs DB B" แต่ต้องทำ 3 ขั้น:

1. **หาต้นทุนคงที่ของทางเลือกใหม่ก่อน** — Supabase dev+prod = $35/เดือน (Free ใช้กับ prod ไม่ได้เพราะ pause
   หลังไม่มี traffic 1 สัปดาห์) นี่คือ "ราคาที่ต้องจ่ายวันแรก" ไม่ว่าจะได้ประโยชน์หรือไม่
2. **แปลงเป็นจุดคุ้มทุนในหน่วยที่ของเดิมคิดเงิน** — Firestore คิดต่อ operation ไม่ใช่ต่อเดือน
   $35/เดือน ÷ $0.06 ต่อ 100k read = **1.94 ล้าน read/วัน** ถึงจะเสมอตัว
   พอเห็นตัวเลขนี้เทียบกับขนาดงานจริง คำตอบก็ชัดทันทีโดยไม่ต้องเถียงกัน
3. **ถ้าค่าใช้จ่ายไม่ใช่เหตุผล ต้องหา driver ที่แท้จริงให้เจอ** แล้วแปลงเป็นเกณฑ์ที่วัดได้
   ของเราคือ "หนี้ integrity" — วัดจากจำนวน backfill/repair script ที่ต้องเขียนต่อไตรมาส

**บทเรียนที่สำคัญที่สุด:** ต้องกล้าตรวจสอบว่าทางเลือกใหม่แก้ปัญหาได้จริงกี่เปอร์เซ็นต์
ไล่บั๊ก billing ย้อนหลัง 6 เคส พบว่า SQL แก้ได้จริงแค่ 2 เคส (ที่เป็น referential integrity)
อีก 4 เคสเป็น business-logic bug ที่ย้าย DB ไปก็เกิดเหมือนเดิม — ถ้าไม่ตรวจข้อนี้จะย้ายไปแล้วผิดหวัง

**ทางเลือกที่พิจารณา / Trade-off:**
ก่อนย้าย DB ควรหมดทางถูกก่อน: BigQuery export (แก้เรื่องรายงานได้หมด ใช้เวลา 1–3 วัน ไม่แตะ production),
validate ตอนเขียน, เลิก denormalize ที่ไม่จำเป็น — ทั้งสามอย่างถูกกว่า migration 10–16 สัปดาห์มาก

**อ้างอิง:**
- [Firestore billing example (เรตจริง $0.06/100k read)](https://firebase.google.com/docs/firestore/billing-example)
- [Supabase Pricing](https://supabase.com/pricing)
- [Supabase billing docs](https://supabase.com/docs/guides/platform/billing-on-supabase)

**ลองต่อยอด:** เปิด Firebase Console → Usage and billing ของ `logitrack-prod` จดตัวเลข read/write ต่อวันจริง
แล้วเอาไปหารด้วยจุดคุ้มทุนใน `shared-docs/sql-migration-business-case.md` §2 จะรู้ทันทีว่าห่างจุดคุ้มทุนกี่เท่า

---

## [2026-09-11] เปิดระบบให้ TTP + รถร่วม 20+ ราย: ออกแบบ multi-tenancy บน Firestore

**Stack:** Firebase/GCP (Firestore Security Rules, Firebase Auth custom claims) — ผูกกับ ADR 0026

**Concept:** Tenant isolation บน shared database + "Rules are not filters" + แยกตัวตน platform operator ออกจาก tenant

**อธิบาย:**

เวลาจะเปลี่ยนแอปที่ใช้ในบริษัทเดียว ให้รองรับหลายองค์กรที่ไม่ไว้ใจกัน มี 3 บทเรียนที่ต้องรู้ก่อนแตะโค้ด:

**1. Firestore Security Rules ไม่ใช่ filter — มันคือ "ผ่าน/ไม่ผ่าน" ทั้งก้อน**

เอกสารทางการพูดตรงๆ ว่า *"You cannot write a query for all the documents in a collection and expect
Cloud Firestore to return only the documents that the current client has permission to access."*
ถ้า query มีโอกาสคืน doc ที่ rule ปฏิเสธ → **ทั้ง query ล้ม** ไม่ใช่คืนเฉพาะที่อ่านได้

ผลที่ตามมาซึ่งคนมักคาดไม่ถึง: **การ "เพิ่มความปลอดภัย" ไม่ใช่การค่อยๆ เข้ม มันคือ breaking change**
ทุก query ที่ไม่ได้ใส่ `where("tenantId","==",…)` เอง จะพังทันทีที่ rule เข้ม ไม่ใช่เห็นข้อมูลน้อยลง
→ ลำดับ rollout จึงสำคัญเท่ากับตัว design: เขียนฟิลด์ก่อน → backfill → สร้าง index → **ค่อย**เข้ม rules

**2. กุญแจแบ่งข้อมูลต้อง "อยู่บน doc" ห้ามคำนวณตอนอ่าน**

rules ทำ JOIN ใน query ไม่ได้ และ `get()`/`exists()` ใน rules ถูกจำกัด **10 ครั้งต่อ single-document
request** (20 สำหรับ multi-document/transaction) แถม **เสียเงินค่า read แม้ rule จะปฏิเสธ request นั้น**
→ isolation ต้องเป็นการเทียบ "claim ใน token" กับ "field บน doc" เท่านั้น

แต่เหตุผลที่หนักกว่าเรื่องเทคนิคคือเรื่อง **domain**: ถ้าคำนวณเจ้าของเที่ยวจาก `driver.subcontractorId`
ตอนอ่าน วันที่คนขับย้ายสังกัด (ซึ่งเกิดจริงกับ "นายหน้าหารถ") **ประวัติทั้งหมดจะเปลี่ยนเจ้าของย้อนหลัง**
รวมถึงเที่ยวที่รายงานลูกค้าไปแล้ว → ต้อง **freeze ค่าลงบนแถวตอนเขียน** เป็นหลักการเดียวกับ frozen price
ใน ADR 0010 (เก็บ "ค่าที่เป็นจริง ณ เวลานั้น" ไม่ใช่ "ค่าที่คำนวณได้ตอนนี้")

**3. บทเรียนที่ไม่ใช่เทคนิคแต่กระทบโค้ดที่สุด: role เดียวถือสองตัวตน**

`lib/roles.ts:45` มี `admin: "*"` และ `isWebAdmin()` เป็นประตูเกือบทุก rule — แปลว่าระบบไม่มีแนวคิด
"ผู้ดูแลซอฟต์แวร์" แยกจาก "พนักงานบริษัทเรา" เลย มันเป็นคนเดียวกันมาตลอดโดยไม่มีใครสังเกต เพราะจนถึงวันนี้
มันเป็นคนเดียวกันจริงๆ

พอจะขายซอฟต์แวร์ให้คู่ค้า ในขณะที่ตัวเองก็เป็นผู้แข่งขันบนแพลตฟอร์มนั้นด้วย ตัวตนสองอันนี้ขัดกันทันที
และถ้าไม่แยกตอนนี้ ทุกฟีเจอร์ถัดไปจะฝังการรวมร่างลึกลงไปอีก จนวันหนึ่งตอบคำถาม
"คู่แข่งที่เป็นเจ้าของระบบเห็นข้อมูลผมไหม" ไม่ได้ — **หนี้ประเภทนี้แพงขึ้นแบบทบต้น เพราะมันฝังใน rules
ที่ทุกอย่างพึ่งพา ไม่ใช่ในไฟล์เดียวที่ refactor ได้**

มาตรฐานที่ตั้งไว้คือ **auditable access** ไม่ใช่ zero access (เพราะ platform admin ต้อง support ได้จริง)
— platform admin เข้าถึงข้าม tenant ได้ แต่ต้องลง `security_events` ทุกครั้ง

**ทางเลือกที่พิจารณา / Trade-off:**

- **แยก Firebase project ต่อองค์กร** — ตกไปเพราะเที่ยวหนึ่งวิ่งจริงครั้งเดียว แต่ต้องบันทึกสองระบบ
  → reconcile ตลอดชีพ และทำลาย cross-carrier view ซึ่งเป็นเหตุผลเดียวที่ปลายทางอยากใช้
- **GCIP multi-tenancy (Identity Platform tenants)** — ตกไปเพราะมัน isolate **identity เท่านั้น**
  (user account + IdP) ไม่ได้ isolate document ใน Firestore → ไม่ลดงาน rules แม้แต่บรรทัดเดียว
- **ยัด permission ทั้งชุดลง custom claims** — ทำไม่ได้: เพดาน **1000 bytes** และเอกสารระบุชัดว่า claims
  มีไว้ทำ access control ไม่ใช่เก็บ data → เก็บได้แค่ `tenantId` + `role`, รายละเอียดที่เหลือไว้ Firestore
- **Cost:** ไม่ใช่ค่า DB แต่เป็น **composite index ใหม่เกือบทุก query** (`firestore.indexes.json`) เพราะทุก
  query ต้องเพิ่ม `tenantId` เข้า filter — ต้องนับและ deploy index ให้ครบ **ก่อน** เข้ม rules
- **Security:** failure mode ใหม่คือ **tenant orphan** — แถวที่ไม่มี `tenantId` จะไม่มีใครอ่านได้เลย
  (แม้แต่เจ้าของ) ต้องมี validation ตอนเขียน + ตัวตรวจ orphan ก่อนเข้ม rules

**อ้างอิง:**
- [Firestore Security Rules conditions — "rules are not filters" + ลิมิต/ค่าใช้จ่ายของ get()/exists()](https://firebase.google.com/docs/firestore/security/rules-conditions)
- [Firebase Auth custom claims — เพดาน 1000 bytes, ใช้เพื่อ access control เท่านั้น, propagation ตอน token refresh](https://firebase.google.com/docs/auth/admin/custom-claims)
- [Identity Platform multi-tenancy — isolate identity ไม่ใช่ data](https://cloud.google.com/identity-platform/docs/multi-tenancy-authentication)
- [Firebase Security Rules basics](https://firebase.google.com/docs/rules/basics)
- ADR ในโปรเจกต์: `shared-docs/adr/0026-multi-tenant-carrier-isolation.md`

**ลองต่อยอด:** ลองนับงานจริงที่รออยู่ด้วยคำสั่งเดียว — จำนวน query ที่ต้องเติม `tenantId` เข้าไป:
```bash
grep -rn "collection(db, COLLECTIONS.\(TRIP_RECORDS\|TASKS\|DRIVERS\|TRUCKS\)" \
  logitrack-web/app logitrack-web/features logitrack-web/lib | wc -l
```
ตัวเลขที่ได้คือขนาดของ breaking change ในข้อ 1 — เอาไปตั้งเป็น scope ของ spec ได้เลย

---

## [2026-09-12] แยกงาน tenantId ออกจาก working tree ที่มีงาน 3 ชุดปนกัน

**Stack:** Git (ไม่ผูก stack แอป — เป็นทักษะพื้นฐานที่ใช้ได้ทุกโปรเจกต์)

**Concept:** หน่วยของ commit คือ "การเปลี่ยนแปลงเชิงตรรกะ" ไม่ใช่ "ไฟล์" → partial staging ระดับ hunk +
`git stash push --staged` เป็นเครื่องมือที่ตรงกับปัญหา

**อธิบาย:**

สถานการณ์: working tree บน `main` มีงาน **3 ชุดที่ไม่เกี่ยวกันเลย** ปนกันอยู่ (multi-tenant ADR 0026 /
plan-date billing ADR 0027-0028 / job-assign + import dialog + mobile OCR) และ **4 ไฟล์ถือทั้งสองเรื่อง
ในไฟล์เดียว** เช่น `validate/taskSchema.ts` มีทั้ง `billingCustomerId` (0027) และ `tenantId` (0026)

**1. ทำไม `git checkout -b` เฉยๆ แก้ปัญหานี้ไม่ได้**

การสร้าง branch ใหม่ *ไม่ได้ย้าย* อะไรเลย — dirty working tree ไม่ผูกกับ branch มันลอยอยู่เหนือ index
ดังนั้น `git switch -c` แล้ว `git commit -a` จะได้ commit ที่มีงานทั้ง 3 ชุด = ไม่ได้แยกอะไร
ปัญหาจริงคือ **ต้องแยก index ก่อน** แล้วค่อยใช้ branch เป็นที่ยึด

**2. หน่วยของ commit คือ logical change ไม่ใช่ไฟล์ → ต้องแยกระดับ hunk**

Pro Git บอกจุดประสงค์ของ partial staging ตรงๆ ว่า *"you want those changes to be partitioned into
several focused commits rather than one big messy commit... commits are logically separate changesets"*
เครื่องมือปกติคือ `git add -p` (interactive) แต่ใน automation กดเลือกทีละ hunk ไม่ได้ →
ใช้วิธี **สร้าง patch ของ hunk ที่ต้องการ แล้วถอนออกจาก index ด้วย `git apply --cached -R`**
(`--cached` = *"Apply the patch to just the index, without touching the working tree"*)

ลำดับที่ใช้จริง: `git add -A` (stage ทุกอย่าง) → `git restore --staged <ไฟล์ที่เป็น tenant ล้วน>` →
`git apply --cached -R tenant-hunks.patch` (ถอน hunk tenant ออกจาก index ของไฟล์ที่ปนกัน)
ผลคือ **index = งานที่จะอยู่ main, working tree = งาน tenant**

**3. `--staged` คือ flag ที่ตรงกับปัญหานี้ (คนมักใช้ `--keep-index` ผิด)**

- `git stash push --staged` = *"Stash only the changes that are currently staged"* → เอา **เฉพาะที่ stage**
  ไปเก็บ และ **เอาออกจาก working tree** ด้วย เหลือส่วนที่ไม่ stage ไว้ในมือ ← ตรงกับที่ต้องการ
- `git stash push --keep-index` = *"All changes already added to the index are left intact"* → เก็บ
  **ทุกอย่าง** ลง stash แต่คงส่วนที่ stage ไว้ในมือ (ใช้ตอนอยาก test เฉพาะสิ่งที่จะ commit) — **ไม่ใช่**
  สิ่งที่ต้องการตรงนี้ เพราะมันไม่ได้แยกงานสองชุดออกจากกัน

**4. บทเรียนที่สำคัญที่สุด: ต้องพิสูจน์ว่าไม่มีบรรทัดหาย ไม่ใช่ดูแล้วเชื่อ**

การแยกแบบนี้ "หายเงียบ" ได้ง่ายมาก (hunk ผิดตัว / stash pop ไม่สมบูรณ์) จึงต้องมี 2 อย่าง:
- **safety net ก่อนเริ่ม:** `git diff HEAD > backup.patch` + **copy ไฟล์ untracked ออกไปด้วยมือ**
  (ไฟล์ untracked ไม่มีอะไรใน git ปกป้องมันเลย — `git stash`/`checkout` เผลอกลืนได้)
- **การตรวจแบบ mechanical:** เทียบ *multiset ของบรรทัดที่เพิ่ม* ต่อไฟล์ ระหว่าง "ของเดิม" กับ
  "main ใหม่ + branch" ด้วย `sort | md5sum` → ได้ 6/6 MATCH คือหลักฐานว่าไม่มีบรรทัดหายหรือซ้ำ
  (ห้ามใช้ยอด insertions ของ `--stat` ตัดสิน เพราะบรรทัดว่างและไฟล์ใหม่ทำให้ยอดเพี้ยนได้)
- และ `git apply --check tenant.patch` บน tree ใหม่ (*"see if the patch is applicable... detects errors"*)
  พิสูจน์ว่า hunk ถูกถอดออกอย่างสะอาด (context ตรง เอากลับมาแปะได้)

**5. ไฟล์ append-only คือแม่เหล็กดูด conflict — อย่าแยกมัน**

`learning-log.md` และ `shared-docs/adr/README.md` (ตาราง index ของ ADR) เป็นไฟล์ที่ทุกคน/ทุกงาน
**เขียนต่อท้ายจุดเดียวกัน** ถ้าแยกครึ่งไปอยู่อีก branch แล้วทั้งสองฝั่งเขียนต่อ → merge ชนทุกครั้ง
ที่บรรทัดท้าย ทั้งที่เนื้อหาไม่ขัดกันเลย → ตัดสินใจ **เก็บไว้ฝั่ง main ทั้งก้อน** (แถว ADR 0026 ในตาราง
index ก็ยังอยู่ main) แลกกับการที่ branch ไม่มีเอกสารของตัวเอง

**ทางเลือกที่พิจารณา / Trade-off:**

- **commit รวมก่อนแล้วค่อย `rebase -i` / `cherry-pick` แยกทีหลัง** — ได้ผลเหมือนกันแต่ต้อง rewrite history
  และถ้าเผลอ push ไปก่อนจะแก้ยาก; วิธี stash ทำงานกับสิ่งที่ยังไม่ commit จึงไม่แตะ history เลย
- **`git worktree` แยกโฟลเดอร์ต่อ branch** — เหมาะกับงานที่สลับไปมาบ่อย แต่ผู้ใช้ทำงานใน checkout เดียว
  (บันทึกไว้แล้วใน memory) จึงไม่ใช้
- **commit ทุกอย่างแล้ว `revert` ส่วนที่ไม่เอา** — ประวัติสกปรกและ reviewer อ่านไม่รู้เรื่อง
- **cost/risk:** วิธีนี้แพงที่ "เวลาเตรียม patch + ตรวจ" (~10 นาที) แต่ถูกกว่าการที่ branch ปนงานคนอื่น
  แล้ว review/merge พลาด; ความเสี่ยงที่เหลือคือ EOL normalization (CRLF) ทำให้ไฟล์ที่ผ่าน git
  ต่างจากสำเนาเดิมแบบ byte (เนื้อหาเท่ากัน) — ตรวจด้วย `diff <(tr -d '\r' a) <(tr -d '\r' b)`

**อ้างอิง:**
- [git-stash docs — `--staged` / `--keep-index` / `-u`](https://git-scm.com/docs/git-stash)
- [git-apply docs — `--cached`, `--check`, `-R`](https://git-scm.com/docs/git-apply)
- [Pro Git — Interactive Staging (staging patches/hunks)](https://git-scm.com/book/en/v2/Git-Tools-Interactive-Staging)
- [git-restore docs — `--staged` (ถอนจาก index โดยไม่แตะ working tree)](https://git-scm.com/docs/git-restore)

**ลองต่อยอด:** ฝึก `git add -p` แล้วกด `e` เพื่อแก้ hunk ด้วยมือ 1 ครั้ง (ในไฟล์ที่มี 2 เรื่องปนกัน)
จะเข้าใจว่า patch format คือ "ข้อความธรรมดา" ที่แก้ได้ — ทำได้แล้วจะไม่กลัวการแยก commit อีกเลย
