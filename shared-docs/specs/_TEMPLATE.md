# Spec: <ชื่อฟีเจอร์ / Feature name>

> **Status:** 🟡 Draft | 🟢 Approved | 🔵 In progress | ✅ Done
> **Owner:**
> **Created:** <YYYY-MM-DD>
> **Domain:** <feature domain เช่น accounting / tasks / drivers / maintenance>
> **Related:** <spec/PR/commit ที่เกี่ยวข้อง>

---

## 1. Problem & Goal (ทำไมต้องทำ)
<ปัญหาปัจจุบัน + ผลลัพธ์ที่ต้องการ — 2–4 บรรทัด ไม่ใช่วิธีแก้>

## 2. Scope
**In scope:**
-

**Out of scope (ทำทีหลัง / ไม่ทำ):**
-

## 3. Requirements
> ข้อกำหนดที่ทดสอบได้ ใส่เลขเพื่ออ้างอิงใน task/acceptance

**Functional**
- R1.
- R2.

**Non-functional** (perf / security / i18n / cost)
- N1. รองรับ i18n ครบทั้ง `en` และ `th`
- N2.

## 4. Design
> ออกแบบให้สอดคล้อง `.vibe-rules.md` และ feature architecture (`features/<domain>/api` ↔ `components`)

**Data model (Firestore)**
- Collection / field ที่เพิ่ม-แก้:
- denormalized fields (ถ้ามี):

**Cloud Functions / billing**
- ⚠️ ถ้าแตะ billing: แก้ทั้ง `lib/billingCompute.ts` **และ** `functions/src/core/billingCompute.ts` ให้ sync เสมอ
- callable / trigger ที่เกี่ยว:

**Web (Next.js)**
- หน้า/คอมโพเนนต์ใน `app/app/**` + `features/<domain>/`:
- i18n keys (`context/locales/en|th/<ns>.ts`):

**Mobile (Flutter)** (ถ้ามี)
- หน้า/repository:
- bump `pubspec.yaml` version:

**Firestore Rules** (ถ้ามี collection ใหม่/สิทธิ์เปลี่ยน)
-

## 5. Affected files
<ลิสต์ไฟล์หลักที่จะแตะ — ช่วยจำกัด scope>

## 6. Task breakdown
- [ ] T1.
- [ ] T2.
- [ ] T3. อัปเดต i18n en + th
- [ ] T4. อัปเดต `.vibe-rules.md` Change Log (ตามกฎโปรเจกต์)

## 7. Acceptance criteria (ตรวจรับ)
> ข้อความที่พิสูจน์ได้ว่าทำเสร็จจริง map กับ R1/R2…
- [ ] AC1. (R1) …
- [ ] AC2. (R2) …
- [ ] AC3. `tsc --noEmit` / `dart analyze` ผ่าน, CI เขียว

## 8. Risks & rollback
| Risk | Mitigation / rollback |
|------|----------------------|
| | |

## 9. Open questions / follow-ups
-
