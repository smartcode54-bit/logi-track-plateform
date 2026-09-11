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
