# Spec: Per-trip P&L — กำไรส่วนเกินต่อเที่ยว + P&L รายเดือน (contribution model)

> **Status:** 🔵 In progress — code complete 2026-11-16 incl. HTML what-if export, พรบ./ภาษีรถ/custom categories and year view (tsc / eslint 0 errors / vitest 431 pass), not committed or deployed; AC3 + AC12–AC14 wait for deploy to dev. (Approved 2026-11-16; same day the owner revised OQ1 → multi-drop = round trip to the farthest stop, OQ2 → standby = previous route × 2; see §9)
> **Owner:** Samart Kas
> **Created:** 2026-11-16
> **Domain:** accounting (หน้าใหม่ Accounting → P&L + ตั้งค่า P&L)
> **Related:** [ADR 0031](../adr/0031-per-trip-pnl-contribution-model.md) (หลัก), ADR 0009 (append-only effective-dated), ADR 0012 (helper-day window), ADR 0015, ADR 0027/0028 (แกนวันที่), ADR 0003/0010 (fail loud), ADR 0004 (`onInvalid` กลาง), ADR 0026 (tenant ในอนาคต)
> **Glossary:** Trip contribution, Job distance, Off-plan km, Truck fuel cost per km, Maintenance variance, Fixed cost, P&L month, Helper-day (`shared-docs/glossary.md`)

---

## 1. Problem & Goal (ทำไมต้องทำ)

วันนี้ P&L ทำมือทุกครั้ง (รายงานต้นทุน ต.ค. 2569, P&L แผน CJSF พ.ย.) จากสคริปต์และตัวเลขที่เจ้าของส่งให้ ระบบไม่บอกว่าเที่ยวไหน/เส้นไหน/รถคันไหนได้เงินจริงเท่าไร และไม่เห็นรอยรั่วใหญ่ 2 จุด (น้ำมันระยะเกินใบงาน, ค่าซ่อมเกิน accrual)

**เป้าหมาย:** หน้า P&L ที่คำนวณสด ๆ ในเบราว์เซอร์จากข้อมูลที่ระบบเก็บอยู่แล้ว แสดง **กำไรส่วนเกินต่อเที่ยว** (ไม่ปันต้นทุนคงที่) รายเที่ยว / รายเส้น / รายรถ และ **P&L รายเดือน** ที่มีบรรทัดระยะเกินใบงาน, ส่วนต่างค่าซ่อม, ต้นทุนคงที่ และจำนวนเที่ยวคุ้มทุน — ข้อมูลใหม่มีแค่ "ตั้งค่า P&L"

## 2. Scope

**In scope:**
- ตั้งค่า P&L แบบ effective-dated + append-only: ค่าคนขับต่อเที่ยว, ค่าผู้ช่วยต่อ helper-day, ค่าซ่อม ฿/กม. (บำรุงรักษา + ยาง), รายการต้นทุนคงที่ (ต่อรถ/ต่อบริษัท, รายเดือน/รายปี, มีผลจาก–ถึง)
- Firestore rules ของ 2 collection ใหม่ + capability ใหม่ 2 ตัว (Admin เป็นค่าเริ่มต้น)
- pure function `logitrack-web/lib/pnlCompute.ts` + `lib/pnlCompute.test.ts`
- หน้า `/app/accounting/pnl` 4 มุมมอง: รายเที่ยว, รายเส้น, รายรถ, P&L รายเดือน (+ จำนวนเที่ยวคุ้มทุน) + แผงคำเตือน fail-loud + Export ไฟล์ HTML ปรับอัตราได้ (R21)
- หน้า `/app/accounting/pnl/settings`
- i18n en + th ครบ

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- รถร่วม (subcontractor) — แสดงแค่จำนวนเที่ยว + รายได้ (ADR 0031 §4)
- เก็บผลลัพธ์ / ปิดงวด / month close — ไม่มี (ADR 0031 §9); ไฟล์ HTML ที่ export คือสำเนาแช่แข็ง (ปรับได้แค่อัตราในไฟล์)
- Cloud Function, trigger, mobile — ไม่แตะ
- ระยะทางระหว่างจุดส่ง (stop↔stop) สำหรับ multi-drop — ไม่มีในตารางและไม่ทำ; ใช้ระยะไป-กลับของจุดไกลสุดแทน (R8, owner 2026-11-16)
- เทรนด์หลายเดือน / กราฟ / เปรียบเทียบเดือน — ทีหลัง
- แก้ค่าคนขับใน `driver_compensation_config` ให้ตรงกับ P&L — แค่แสดงเตือน (§4.6)
- BigQuery / SQL — ไม่ทำ (ADR 0031 follow-up)

## 3. Requirements

> ตัวเลขเงินทั้งหมดเป็นบาท ปัดทีละองค์ประกอบเป็น 2 ตำแหน่ง (สตางค์) ก่อนรวม ยอดรวมทุกตาราง = ผลรวมของแถวที่ปัดแล้ว (ตารางบวกกันลงตัว)
> "วันที่" ทั้งหมดคิดตามปฏิทินกรุงเทพ (UTC+7) ด้วย `bangkokDateKey` (`lib/compensationCompute.ts:29`)

**Functional — ตั้งค่า**
- R1. **อัตราต่อหน่วย** เก็บใน collection ใหม่ `pnl_rates` แถวละ 1 อัตรา: `kind` ∈ `driver_trip` (฿/เที่ยว), `helper_day` (฿/helper-day), `maint_service_km` (฿/กม.), `maint_tyre_km` (฿/กม.); `amountThb ≥ 0`; `effectiveFrom` = สตริง `yyyy-MM-dd` (ไม่ใช่ Timestamp — กัน bug UTC-midnight ของรอบน้ำมันเดิม). อัตราที่ใช้กับวัน `d` = แถวที่ไม่ void ของ kind นั้น ที่ `effectiveFrom ≤ d` และ `effectiveFrom` มากที่สุด (เสมอกัน → `createdAt` ล่าสุด)
- R2. **ต้นทุนคงที่** เก็บใน collection ใหม่ `pnl_fixed_costs` แถวละ 1 **version** ของ 1 รายการ (`itemId` คงที่ทุก version): `name`, `category`, `scope` (`truck`|`company`), `truckId` (บังคับเมื่อ scope = truck), `frequency` (`monthly`|`yearly`), `amountThb ≥ 0`, `effectiveFromMonth` (`yyyy-MM`), `effectiveToMonth?` (`yyyy-MM` รวมเดือนนั้น, ว่าง = ไม่มีกำหนด), `isStop?`. ยอดของรายการในเดือน `M` = version ที่ไม่ void และ `effectiveFromMonth ≤ M` ล่าสุด → ถ้าไม่มี หรือ `isStop` หรือ `M > effectiveToMonth` = 0; ไม่งั้น `monthly` = `amountThb`, `yearly` = `amountThb / 12`
- R2b. **(owner 2026-11-16) หมวดเพิ่ม:** `cmi` (พรบ.) และ `vehicle_tax` (ภาษีรถ) — บังคับผูกรถ (scope truck); **รายการ `vehicle_tax` ของรถคันใดในเดือนนั้น ใช้แทนภาษีจากประวัติต่ออายุของคันนั้น (ไม่นับซ้ำ, ไม่เตือน F14)** และแสดงในบรรทัด "ภาษีรถ" ไม่ใช่บรรทัดต้นทุนคงที่; `custom` = หมวดตั้งชื่อเอง (`categoryLabel` บังคับ) — งบเดือน/ปี/HTML แสดงบรรทัดละชื่อหมวด (จับกลุ่มชื่อแบบไม่สนตัวพิมพ์). ตารางตั้งค่าแสดงรายการละแถวเดียว ลูกศรประวัติ version ขึ้นเฉพาะรายการที่มี ≥ 2 version, ยกเลิก (void) อยู่ในเมนู ⋯
- R3. **ทั้งสอง collection เป็น append-only:** แก้ = เพิ่มแถว/version ใหม่ที่มีผลจากวัน/เดือนใหม่ (เดือนก่อนหน้าได้ตัวเลขเดิม); หยุดรายการ = เพิ่ม version `isStop: true` ที่มีผลจากเดือนนั้น; แก้ข้อมูลผิด = **void** แถวนั้นพร้อมเหตุผล (เปลี่ยนตัวเลขย้อนหลังโดยตั้งใจ). ลบ/แก้ในที่เดิมไม่ได้ — บังคับที่ Firestore rules ไม่ใช่แค่ UI
- R4. หน้าตั้งค่าแสดง: ค่าปัจจุบัน + วันมีผล ของทั้ง 4 อัตรา, ประวัติทุกแถว (รวมที่ void), ปุ่มเพิ่มอัตรา / void; ตารางต้นทุนคงที่ (ยอดเทียบต่อเดือน, ช่วงมีผล, สถานะ), ปุ่มเพิ่มรายการ / เปลี่ยนยอดตั้งแต่เดือน / หยุดตั้งแต่เดือน / void version; และกล่องอ่านอย่างเดียวแสดงค่าคนขับใน payroll config (`weekdayRateThb` / `holidayRateThb` / `helperDayRateThb` ของ `driver_compensation_config` ที่มีผลวันนี้) ข้างค่า P&L พร้อมข้อความว่าต่างกันโดยตั้งใจ (ADR 0031 Risks)

**Functional — แหล่งข้อมูลและการจัดเดือน**
- R5. **เที่ยวที่นับ** = `trip_records` ที่ `status == "delivered"` บนรถของเรา (`trucks.ownershipType != "subcontractor"` และไม่มี `subcontractorId`) — `trip_records.status == "standby"` ไม่นับ (นับจาก `standby_records` แทน กันนับซ้ำ). **เดือน P&L** ของเที่ยว = `bangkokDateKey(task.checkInAt)` → ไม่มีใช้ `trip.deliveredTimestamp` (ห้ามใช้ `billingDate`)
- R6. **Standby ที่นับ** = `standby_records` ที่ `status == "completed"` บนรถของเรา; วันที่ = `startedAt` → `endedAt` → `createdAt`; นับเป็น 1 เที่ยว
- R7. **รายได้** = `trip_records.billingEstimateThb` / `standby_records.billingEstimateThb` (คิดที่ server แล้ว — หน้านี้ห้ามคิดราคาเอง และห้ามเขียน `billing*`)
- R8. **ระยะตามใบงาน (job distance)**:
  - เที่ยวเดี่ยว: `leg(O, D) + leg(D, O)` โดย O = `trip.billingLookupHubId` (ไม่มี → `extractHubId(task.sourceHub)`), D = `trip.billingLookupDestination` (ไม่มี → `normalizeDestinationCode(task.destination)`)
  - multi-drop: **ระยะไป-กลับของจุดที่ไกลที่สุด** = `max over i of (leg(O, Si) + leg(Si, O))` โดย Si = `normalizeDestinationCode(stop.destination)` ของทุกจุดใน `task.deliveryStops` (ไม่ขึ้นกับลำดับจุด). ตารางไม่มีระยะจุด→จุด และเจ้าของถือว่าจุดไกลสุดพอแล้ว; กม. อ้อมระหว่างจุดไปตกที่ระยะเกินใบงาน. จุดใดขาดคู่ไป/กลับ → F2 (ไม่รู้ว่าจุดนั้นไกลสุดหรือไม่ จึงไม่เดา)
  - `leg(a, b)` = `hub_soc_distances` ที่ `hubId=a, socId=b` → ไม่มีใช้ `soc_hub_distances` ที่ `socId=a, hubId=b`; key เทียบแบบตัวพิมพ์ใหญ่จาก **field ในเอกสาร** (ไม่ใช่ doc id — `hubId` เก็บตาม `source_id` ดิบ)
  - Standby: ยังไม่เกิดเที่ยวงานใหม่ จึงใช้ **ระยะของ route ก่อนหน้า × 2** (รวมขากลับ yard). route ก่อนหน้า = เที่ยว delivered ล่าสุดบน **รถคันเดียวกัน** ที่เวลาจริง (`checkInAt` → `deliveredTimestamp`) ก่อน `standby.startedAt` ย้อนได้ไม่เกิน 3 วัน (= buffer ของ query เที่ยว) — นับได้ทั้งเที่ยวที่นับและไม่นับยอด ขอแค่ resolve route ได้. ระยะ route = ระยะขาเดียว `leg(O, D)` (multi-drop = `max leg(O, Si)` ของจุดไกลสุด) ⇒ standby km = `2 × leg ขาเดียว`
    - ไม่มีเที่ยวก่อนหน้าในช่วงนั้น → ใช้ route ของงานที่ standby ผูกอยู่ (`standby.taskId` → `task.sourceHub` → `task.destination`) ขาเดียว × 2 + badge F18 `standbyDistanceFromTask`
    - ไม่มีทั้งสองอย่าง หรือหา leg ไม่เจอ → F2
  - leg ใดหาไม่เจอ → เที่ยวไม่มี job distance → fail loud (R20)
- R9. **ค่าน้ำมันต่อ กม. ของรถ (fill-to-fill)** ต่อรถ T เดือน M:
  - ใช้เฉพาะ `vehicle_expenses` `type == "fuel"`, `status == "APPROVED"` ของรถ T (จับคู่ `truckId` → ไม่มีใช้ทะเบียน normalize ตามกฎ `fuelTruckKey` ใน `app/app/accounting/fuel/page.tsx:72-77`)
  - จุดอ่านเลขไมล์ = แถวที่มี `odometer` เรียงตามเวลา; แถวที่เลขไมล์ต่ำกว่าค่าสูงสุดก่อนหน้า (ย้อนกลับ) ถูกทิ้ง + เตือน
  - A (anchor) = จุดอ่านล่าสุด **ก่อน** ต้นเดือน (มองย้อน 60 วัน); ไม่มี → A = จุดอ่านแรกในเดือน (+ เตือน "ไม่มีจุดตั้งต้นก่อนเดือน")
  - E = จุดอ่านล่าสุดในเดือน
  - `odoKm = E.odometer − A.odometer`; `windowSpend` = Σ `amount` ของแถวน้ำมัน APPROVED ของรถ T ที่ `A.date < date ≤ E.date` (รวมแถวที่ไม่มีเลขไมล์); `windowLiters` = Σ `volumeLiters` แบบเดียวกัน
  - **ใช้ได้** เมื่อมี A และ E, `odoKm > 0` และ (ถ้ามีลิตรครบ) `odoKm / windowLiters` อยู่ในช่วง 2–25 กม./ลิตร (`KMPL_MIN/MAX` เดิมของหน้า fuel)
  - `rate_T = windowSpend / odoKm`; ใช้ไม่ได้ → ใช้ **ค่าเฉลี่ยฟลีต** = Σ windowSpend ÷ Σ odoKm ของรถที่ใช้ได้ในเดือนนั้น + เตือน (ADR 0031 §10)
- R10. **ค่าคนขับต่อเที่ยว** = อัตรา `driver_trip` ณ วันที่ของเที่ยว — วันธรรมดา/วันหยุดเท่ากัน, standby ได้อัตราเดียวกัน, multi-drop = 1 เที่ยว
- R11. **ส่วนแบ่งค่าผู้ช่วย** ต่อผู้ช่วย h ใน `task.helperDriverIds`:
  - window key = `helperWindowKey(task.checkInAt ?? เที่ยงวัน task.date)` (`lib/compensationCompute.ts:155`, เหมือน `taskWindowKey` ใน `functions/src/driverCompensation.ts:190-195`)
  - helper-day ที่ **ไม่นับ** = window ที่ h ขับงานของตัวเอง (กฎไม่จ่ายซ้ำของ payroll, `eligibleHelperDayKeys` `lib/compensationCompute.ts:166`) — เทียบ `task.driverId` กับทุก key ของ h (`driverIdentityKeys`)
  - helper-day ที่นับ แบ่ง **เท่ากัน** ให้เที่ยว + standby บนรถของเราที่ h อยู่ใน `helperDriverIds` และอยู่ใน window เดียวกัน: ส่วนแบ่ง = อัตรา `helper_day` ณ วัน D ÷ จำนวนเที่ยวนั้น. ส่วนแบ่งตามเที่ยวไปเดือนของเที่ยว
  - helper-day ที่นับแต่ไม่มีเที่ยวบนรถของเราเลย → บรรทัดรายเดือน "ค่าผู้ช่วยที่ไม่มีเที่ยว" (เดือนตาม window key D)
- R12. **ค่าซ่อม accrual ต่อเที่ยว** = job distance × (อัตรา `maint_service_km` + `maint_tyre_km` ณ วันที่ของเที่ยว). CM ไม่เคย accrue
- R13. **ค่าซ่อมจริงรายเดือน** = Σ `maintenanceDisplayCost(record)` (`features/maintenance/utils/maintenanceDisplayCost.ts`) ของ `maintenance` ที่ `status == "completed"` บนรถของเรา, **ลงเดือนตามวันที่ซ่อมเสร็จ**: `endDate` (`yyyy-MM-dd`) → ไม่มีใช้ `checkOutAt` → ไม่มีใช้ `startDate` (+ badge "ประมาณวันบิล"); `paymentMethod == "insurance_claim"` ไม่นับเป็นต้นทุน (แสดงเป็นข้อมูล). เหตุผล §4.3
- R14. **กำไรส่วนเกินต่อเที่ยว** = รายได้ − (job distance × `rate_T`) − ค่าคนขับ − ส่วนแบ่งผู้ช่วย − ค่าซ่อม accrual. ไม่มีส่วนแบ่งต้นทุนคงที่เด็ดขาด
- R15. **ประกัน + ภาษีรถ** จาก `transactions` (`type` ∈ `insurance`|`tax`, `truckId`, `amount`, `date` `yyyy-MM-dd` — `RenewalForm.tsx:177-189`):
  - ช่วงของการต่ออายุครั้งที่ i (รถเดียวกัน ประเภทเดียวกัน) = เดือนของ `date_i` ถึงก่อนเดือนของครั้งถัดไป **หรือ** 12 เดือน แล้วแต่อันไหนสั้นกว่า; ยอดต่อเดือน = `amount_i ÷ จำนวนเดือนในช่วง` (รับรู้ครบทุกบาท ไม่ทิ้งเศษเมื่อต่อเร็ว)
  - เดือนที่ไม่มีช่วงไหนครอบ (รถที่อยู่ในฟลีตเดือนนั้น) → fallback `trucks.insurancePremium` / `trucks.taxExpense` ÷ 12 + เตือน; truck doc ไม่มีค่า → 0 + เตือน
  - "รถที่อยู่ในฟลีตเดือนนั้น" = รถของเราที่มีเที่ยว/standby ในเดือน, หรือมีช่วงประกัน/ภาษีครอบ, หรือ `truckStatus` ∈ `active`/`maintenance`/`insurance-claim`
- R16. **P&L รายเดือน** (โครงตาม §4.4): รายได้ → ต้นทุนผันแปรต่อเที่ยว → กำไรส่วนเกินรวม → บรรทัดรายเดือน (น้ำมันระยะเกินใบงานรายรถ, ส่วนต่างกระทบยอดน้ำมัน, ค่าผู้ช่วยที่ไม่มีเที่ยว, ค่าผ่านทาง/ค่าใช้จ่ายรถอื่น, ส่วนต่างค่าซ่อม) → ต้นทุนคงที่ → กำไรจากการดำเนินงาน
  - น้ำมันรวมในงบ = เงินน้ำมันที่จ่ายจริงในเดือน (เที่ยว + ระยะเกิน + กระทบยอด)
  - ค่าซ่อมรวมในงบ = บิลจริงในเดือน (accrual + ส่วนต่าง)
- R17. **เที่ยวคุ้มทุน** = `ceil(ต้นทุนคงที่ของเดือน ÷ กำไรส่วนเกินเฉลี่ยต่อเที่ยว)` (ตาม ADR 0031 §1) + แสดงตัวเลขที่สอง "รวมบรรทัดรายเดือน" = `ceil((ต้นทุนคงที่ + บรรทัดรายเดือน) ÷ เฉลี่ย)`; เฉลี่ย ≤ 0 → แสดง "ไม่ถึงจุดคุ้มทุน"; แสดงจำนวนเที่ยวจริงเทียบด้วย
- R18. **มุมมอง** (แท็บในหน้าเดียว เลือกปี/เดือน, ค่าเริ่มต้น = เดือนปัจจุบัน):
  - รายเที่ยว: วันที่จริง, ทะเบียน, คนขับ (`driverDisplayName`), ลูกค้า, เส้นทาง (ชื่อ ผ่าน `buildHubDisplayResolver` ของ billing — export ออกมาใช้ร่วม), หลัก/เสริม (`trip.jobCategory` → `task.jobCategory`, ADR 0010), job km, รายได้, น้ำมัน, คนขับ, ผู้ช่วย, ค่าซ่อม, กำไรส่วนเกิน, % margin, badge; filter รถ/ลูกค้า/เส้น/สถานะ(นับ/ไม่นับ); แบ่งหน้า
  - รายเส้น: key = ลูกค้า + ต้นทาง + ปลายทาง (multi-drop = จุดเรียงต่อกัน) + ประเภทรถ; จำนวนเที่ยว (หลัก/เสริม), job km/เที่ยว, รายได้เฉลี่ย, กำไรส่วนเกินเฉลี่ย, รวม, % margin; เรียงตามกำไรเฉลี่ย
  - รายรถ: เที่ยว, รายได้, job km, odo km, % ระยะเกิน, ฿/กม. (badge ถ้าใช้ค่าเฉลี่ยฟลีต), น้ำมันเที่ยว, น้ำมันระยะเกิน, accrual vs บิลจริง (PM/CM), กำไรส่วนเกิน, ต้นทุนคงที่ของรถ (ประกัน/ภาษี/รายการ scope truck), กำไรหลังต้นทุนคงที่ของรถ
  - **รายปี (owner 2026-11-16):** แถว = บรรทัดงบ (เหมือนรายเดือน + เที่ยวที่นับ / เที่ยวคุ้มทุน / รายการที่ไม่นับ), คอลัมน์ = ม.ค. → เดือนล่าสุด (ปีก่อน = ถึง ธ.ค.) + รวมทั้งปี; โหลดข้อมูลทั้งช่วงครั้งเดียวแล้ว `computePnl` ทีละเดือน (`lib/pnl/year.ts`) — ตัดแถวค่าใช้จ่ายรถต่อเดือนให้ fuel window ไม่เห็นเดือนถัดไป; โหลดเฉพาะเมื่อเปิดแท็บ
  - P&L รายเดือน: งบตาม R16 + เที่ยวคุ้มทุน (R17) + ตารางย่อยระยะเกินรายรถ + ตารางย่อยส่วนต่างค่าซ่อมรายรถ + สรุปรถร่วม (จำนวนเที่ยว + รายได้เท่านั้น)
- R19. **ป้ายเตือนบนหน้า**: (ก) เดือนที่ยังไม่จบ = "เดือนยังไม่ปิด ตัวเลขยังเปลี่ยน"; (ข) ถ้ามีเที่ยวของลูกค้า `billingDateBasis == "plan"` = "รายได้ของลูกค้าที่วางบิลตามวันแผน (เช่น CJSF) อาจต่างจากเอกสารวางบิลช่วงปลายเดือน" (ADR 0031 §7); (ค) คำนวณสด — ไฟล์ที่ export ไปก่อนอาจไม่ตรงหลังแก้ข้อมูลต้นทาง
- R20. **Fail loud** — ตารางเต็มที่ §4.5: เที่ยวที่คำนวณไม่ได้ **แสดงในรายการพร้อม badge, ไม่รวมในยอด, และนับจำนวน + รายได้ที่ตกหล่น** ในแผงคำเตือนด้านบน; ไม่มีกรณีไหนกลืนเงียบ
- ~~R21. Export Excel~~ → **R21 (แก้ 2026-11-16, owner): Export เป็นไฟล์ HTML ไฟล์เดียว ที่เปิดแล้วปรับอัตราเองได้ (what-if) — แทนที่ Excel**
  - ไฟล์เดียวจบ ไม่ต้องต่อเน็ต (CSS/JS/ข้อมูลฝังในไฟล์) ชื่อ `PnL_<yyyy-MM>_<exportedAt>.html` ภาษาตาม UI ตอน export
  - ข้อมูลแช่แข็ง ณ ตอน export; **ปรับได้แค่ 4 อัตรา** (ค่าคนขับ/เที่ยว, ผู้ช่วย/helper-day, บำรุง ฿/กม., ยาง ฿/กม.) ใช้ค่าเดียวทั้งเดือน — ไม่แก้ = ใช้อัตราตามจริงรายเที่ยว (ตัวเลขตรงกับหน้าเป๊ะ); ปุ่ม "คืนค่าตามจริง"
  - คำนวณใหม่ทันทีในไฟล์: กำไรส่วนเกินรายเที่ยว, รายเส้น, รายรถ, งบเดือน (ค่าผู้ช่วยที่ไม่มีเที่ยว + ส่วนต่างค่าซ่อมขยับตาม), เที่ยวคุ้มทุน. น้ำมัน รายได้ ต้นทุนคงที่ และ**รายการที่ไม่นับ คงเดิม** (ไม่กลายเป็นนับเพราะใส่อัตรา)
  - **ไม่เขียนกลับระบบ** — เป็นสนามลองตัวเลขเท่านั้น
  - สูตรปรับอัตรามาจากฟังก์ชันเดียว `recomputePnlScenario` (`lib/pnl/scenario.ts`, self-contained, ฝัง source ลงไฟล์) — test บังคับว่าไม่แก้อัตรา = ผล `computePnl` เดิมทุกยอด
  - ข้อความทุกตัว escape ก่อนลง HTML และ JSON ฝังแบบ `<` (กัน `</script>` จากชื่อในข้อมูล)
  - มีแผงคำเตือน (F1–F18) และรายการตั้งค่าที่ใช้ (อัตรา + ต้นทุนคงที่ที่ active) แบบเดียวกับหน้า

**Non-functional**
- N1. i18n ครบทั้ง `en` และ `th` — ไฟล์ใหม่ `context/locales/{en,th}/pnl.ts` (namespace `pnl.*`) + `nav.pnl` + capability keys ใน `securityCenter.ts`
- N2. ไม่มี Cloud Function, trigger, ผลลัพธ์ที่เก็บ, mobile change; ไม่แตะ `billingCompute.ts` ทั้งสองไฟล์ (ไม่ต้อง sync)
- N3. โหลด 1 เดือน ≤ ~5,000 reads (ประมาณการ: trips ~350, tasks ~450, standby ~60, น้ำมัน ~200, maintenance/transactions/trucks ทั้ง collection < 1,000, distances 2 collection ~2,000) และ `computePnl` < 1 วินาทีสำหรับ 500 เที่ยว; ไม่ต้องเพิ่ม composite index
- N4. Security: อ่าน/เขียนตั้งค่า = `isWebAdmin()`; ลูกค้า/พาร์ทเนอร์ไม่เห็นหน้า/เมนู (ADR 0031 §10)
- N5. `computePnl` เป็น pure (ไม่มี Firestore, ไม่มี `Date.now()` ภายใน — รับ `now` เป็น input) ทดสอบด้วย Vitest
- N6. collection ใช้ `COLLECTIONS.*`; ฟอร์มใช้ Zod + `onInvalid` กลาง (ADR 0004); ไฟล์ < ~300 บรรทัด (แยก `lib/pnl/*.ts` ได้ re-export จาก `lib/pnlCompute.ts`)

## 4. Design

> ทำตาม `.vibe-rules.md` + feature architecture: `features/pnl/api` (Firestore) ↔ `features/pnl/components` (UI) ↔ `lib/pnlCompute.ts` (pure)

### 4.1 แหล่งข้อมูล (grounded)

| ข้อมูล | Collection / field | อ้างอิงโค้ด | วิธี query (เดือน M กรุงเทพ `[start, end)`) |
|---|---|---|---|
| เที่ยว | `trip_records`: `status`, `taskId`, `truckId`, `billingEstimateThb`, `billingCustomerId`, `billingLookupHubId`, `billingLookupDestination`, `billingIsMultiDelivery`, `deliveredTimestamp`, `driverId`, `jobCategory` | `functions/src/tripBillingOnDelivered.ts:545-560,651-652` | `createdAt` ∈ `[start − 3 วัน, end + 3 วัน)` (แกน query เดิม — ADR 0001 Update: trip ไม่มี `checkInAt`), กรอง status ในหน่วยความจำ, จัดเดือนด้วย R5; `getDocsFromServer` (ราคา recompute ล่าสุด ตาม Change Log #34) |
| งาน | `tasks`: `checkInAt`, `date`, `sourceHub`, `destination`, `deliveryStops[]`, `helperDriverIds`, `driverId`, `truckId`, `truckType`, `jobCategory` | `validate/taskSchema.ts:40-131` | `date` ∈ `[start − 10 วัน, end + 3 วัน)` index ทั้งด้วย doc id และ field `taskId` (เหมือน join ของ Driver Monitor); เที่ยวที่ยังหา task ไม่เจอ → ดึงเพิ่มด้วย `documentId() in` แล้ว `taskId in` (ชุดละ 30) |
| Standby | `standby_records`: `status`, `truckId`, `taskId`, `startedAt`, `endedAt`, `billingEstimateThb`, `customerId` | `logitrack-mobile/.../standby_repository.dart:182-209`, `functions/src/standbyBilling.ts:120-130` | `endedAt` ∈ `[start − 1 วัน, end + 1 วัน)`; จัดเดือนด้วย R6 |
| ระยะทาง | `hub_soc_distances` / `soc_hub_distances`: `hubId`, `socId`, `distanceKm` | `functions/src/distances.ts:315-325,363`, doc id `functions/src/core/distances.ts:31-37`, SOC key `lib/hubSocDistances.ts:96-104` | โหลดทั้ง 2 collection ครั้งเดียวต่อ session หน้า, map ด้วย `UPPER(hubId)_UPPER(socId)` จาก field |
| รถ | `trucks`: `ownershipType`, `subcontractorId`, `licensePlate`, `truckStatus`, `insurancePremium`, `taxExpense` | `validate/truckSchema.ts:26-27,41-46,89,100` | ทั้ง collection |
| น้ำมัน / ค่าใช้จ่ายรถ | `vehicle_expenses`: `type`, `status`, `truckId`, `truckLicensePlate`, `date`, `amount`, `volumeLiters`, `odometer`, `category` | `features/accounting/api/expenses.ts:25-54` | `date` ∈ `[start − 60 วัน, end)`; type/status กรองในหน่วยความจำ (ห้ามใช้ `getVehicleExpensesByType` ที่โหลดทั้ง collection) |
| ซ่อมบำรุง | `maintenance`: `truckId`, `type` (PM/CM), `status`, `endDate`, `checkOutAt`, `startDate`, `totalCost`/`costLabor`/`costParts`/`invoiceAmount`, `paymentMethod`, `driverSubmitted` | `validate/maintenanceSchema.ts:26-66`, mobile `maintenance_repository.dart:86-114` | ทั้ง collection (เล็ก — `getMaintenanceOverview` ก็โหลดทั้งหมดอยู่แล้ว) |
| ประกัน/ภาษี | `transactions`: `truckId`, `type`, `amount`, `date` | `features/trucks/components/renew/RenewalForm.tsx:177-189`, `features/trucks/services/truckService.ts:104-109` | ทั้ง collection, กรอง `type ∈ {insurance, tax}` |
| ผู้ช่วย identity | `drivers`: `authId`, `authIdHistory` | `lib/driverName.ts` `driverIdentityKeys` | ทั้ง collection (ใช้ชื่อด้วย) |
| ลูกค้า (ป้ายวันแผน) | `customers.billingDateBasis` | `validate/customerSchema.ts:52` | ทั้ง collection |
| ตั้งค่า P&L | `pnl_rates`, `pnl_fixed_costs` (ใหม่) | §4.2 | ทั้ง collection (เล็ก) |
| payroll config (เตือน) | `driver_compensation_config` | `features/driver-compensation/api/config.ts`, `validate/compensationConfigSchema.ts:53-62` | config ที่มีผลวันนี้ — หน้าตั้งค่าเท่านั้น |

ข้อสังเกตจากโค้ดที่ส่งผลต่อ design:
- **ไม่มีประวัติเลขไมล์อื่น** — Cartrack เขียนทับ `vehicle_locations/{gpsId}.odometerKm` ค่าปัจจุบันค่าเดียว (`functions/src/cartrack.ts:185`) → เลขไมล์รายเดือนมาจากใบเสร็จน้ำมันเท่านั้น
- `transactions.date` มาจาก `new Date().toISOString().split('T')[0]` (`RenewalForm.tsx:183`) = **วันที่ UTC** ตอนกด completed (ต่ออายุช่วง 00:00–06:59 น. ได้วันก่อนหน้า) — ผลต่อการกระจาย 12 เดือนแค่ขอบวัน ไม่แก้ใน spec นี้ (§9 FU)
- `transactions.amount = parseFloat(expense || "0")` → อาจเป็น 0 ถ้าไม่กรอก → เตือน (F15)

### 4.2 Data model (Firestore) — collection ใหม่ 2 ตัว

```ts
// COLLECTIONS.PNL_RATES = "pnl_rates"
interface PnlRateRow {
  kind: "driver_trip" | "helper_day" | "maint_service_km" | "maint_tyre_km";
  amountThb: number;            // >= 0 — ฿/เที่ยว, ฿/helper-day, ฿/กม.
  effectiveFrom: string;        // "yyyy-MM-dd" (Bangkok calendar date)
  note?: string;
  createdAt: Timestamp; createdBy: string; createdByName?: string;
  voided: boolean;              // false ตอนสร้าง
  voidedAt?: Timestamp; voidedBy?: string; voidedReason?: string; updatedAt?: Timestamp;
}

// COLLECTIONS.PNL_FIXED_COSTS = "pnl_fixed_costs"   (1 doc = 1 version)
interface PnlFixedCostVersion {
  itemId: string;               // คงที่ทุก version ของรายการ (uuid ตอนสร้างรายการ)
  name: string;                 // เช่น "ค่างวดรถ 70-1234"
  category: "installment" | "gps" | "accounting" | "audit" | "sso" | "bonus" | "battery" | "other";
  scope: "truck" | "company";
  truckId?: string;             // บังคับเมื่อ scope = "truck"
  truckPlate?: string;          // snapshot สำหรับแสดงผล
  frequency: "monthly" | "yearly";
  amountThb: number;            // >= 0 — ต่อเดือน หรือ ต่อปี ตาม frequency
  effectiveFromMonth: string;   // "yyyy-MM"
  effectiveToMonth?: string;    // "yyyy-MM" รวมเดือนนี้ (เช่น งวดสุดท้ายของสัญญาผ่อน)
  isStop?: boolean;             // version ที่หยุดรายการตั้งแต่ effectiveFromMonth
  note?: string;
  createdAt: Timestamp; createdBy: string; createdByName?: string;
  voided: boolean; voidedAt?: Timestamp; voidedBy?: string; voidedReason?: string; updatedAt?: Timestamp;
}
```
- Zod: `validate/pnlSettingsSchema.ts` (`pnlRateRowSchema`, `pnlFixedCostVersionSchema` + `superRefine`: scope truck ⇒ `truckId`; `effectiveToMonth ≥ effectiveFromMonth`; `isStop` ⇒ ไม่มี `effectiveToMonth`)
- เก็บวันที่เป็นสตริง (ไม่ใช่ Timestamp) โดยตั้งใจ — กันปัญหา `effectiveFrom` UTC-midnight ที่รอบน้ำมันเคยเจอ; สตริง ISO เทียบด้วย `<=` ได้ตรง
- ค่าที่เจ้าของบอกไว้ (กรอกเองผ่านหน้าตั้งค่า ไม่มี seed script): `driver_trip` 250, `helper_day` 400, `maint_service_km` 0.14, `maint_tyre_km` 0.30; ค่างวดรถรวม ≈ ฿80,000/เดือน (4 คัน)
- ไม่มี denormalized field บน collection เดิม; ไม่แก้ schema เดิม

### 4.3 การตัดสินใจใน spec นี้ (นอกเหนือ ADR — เจ้าของแก้ได้ตอน approve)

1. **บิลซ่อมลงเดือนตามวันที่ซ่อมเสร็จ** (`endDate` → `checkOutAt` → `startDate`) เฉพาะ `status == "completed"` (ตอบ follow-up ของ ADR 0031 "completion vs start")
   - ยอดบิลรู้ตอนเสร็จเท่านั้น: คนขับส่ง `invoiceAmount` ตอน check-out (`maintenance_repository.dart:86-114`), แอดมินกรอก `totalCost` ตอนปิด. ถ้าลงตามวันเริ่ม งานที่ปิดเดือนหน้าจะย้อนไปเปลี่ยนเดือนที่ดูไปแล้ว (P&L คำนวณสด)
   - ตรงกับที่แอปคนขับใช้อยู่แล้ว: ประวัติค่าใช้จ่ายลงบิลซ่อมตาม `endDate` (`_maintenanceCompletedDate`, `maintenance_repository.dart:128`)
   - งานซ่อมหลายวันข้ามเดือนจบในเดือนเดียว; PM ที่จอง/นัดไว้มี `startDate` เป็นวันนัดซึ่งอาจอยู่เดือนหน้า
   - ข้อเสีย: แอดมินลืมปิดงาน → บิลไปตกเดือนที่ปิด → แก้ด้วย fail loud F11 (ส่งบิลแล้วแต่ยังไม่ปิด)
   - fallback ใช้ `startDate` ไม่ใช่ `updatedAt` เพราะ `updatedAt` ขยับทุกครั้งที่แก้ (บิลจะกระโดดเดือน)
2. **฿/กม. แบบ fill-to-fill + บรรทัด "ส่วนต่างกระทบยอดน้ำมัน"** (R9, R16) — ทำให้น้ำมันรวมในงบ = เงินที่จ่ายจริงในเดือน (เทียบใบเสร็จได้) และ "ระยะเกินใบงาน" เป็นสัญญาณรั่วล้วน ๆ ไม่ปนเรื่องจังหวะเติมเต็มถังปลายเดือน. บรรทัดกระทบยอด = น้ำมันจ่ายจริงในเดือน − น้ำมันเที่ยว − น้ำมันระยะเกิน (ครอบ: น้ำมันหลังจุดอ่านสุดท้าย, รถที่ใช้ค่าเฉลี่ยฟลีต, แถวไม่มีรถ, น้ำมันของเที่ยวที่ไม่นับ)
3. **Standby = ระยะ route ก่อนหน้า (ขาเดียว) × 2** (owner 2026-11-16, §9 OQ2) — standby จึงแบกน้ำมันและค่าซ่อม accrual ตามระยะนั้น (R8)
4. **multi-drop = ระยะไป-กลับของจุดไกลสุด** (owner 2026-11-16, §9 OQ1) — ไม่ต้องมีระยะจุด→จุด งาน Lotus คำนวณได้; ระยะอ้อมระหว่างจุดตกที่ระยะเกินใบงาน. ต่างจากข้อความ ADR 0031 §2 ("sum of legs + last stop → hub") → แก้ ADR/glossary ใน T11
5. **กระจายประกัน/ภาษี** = ถึงก่อนครั้งถัดไปหรือ 12 เดือน แล้วหารเท่า ๆ กัน (รับรู้ครบทุกบาท) — ตีความ "12 months or until next renewal" ของ ADR §8
6. **ผู้ช่วย:** ใช้กฎไม่จ่ายซ้ำของ payroll (window ที่ผู้ช่วยขับเอง = ไม่มีค่าผู้ช่วย), แบ่งเฉพาะเที่ยวบนรถของเรา (นับ standby เป็นเที่ยว)
7. **`insurance_claim`** ไม่นับเป็นค่าซ่อม (บริษัทประกันจ่าย) แสดงเป็นบรรทัดข้อมูล
8. **เที่ยวคุ้มทุน 2 ตัวเลข** (R17) — ตัวหลักตาม ADR (ต้นทุนคงที่อย่างเดียว) + ตัวรองรวมบรรทัดรายเดือน
9. **ค่าคนขับ standby** ใช้อัตรา P&L เสมอ แม้ payroll config `payStandby: false` (ADR 0031 §3) — แสดงหมายเหตุในหน้าตั้งค่า

### 4.4 `lib/pnlCompute.ts` (pure) + tests

```ts
export interface PnlInputs {
  year: number; month: number;            // เดือน P&L (Bangkok)
  now: Date;                              // สำหรับป้าย "เดือนยังไม่ปิด" — ห้ามเรียก Date.now() ข้างใน
  trips: PnlTripInput[];                  // trip_records delivered ที่ join task แล้ว (field ที่ใช้ตาม §4.1)
  standbys: PnlStandbyInput[];
  helperTasks: PnlHelperTaskInput[];      // tasks ในช่วงที่โหลด: id, driverId, helperDriverIds, checkInAt, date
  trucks: PnlTruckInput[];
  distances: PnlDistanceRow[];            // { table: "hub_soc"|"soc_hub", hubId, socId, distanceKm }
  fuelRows: PnlFuelRowInput[];            // vehicle_expenses type fuel (ทุก status — กรองข้างใน เพื่อเตือน PENDING)
  otherExpenses: PnlOtherExpenseInput[];  // vehicle_expenses type other
  maintenance: PnlMaintenanceInput[];
  renewals: PnlRenewalInput[];            // transactions insurance|tax
  rates: PnlRateRow[];
  fixedCosts: PnlFixedCostVersion[];
  driverIdentity: Record<string, string[]>; // helper uid → identity keys
  planBasisCustomerIds: string[];
}

export interface PnlResult {
  trips: PnlTripRow[];        // ทุกเที่ยว + standby (counted | excluded + reasons[])
  lanes: PnlLaneRow[];
  trucks: PnlTruckRow[];
  statement: PnlMonthStatement;
  breakEven: { primaryTrips: number | null; withMonthLinesTrips: number | null; actualTrips: number; avgContributionThb: number };
  subcontractor: { trips: number; revenueThb: number };
  warnings: PnlWarning[];     // { code, count, amountThb?, refs[] } — code ตาม §4.5
  flags: { monthInProgress: boolean; hasPlanBasisCustomer: boolean };
}

export function computePnl(inputs: PnlInputs): PnlResult;

// helper ที่ export เพื่อทดสอบแยก
export function pnlDateKeyForTrip(checkInAt?: Date, deliveredAt?: Date): string | null;
export function resolveJobDistanceKm(origin: string, destinations: string[] /* 1 = เดี่ยว, ≥2 = multi-drop */, distances: DistanceIndex): { km: number; oneWayKm: number; farthest: string } | { missingLeg: [string, string] }; // max round trip over destinations
export function standbyJobDistanceKm(standby: PnlStandbyInput, truckTrips: PnlTripInput[], task: PnlTaskRoute | null, distances: DistanceIndex): { km: number; source: "previousTrip" | "standbyTask" } | { missing: true }; // 2 × oneWayKm
export function truckFuelWindow(rows: PnlFuelRowInput[], monthStart: Date, monthEnd: Date): TruckFuelWindow; // { usable, odoKm, spend, rate, flags[] }
export function selectPnlRate(rates: PnlRateRow[], kind: PnlRateKind, dateKey: string): PnlRateRow | null;
export function fixedCostMonthlyAmount(versions: PnlFixedCostVersion[], monthKey: string): number;
export function renewalMonthlyAmounts(renewals: PnlRenewalInput[], truckId: string, type: "insurance" | "tax"): Map<string, number>; // monthKey → ฿
export function maintenanceBillDateKey(m: PnlMaintenanceInput): { dateKey: string; estimated: boolean } | null;
export function helperShares(...): { byTripId: Map<string, number>; noTripHelperDays: Array<{ helperUid: string; windowKey: string; amountThb: number }> };
```

- ใช้ซ้ำ: `bangkokDateKey`, `helperWindowKey`, `eligibleHelperDayKeys` (`lib/compensationCompute.ts`), `extractHubId`, `normalizeDestinationCode` (`lib/billingCompute.ts` — import อย่างเดียว ไม่แก้), `normalizeSocIdToKey`, `maintenanceDisplayCost`. ย้าย `KMPL_MIN/KMPL_MAX` จาก `app/app/accounting/fuel/page.tsx:68-69` ไป `lib/fuelEfficiency.ts` แล้วให้หน้า fuel import (พฤติกรรมเดิม)
- **โครงงบรายเดือน (`PnlMonthStatement`)**

| # | บรรทัด | สูตร |
|---|---|---|
| 1 | รายได้ (เที่ยว + standby ที่นับ) | Σ revenue |
| 2 | น้ำมันตามระยะใบงาน | Σ jobKm × rate_T |
| 3 | ค่าคนขับ | Σ driver_trip |
| 4 | ค่าผู้ช่วย (ส่วนแบ่ง) | Σ helper share |
| 5 | ค่าซ่อม accrual | Σ jobKm × (service + tyre) |
| 6 | **กำไรส่วนเกินรวม** | 1 − (2..5) |
| 7 | น้ำมันระยะเกินใบงาน (รายรถ) | Σ_T (odoKm_T − jobKm_T) × rate_T (เฉพาะรถที่฿/กม.ใช้ได้; ติดลบได้ แสดงตามจริง) |
| 8 | ส่วนต่างกระทบยอดน้ำมัน | น้ำมันจ่ายจริงในเดือน − 2 − 7 |
| 9 | ค่าผู้ช่วยที่ไม่มีเที่ยว | Σ helper-day ที่ไม่มีเที่ยว |
| 10 | ค่าผ่านทาง / ค่าใช้จ่ายรถอื่น | Σ `vehicle_expenses` type other, APPROVED, รถเรา/ไม่มีรถ, แยก `category` toll / อื่น |
| 11 | ส่วนต่างค่าซ่อม | บิลจริง (R13) − 5 |
| 12 | **กำไรหลังบรรทัดรายเดือน** | 6 − (7..11) |
| 13 | ประกันภัย | R15 |
| 14 | ภาษีรถ | R15 |
| 15 | ต้นทุนคงที่จากรายการ | Σ R2 แยกตาม `category` (ต่อรถ + ต่อบริษัท) |
| 16 | **กำไรจากการดำเนินงาน** | 12 − (13..15) |

- **Tests (`lib/pnlCompute.test.ts`)** — ใช้ fixture ตัวเลขกลมตามตัวอย่างใน §7; ต้องมีเคส: จัดเดือนตาม `checkInAt` ข้ามเที่ยงคืนวันสิ้นเดือน + fallback `deliveredTimestamp`; leg ทั้ง 2 ทิศ + key ตัวพิมพ์ต่างกัน; multi-drop เลือกจุดไกลสุด/จุดหนึ่งขาดคู่ → F2; standby จากเที่ยวก่อนหน้าบนรถเดียวกัน (เลือกล่าสุดก่อน `startedAt`, ข้ามรถคันอื่น, เกิน 3 วัน → ไม่ใช้)/fallback route งานของ standby (F18)/ไม่มีทั้งคู่ → F2; fill-to-fill (anchor ก่อนเดือน, ไม่มี anchor, ย้อนกลับ, km/L หลุดช่วง → ค่าเฉลี่ยฟลีต, ไม่มีรถใช้ได้เลย); PENDING ไม่นับ; อัตราตามวันมีผล + void + เสมอกัน; ต้นทุนคงที่ monthly/yearly/effectiveTo/isStop/void; ประกันกระจายปกติ/ต่อเร็ว/ไม่มีประวัติ; ผู้ช่วย 2 เที่ยว/standby/ขับเอง/ไม่มีเที่ยว/ข้ามเดือน; บิลซ่อม endDate/checkOutAt/startDate/insurance_claim/ยังไม่ปิด; เที่ยวคุ้มทุน (เฉลี่ย ≤ 0); รถร่วมไม่เข้ายอด; ยอดรวม = ผลรวมแถวที่ปัด

### 4.5 Fail-loud cases

| Code | กรณี | ผล |
|---|---|---|
| F1 `unpriced` | เที่ยว/standby ไม่มี `billingEstimateThb` | badge, ไม่รวมยอด, นับจำนวน |
| F2 `noJobDistance` | resolve รหัสไม่ได้ / leg ไม่มีในตาราง / multi-drop จุดใดขาดคู่ไป-กลับ / standby ไม่มีทั้งเที่ยวก่อนหน้าและ route ของงาน | badge (บอกคู่ที่ขาด), ไม่รวมยอด, นับ |
| F3 `noTruck` | ไม่มี `truckId` (trip → task) หรือ truck doc ไม่มี | badge, ไม่รวมยอด, นับ |
| F4 `missingRate` | ไม่มีอัตรา P&L ของ kind ใดที่มีผล ณ วันที่ของเที่ยว | badge, ไม่รวมยอด, นับ + แบนเนอร์ลิงก์ไปหน้าตั้งค่า |
| F5 `fleetAvgFuelRate` | รถ-เดือนที่ ฿/กม. ใช้ไม่ได้ (ไม่มี A/E, km ≤ 0, km/L หลุด 2–25) | ใช้ค่าเฉลี่ยฟลีต + เตือนรายรถ; ไม่คำนวณระยะเกินของรถนั้น |
| F6 `noFuelRate` | ไม่มีรถคันไหนใช้ได้ในเดือน | เที่ยวทั้งหมดไม่รวมยอด + แบนเนอร์ |
| F7 `noAnchorBeforeMonth` | ไม่มีจุดอ่านก่อนเดือนใน 60 วัน | เตือนรายรถ (ใช้จุดแรกในเดือน) |
| F8 `pendingFuel` | มีใบเสร็จน้ำมัน PENDING ในช่วง | เตือนจำนวน + ยอด ("อนุมัติในหน้า Audit ก่อน") |
| F9 `fuelNoTruck` | แถวน้ำมันไม่มีรถ | เข้าบรรทัดกระทบยอด + เตือน |
| F10 `odometerRollback` | จุดอ่านเลขไมล์ย้อนกลับ | ทิ้งจุดนั้น + เตือน |
| F11 `maintenanceNotClosed` | `driverSubmitted`/มียอด แต่ status ยังไม่ `completed`/`cancelled` | แสดงรายการ + ยอด, ไม่รวม |
| F12 `maintenanceDateEstimated` | completed แต่ไม่มี `endDate` | ลงเดือนด้วย `checkOutAt`/`startDate` + badge |
| F13 `maintenanceZeroAmount` | completed แต่ยอด 0 | เตือน |
| F14 `noRenewalHistory` | รถในฟลีตเดือนนั้นไม่มีช่วงประกัน/ภาษีครอบ | fallback truck doc ÷ 12 (+ เตือน); ไม่มีค่า → 0 + เตือน |
| F15 `renewalZeroAmount` | `transactions` ต่ออายุยอด 0 | เตือน |
| F16 `noTask` | เที่ยวหา task ไม่เจอ | นับได้ (เดือนจาก `deliveredTimestamp`, ระยะจาก `billingLookup*`), ไม่มีส่วนแบ่งผู้ช่วย, badge ข้อมูล |
| F18 `standbyDistanceFromTask` | standby ไม่มีเที่ยวก่อนหน้าบนรถเดียวกันใน 3 วัน | ใช้ route ของงานที่ standby ผูกอยู่ × 2 + badge ข้อมูล (นับยอดได้) |
| F17 `sourceDenied` | อ่าน collection ต้นทางไม่ได้ (เช่น role ที่ได้ capability แต่ไม่ใช่ admin) | หน้า error ระบุชื่อ collection — ไม่แสดงตัวเลขบางส่วน |

ไม่ใช่ error (แสดงผลตามปกติ): helper-day ไม่มีเที่ยว (บรรทัด 9), `trip_records.status == "standby"` (ข้ามโดย design), รถร่วม (สรุปแยก)

### 4.6 Web (Next.js)

- **หน้า**
  - `app/app/accounting/pnl/page.tsx` — `PagePermissionGuard capability={CAPABILITIES.accounting_view_pnl}`; ตัวเลือกปี/เดือน, แผงคำเตือน, แท็บ เดือน / เที่ยว / เส้น / รถ, ปุ่ม Export
  - `app/app/accounting/pnl/settings/page.tsx` — guard `accounting_manage_pnl_settings`
  - หน้า P&L มีลิงก์ "ตั้งค่า P&L" เฉพาะคนที่มีสิทธิ์ manage
- **features/pnl/**
  - `api/pnlSources.ts` — `fetchPnlMonthSources(year, month)` โหลดตาม §4.1 แล้ว map เป็น `PnlInputs` (Timestamp → Date ที่ขอบนี้เท่านั้น)
  - `api/pnlSettings.ts` — `listPnlRates`, `addPnlRate`, `voidPnlRate`, `listPnlFixedCosts`, `addPnlFixedCostItem`, `addPnlFixedCostVersion`, `stopPnlFixedCostItem`, `voidPnlFixedCostVersion` (เขียนจาก client + Zod — แบบเดียวกับ rate card/รอบน้ำมันใน `actions.client.ts`; `stripUndefined` จาก `lib/firestoreWrite.ts`)
  - `hooks/usePnlMonth.ts` — โหลด → `useMemo(() => computePnl(...))`, state loading/error
  - `components/` — `PnlMonthStatement.tsx`, `PnlTripTable.tsx`, `PnlLaneTable.tsx`, `PnlTruckTable.tsx`, `PnlWarningsPanel.tsx`, `PnlRateSettings.tsx`, `PnlFixedCostSettings.tsx`, `PnlRateDialog.tsx`, `PnlFixedCostDialog.tsx`, `PnlVoidDialog.tsx`
  - ~~`utils/pnlExcel.ts`~~ → `utils/pnlHtml.ts` — export ไฟล์ HTML what-if (R21 ใหม่) + `lib/pnl/scenario.ts`
- Select ใน Dialog: `SelectContent className="z-[1005]" position="popper"` (pattern เดิม)
- **เมนู:** `components/app-sidebar.tsx` กลุ่ม Accounting เพิ่ม `{ title: t("nav.pnl"), url: "/app/accounting/pnl", capability: CAPABILITIES.accounting_view_pnl }`
- **i18n keys** — ไฟล์ใหม่ `context/locales/{en,th}/pnl.ts` ลงทะเบียนใน `context/locales/index.ts`; ตัวอย่างคีย์หลัก (ครบทุกคีย์ตอน build):

| key | en | th |
|---|---|---|
| `nav.pnl` (common.ts) | P&L | กำไร-ขาดทุน (P&L) |
| `pnl.title` | Profit & Loss | กำไร-ขาดทุน |
| `pnl.tabs.month` / `.trips` / `.lanes` / `.trucks` | Month / Trips / Lanes / Trucks | รายเดือน / รายเที่ยว / รายเส้น / รายรถ |
| `pnl.trip.contribution` | Contribution | กำไรส่วนเกิน |
| `pnl.trip.jobKm` | Job km | ระยะตามใบงาน (กม.) |
| `pnl.trip.fuel` / `.driverPay` / `.helperShare` / `.maintAccrual` | Fuel / Driver / Helper share / Maintenance accrual | น้ำมัน / ค่าคนขับ / ส่วนแบ่งผู้ช่วย / ค่าซ่อมสะสม |
| `pnl.line.offPlanFuel` | Off-plan km fuel | น้ำมันระยะเกินใบงาน |
| `pnl.line.fuelReconciliation` | Fuel reconciliation | ส่วนต่างกระทบยอดน้ำมัน |
| `pnl.line.helperNoTrip` | Helper-days with no trip | ค่าผู้ช่วยที่ไม่มีเที่ยว |
| `pnl.line.otherVehicleExpenses` | Tolls & other vehicle expenses | ค่าผ่านทาง / ค่าใช้จ่ายรถอื่น |
| `pnl.line.maintenanceVariance` | Maintenance variance | ส่วนต่างค่าซ่อม |
| `pnl.line.insurance` / `.vehicleTax` / `.fixedItems` | Insurance / Vehicle tax / Fixed-cost items | ประกันภัย / ภาษีรถ / ต้นทุนคงที่อื่น |
| `pnl.line.operatingProfit` | Operating profit | กำไรจากการดำเนินงาน |
| `pnl.breakEven.title` | Break-even trips | จำนวนเที่ยวคุ้มทุน |
| `pnl.breakEven.withMonthLines` | Incl. month lines | รวมบรรทัดรายเดือน |
| `pnl.breakEven.unreachable` | Not reachable (avg contribution ≤ 0) | ไม่ถึงจุดคุ้มทุน (กำไรเฉลี่ย ≤ 0) |
| `pnl.banner.monthInProgress` | Month not finished — figures will change | เดือนยังไม่ปิด ตัวเลขยังเปลี่ยนได้ |
| `pnl.banner.planBasis` | Plan-date customers may differ from billing documents at month edges | ลูกค้าที่วางบิลตามวันแผนอาจต่างจากเอกสารวางบิลช่วงปลายเดือน |
| `pnl.banner.liveCompute` | Computed live — an earlier export may not match after source corrections | คำนวณสด — Excel ที่ export ก่อนหน้าอาจไม่ตรงหลังแก้ข้อมูลต้นทาง |
| `pnl.warn.<code>` (F1–F17) | เช่น `unpriced`: "{count} trips unpriced — {amount} not counted" | "{count} เที่ยวยังไม่มีราคา — ไม่นับ {amount}" |
| `pnl.badge.<code>` | สั้น เช่น Unpriced / No distance / No truck | ยังไม่มีราคา / ไม่มีระยะ / ไม่มีรถ |
| `pnl.settings.title` | P&L settings | ตั้งค่า P&L |
| `pnl.settings.rate.driver_trip` / `helper_day` / `maint_service_km` / `maint_tyre_km` | Driver per trip / Helper per day / Servicing ฿/km / Tyres ฿/km | ค่าคนขับต่อเที่ยว / ค่าผู้ช่วยต่อวัน / ค่าบำรุงรักษา ฿/กม. / ค่ายาง ฿/กม. |
| `pnl.settings.payrollReminder` | Payroll config pays {weekday}/{holiday} per trip; P&L uses {pnl} | payroll config จ่าย {weekday}/{holiday} ต่อเที่ยว; P&L ใช้ {pnl} |
| `pnl.settings.fixed.category.<c>` | Installment / GPS / Accounting / Audit / SSO / Bonus / Battery / Other | ค่างวดรถ / GPS / ค่าทำบัญชี / ค่าสอบบัญชี / ประกันสังคม / โบนัส / แบตเตอรี่ / อื่น ๆ |
| `pnl.settings.void.reasonRequired` | Reason is required | ต้องระบุเหตุผล |

### 4.7 Capabilities

| ที่ | สิ่งที่เพิ่ม |
|---|---|
| `lib/capabilities.ts` `CAPABILITIES` | `accounting_view_pnl: "accounting:view_pnl"`, `accounting_manage_pnl_settings: "accounting:manage_pnl_settings"` |
| `lib/capabilities.ts` meta | title/description, module "Accounting" |
| `lib/capabilities.ts` `ROUTE_CAPABILITIES` | `"/app/accounting/pnl"` → view; `"/app/accounting/pnl/settings"` → manage |
| `app/app/security-center/roles/page.tsx` | 2 แถวในเมทริกซ์ — `defaultPermissions` ADMIN `true` อย่างเดียว |
| `context/locales/{en,th}/securityCenter.ts` | `securityCenter.capabilities.<id>.title/description` |
| `lib/roles.ts` | ไม่แก้ (admin = `"*"`); ไม่ให้ manager/customer/partner |

> หมายเหตุ: ต้นทาง (`transactions`, `maintenance`, ราคาบน trip) อ่านได้เฉพาะ admin ตาม rules ปัจจุบัน → ถ้าให้ capability กับ role อื่นในเมทริกซ์ หน้าจะขึ้น F17 (ต้องแก้ rules แยกถ้าจะเปิดจริง)

### 4.8 Firestore Rules (`logitrack-web/firestore.rules`)

```
// P&L settings (ADR 0031) — append-only like announcement rows (ADR 0009): correct by a new row, retire by voiding.
match /pnl_rates/{docId} {
  allow read: if request.auth != null && isWebAdmin();
  allow create: if isAppCheckVerified() && request.auth != null && isWebAdmin()
    && request.resource.data.kind in ['driver_trip', 'helper_day', 'maint_service_km', 'maint_tyre_km']
    && request.resource.data.amountThb is number && request.resource.data.amountThb >= 0
    && request.resource.data.effectiveFrom is string
    && request.resource.data.effectiveFrom.matches('^[0-9]{4}-[0-9]{2}-[0-9]{2}$')
    && request.resource.data.voided == false;
  allow update: if isAppCheckVerified() && request.auth != null && isWebAdmin() && isVoidOnlyUpdate();
  allow delete: if false;
}
match /pnl_fixed_costs/{docId} {
  allow read: if request.auth != null && isWebAdmin();
  allow create: if isAppCheckVerified() && request.auth != null && isWebAdmin()
    && request.resource.data.itemId is string
    && request.resource.data.scope in ['truck', 'company']
    && (request.resource.data.scope != 'truck' || request.resource.data.truckId is string)
    && request.resource.data.frequency in ['monthly', 'yearly']
    && request.resource.data.amountThb is number && request.resource.data.amountThb >= 0
    && request.resource.data.effectiveFromMonth is string
    && request.resource.data.effectiveFromMonth.matches('^[0-9]{4}-[0-9]{2}$')
    && request.resource.data.voided == false;
  allow update: if isAppCheckVerified() && request.auth != null && isWebAdmin() && isVoidOnlyUpdate();
  allow delete: if false;
}
```
- ใช้ `isVoidOnlyUpdate()` เดิม (`firestore.rules:146-149`) — void แก้ได้แค่ `voided/voidedAt/voidedBy/voidedReason/updatedAt`
- อัปเดตตาราง rules ใน `.vibe-rules.md` (§ Firebase Security Rules)

### 4.9 Cloud Functions / billing / Mobile
- ไม่มี callable/trigger ใหม่; **ไม่แตะ** `lib/billingCompute.ts` / `functions/src/core/billingCompute.ts` (import helper จากฝั่งเว็บเท่านั้น → ไม่ต้อง sync)
- Mobile: ไม่มีการเปลี่ยนแปลง, ไม่ bump `pubspec.yaml`

## 5. Affected files

**ใหม่**
- `logitrack-web/lib/pnlCompute.ts` (+ `lib/pnl/*.ts` ถ้าแยก), `logitrack-web/lib/pnlCompute.test.ts`
- `logitrack-web/lib/fuelEfficiency.ts` (`KMPL_MIN/MAX`)
- `logitrack-web/validate/pnlSettingsSchema.ts`
- `logitrack-web/features/pnl/api/{pnlSources,pnlSettings}.ts`, `features/pnl/hooks/usePnlMonth.ts`, `features/pnl/components/*.tsx`, `features/pnl/utils/pnlHtml.ts`, `lib/pnl/scenario.ts`
- `logitrack-web/app/app/accounting/pnl/page.tsx`, `app/app/accounting/pnl/settings/page.tsx`
- `logitrack-web/context/locales/{en,th}/pnl.ts`

**แก้**
- `logitrack-web/lib/collections.ts` (`PNL_RATES`, `PNL_FIXED_COSTS`)
- `logitrack-web/lib/capabilities.ts`, `app/app/security-center/roles/page.tsx`, `context/locales/{en,th}/securityCenter.ts`
- `logitrack-web/components/app-sidebar.tsx`, `context/locales/{en,th}/common.ts` (`nav.pnl`), `context/locales/index.ts`
- `logitrack-web/firestore.rules`
- `logitrack-web/app/app/accounting/fuel/page.tsx` (import `KMPL_MIN/MAX` เท่านั้น)
- ~~`logitrack-web/features/accounting/api/billing.ts` (export `buildHubDisplayResolver`)~~ — **ไม่แตะตอน build:** ไฟล์นี้มีงาน CJSF ที่ยังไม่ commit ค้างอยู่ จึงสร้างป้าย hub ใน `features/pnl/api/pnlSources.ts` จาก `primaryHubLabelFromFirestoreData` + `resolveHubOrSocDisplay` (`lib/hubDisplay.ts`) แทน — ป้ายไทยก่อน ตาม UI ทั่วไป
- เพิ่มตอน build: `logitrack-web/lib/pnl/{types,settings,distance,fuel,helpers,maintenance,aggregate}.ts` (แยกจาก `pnlCompute.ts` ตามกฎ ~300 บรรทัด), `features/pnl/utils/format.ts`
- เอกสาร: `shared-docs/.vibe-rules.md`, `shared-docs/glossary.md` (เพิ่ม "Fuel reconciliation"), `CLAUDE.md` (handover #50), ADR 0031 (ลิงก์ spec)

## 6. Task breakdown

- [x] T1. `validate/pnlSettingsSchema.ts` + `COLLECTIONS.PNL_RATES/PNL_FIXED_COSTS` + types (R1–R3)
- [x] T2. `firestore.rules` 2 match + ตาราง rules ใน `.vibe-rules.md` (R3, N4)
- [x] T3. Capabilities 2 ตัวครบทุกจุดใน §4.7 + `ROUTE_CAPABILITIES` + เมนู sidebar (N4)
- [x] T4. `lib/pnlCompute.ts` + tests — ทำเป็นชั้น ๆ พร้อม test ทีละชั้น:
  - [x] T4a. วันที่/เดือน + `selectPnlRate` + `fixedCostMonthlyAmount` + `renewalMonthlyAmounts` (R1, R2, R5, R6, R15)
  - [x] T4b. `resolveJobDistanceKm` (R8)
  - [x] T4c. `truckFuelWindow` + ค่าเฉลี่ยฟลีต (R9)
  - [x] T4d. `helperShares` (R11)
  - [x] T4e. `maintenanceBillDateKey` + บิลจริง (R13)
  - [x] T4f. `computePnl` รวม: แถวเที่ยว, เส้น, รถ, งบเดือน, เที่ยวคุ้มทุน, warnings F1–F16 (R7, R10, R12, R14, R16, R17, R20)
- [x] T5. `features/pnl/api/pnlSettings.ts` (append / stop / void) + `pnlSources.ts` (query ตาม §4.1, F17)
- [x] T6. `usePnlMonth` + หน้า `/app/accounting/pnl` 4 แท็บ + แผงคำเตือน + ป้าย (R18, R19, R20)
- [x] T7. หน้า `/app/accounting/pnl/settings` + dialogs + กล่องเตือน payroll config (R4)
- [x] T8. Export Excel (R21)
- [x] T9. ย้าย `KMPL_MIN/MAX` ไป `lib/fuelEfficiency.ts` (ไม่เปลี่ยนพฤติกรรมหน้า fuel)
- [x] T10. อัปเดต i18n en + th (`pnl.ts`, `nav.pnl`, capability keys) — คีย์เท่ากันทั้งสองภาษา (N1)
- [x] T11. อัปเดต `.vibe-rules.md` Change Log + Confirmed Pattern "📊 P&L คำนวณสด ไม่เก็บผล" + glossary + `CLAUDE.md` handover + ADR 0031 ลิงก์ spec + **amendment ใน ADR 0031 §2 และ glossary "Job distance"**: multi-drop = ไป-กลับจุดไกลสุด (ไม่ใช่ผลรวม leg), standby = route ก่อนหน้าขาเดียว × 2 (owner 2026-11-16)
- [x] T13. (R21 ใหม่) `lib/pnl/scenario.ts` (`buildPnlScenario` + `recomputePnlScenario` self-contained) + test; `features/pnl/utils/pnlHtml.ts` สร้าง/ดาวน์โหลดไฟล์; ปุ่ม export ในหน้าเป็น HTML; ลบ `pnlExcel.ts` + คีย์ `pnl.excel.*` แทนด้วย `pnl.html.*` (en+th)
- [x] T14. (R2b) หมวด พรบ./ภาษีรถ/ตั้งชื่อเอง: schema + rules ไม่ต้องแก้ (category ไม่ถูกตรวจใน rules) + compute (`fixedItemLines`, ภาษีรถแทนประวัติต่ออายุ) + dialog/ตาราง (แถวเดียว) + i18n + test
- [x] T15. (R18 รายปี) `fetchPnlYearSources`, `lib/pnl/year.ts` (`computePnlYear`, `sliceInputsForMonth`), `usePnlYear`, `PnlYearView`, แท็บ "รายปี" + i18n + test
- [ ] T12. (ฝั่งโค้ดผ่านแล้ว: `tsc` ✓, eslint 0 errors ✓, vitest 421/421 ✓ — เหลือ dev check หลัง deploy) ตรวจ: `tsc --noEmit`, eslint (0 errors), vitest ผ่าน; เจ้าของ deploy rules + web แล้วตรวจบน dev ตาม AC12–AC13

## 7. Acceptance criteria (ตรวจรับ)

> ตัวอย่างตัวเลขเป็น fixture ใน `lib/pnlCompute.test.ts` (ยกเว้น AC12–AC13 ตรวจบนข้อมูลจริง)

- [x] AC1. (R1, R3) อัตรา `driver_trip` 250 มีผล `2026-10-01` และ 270 มีผล `2026-10-16` → เที่ยววันที่ 15 ต.ค. ได้ 250, วันที่ 16 ได้ 270; void แถว 270 → ทั้งสองวันได้ 250; ไม่มีแถวไหนมีผลก่อน `2026-10-01` → เที่ยววันที่ 30 ก.ย. ติด F4
- [x] AC2. (R2, R3) GPS ต่อบริษัท ฿2,400/ปี จาก `2026-01` → ต.ค. = 200; เพิ่ม version ฿3,600/ปี จาก `2026-07` → มิ.ย. = 200, ต.ค. = 300; เพิ่ม version `isStop` จาก `2026-11` → พ.ย. = 0; ค่างวด ฿20,000/เดือน `2025-06`–`2028-05` → `2028-06` = 0
- [ ] AC3. (R3, N4) Firestore rules: แก้ `amountThb` ของแถวที่มีอยู่ → ถูกปฏิเสธ; ลบ → ถูกปฏิเสธ; void (แค่ field void) → ผ่าน; ผู้ใช้ที่ไม่ใช่ admin อ่าน/เขียน → ถูกปฏิเสธ
- [x] AC4. (R5, R6) เที่ยวที่ `task.checkInAt` = 31 ต.ค. 23:30 น. และส่ง 1 พ.ย. 02:00 → อยู่เดือน ต.ค.; เที่ยวที่ไม่มี task → ใช้ `deliveredTimestamp`; `trip_records.status == "standby"` ไม่นับ; standby `startedAt` 31 ต.ค. → ต.ค.
- [x] AC5. (R8) O=`H1`, D=`SOCN`: `hub_soc` H1→SOCN = 120.5, `soc_hub` SOCN→H1 = 118.0 → job km = 238.5; `hubId` เก็บเป็น `h1` ก็ยังเจอ; ขาด `soc_hub` → F2 ระบุคู่ `SOCN→H1`; multi-drop H1 → {S1, S2} ที่ไป-กลับ S1 = 80 และ S2 = 140 → job km = 140 (ไม่ต้องมี leg S1→S2); S2 ขาด `soc_hub` → F2
- [x] AC5b. (R8) รถ T1 ส่งเที่ยว H1→SOCN (ขาเดียว 120.5) เช็คอิน 10 ต.ค. 08:00 แล้ว standby `startedAt` 10 ต.ค. 18:00 → standby km = 241.0 และได้น้ำมัน/ค่าซ่อม accrual ตามนั้น; เที่ยวก่อนหน้าบนรถคันอื่นไม่ถูกเลือก; T1 ไม่มีเที่ยวใน 3 วันก่อน standby แต่งานของ standby คือ H2→SOCE (ขาเดียว 60) → 120 + badge F18; ไม่มีทั้งคู่ → F2
- [x] AC6. (R9) รถ T1: จุดอ่าน 29 ก.ย. odo 100,000; ต.ค. 5 odo 100,800 ฿3,000 / ต.ค. 20 odo 101,700 ฿3,600 / ต.ค. 30 odo 102,500 ฿3,400 / ต.ค. 31 ไม่มีเลขไมล์ ฿2,000 → odoKm 2,500, spend 10,000, rate ฿4.00/กม.; รถที่มีจุดอ่านในเดือนจุดเดียว → ใช้ค่าเฉลี่ยฟลีต + F5; แถว PENDING ฿1,500 ไม่เข้า spend และขึ้น F8
- [x] AC7. (R9, R14, R16) T1 มี 8 เที่ยว × job 250 กม. รายได้ 2,000/เที่ยว, คนขับ 250, ผู้ช่วย 0, ค่าซ่อม 0.14+0.30 → ต่อเที่ยว: น้ำมัน 1,000, ค่าซ่อม 110, กำไรส่วนเกิน 640; เดือน: น้ำมันเที่ยว 8,000, ระยะเกิน (2,500−2,000)×4 = 2,000, กระทบยอด 12,000−8,000−2,000 = 2,000 → น้ำมันรวมในงบ = 12,000 = เงินที่จ่ายจริงใน ต.ค.
- [x] AC8. (R11) อัตรา `helper_day` 400; ผู้ช่วย h อยู่ในงาน 2 เที่ยวที่ window เดียวกัน → เที่ยวละ 200; window ที่ h ขับงานของตัวเอง → 0; window ที่มีแต่งานยกเลิก (ไม่มีเที่ยว/standby) → บรรทัด "ค่าผู้ช่วยที่ไม่มีเที่ยว" 400; window ที่มี 1 เที่ยว + 1 standby → อย่างละ 200
- [x] AC9. (R12, R13) บิล PM ฿6,000 completed `endDate` 2026-10-28 + CM ฿3,000 completed ไม่มี `endDate` `checkOutAt` 3 ต.ค. (badge F12) + CM ฿5,000 `insurance_claim` + งานส่งบิลแล้วยังไม่ปิด ฿2,500 (F11) → บิลจริง ต.ค. = 9,000; accrual ของ 8 เที่ยว = 880 → ส่วนต่างค่าซ่อม = 8,120
- [x] AC10. (R15) ประกัน T1 ต่อ 2025-11-10 ฿24,000 และ 2026-11-05 → ต.ค. 2026 = 2,000; ต่อ 2025-11 แล้ว 2026-09 (10 เดือน) → เดือนละ 2,400 ใน 2025-11..2026-08; รถ active ไม่มีประวัติภาษี + `taxExpense` 3,600 → 300 + F14
- [x] AC11. (R16, R17) ต้นทุนคงที่เดือน 100,000, กำไรส่วนเกินเฉลี่ย 640 → เที่ยวคุ้มทุน 157; บรรทัดรายเดือนรวม 12,500 → ตัวเลขที่สอง 176 (112,500 ÷ 640 = 175.8); เฉลี่ย ≤ 0 → "ไม่ถึงจุดคุ้มทุน"
- [ ] AC12. (R18, R19, R20) บน dev: หน้าโหลด ต.ค. 2569 แสดงครบ 4 แท็บ; ทุกเที่ยวที่ไม่นับแสดง badge + แผงคำเตือนนับจำนวนและรายได้ที่ตกหล่น; ป้ายวันแผนขึ้นเมื่อมีเที่ยว CJSF; เดือนปัจจุบันมีป้าย "เดือนยังไม่ปิด"; ลูกค้า/พาร์ทเนอร์ไม่เห็นเมนูและเปิด URL ตรงแล้วถูกบล็อก
- [ ] AC13. (R9, R16) smoke check กับรายงานต้นทุน ต.ค. 2569 (ข้อมูลจริง): ฿/กม. ฟลีต ≈ ฿4.17 และ odo km สูงกว่า job km 15–18% (ห่างได้เมื่ออธิบายได้ด้วยแผงคำเตือน) — เจ้าของเป็นคนตรวจ
- [ ] AC14. (R4, R21) หน้าตั้งค่า: เพิ่มอัตรา/รายการ/เปลี่ยนยอด/หยุด/void ได้ตามกฎ append-only, void ต้องใส่เหตุผล, กล่อง payroll config แสดง 300/350 ข้าง 250; Export ได้ไฟล์ HTML เปิดแบบไม่ต่อเน็ตได้ ตัวเลขตรงกับหน้า
- [x] AC17. (R21) `recomputePnlScenario` โดยไม่แก้อัตรา = `computePnl` ทุกยอด (เที่ยว, เส้น, รถ, งบเดือน, เที่ยวคุ้มทุน); ค่าคนขับ 250 → 300 ใน fixture AC7 → กำไรส่วนเกิน/เที่ยว 640 → 590 และรวม 5,120 → 4,720; ผู้ช่วย 400 → 500 → ส่วนแบ่ง 200 → 250 และค่าผู้ช่วยที่ไม่มีเที่ยว 400 → 500; ยาง 0.30 → 0.40 → accrual/เที่ยว 110 → 135 และส่วนต่างค่าซ่อมลดตาม; ฟังก์ชันรันได้หลัง `toString()` (ไม่พึ่งตัวแปรนอกฟังก์ชัน)
- [x] AC18. (R2b) รายการภาษีรถ ฿4,800/ปี ของ T1 → ภาษีเดือน 400 แทนประวัติต่ออายุ 300 และไม่อยู่ในบรรทัดต้นทุนคงที่ (ไม่นับซ้ำ); พรบ. ฿2,400/ปี → บรรทัด พรบ. 200; หมวดตั้งชื่อ "Parking" 1,000 + " parking " 500 → บรรทัดเดียว 1,500; พรบ. แบบ company / custom ไม่มีชื่อ → ไม่ผ่าน validation
- [x] AC19. (R18 รายปี) `computePnlYear` เดือน ต.ค. = `computePnl` ต.ค. ทุกยอด; rollback เลขไมล์ของ พ.ย. ไม่ขึ้นเตือนใน ต.ค.; คอลัมน์ ม.ค. → เดือนที่เลือก
- [x] AC15. (N1) คีย์ `pnl.*` en/th จำนวนเท่ากัน ไม่มีข้อความ hardcode ในหน้าใหม่
- [x] AC16. `tsc --noEmit` (web) ผ่าน, eslint 0 errors, vitest ผ่านทั้งหมด (รวมชุดใหม่), CI เขียว

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| ค่าคนขับ P&L (250) ต่างจาก payroll config (300/350) — ถ้าจ่ายตาม payroll จริง กำไรจะสูงเกิน ฿50–100/เที่ยว | กล่องเตือนในหน้าตั้งค่า (R4); ADR 0031 Risks |
| ฿/กม. รายเดือนแกว่งเมื่อรถเติมน้อยครั้ง / เติมเต็มถังปลายเดือน | fill-to-fill + บรรทัดกระทบยอดน้ำมันแยกจังหวะเติมออกจากระยะเกิน; ค่าเฉลี่ยฟลีตเมื่อใช้ไม่ได้ (F5) |
| multi-drop ใช้จุดไกลสุด → ต่ำกว่าระยะจริง (อ้อมระหว่างจุด) ทำให้กำไรต่อเที่ยว multi-drop ดูดีเกิน | ส่วนที่ขาดไปตกที่ "น้ำมันระยะเกินใบงาน" ของรถ (ยังเห็นในงบเดือน); เจ้าของยอมรับ (2026-11-16) |
| standby ใช้ route ก่อนหน้า × 2 — ถ้าเที่ยวก่อนหน้าไม่ใช่ขาที่วิ่งไป standby จริง ระยะจะเพี้ยน | จำกัดเที่ยวก่อนหน้าแค่รถคันเดียวกันใน 3 วัน; ไม่เจอใช้ route ของงาน standby + badge F18 |
| trip ที่ `createdAt` ห่างจาก `checkInAt` เกิน 3 วัน (งานค้างนาน) หลุดจาก query | buffer ±3 วัน; ถ้าพบจริงขยาย buffer (ตรวจตอน AC13) |
| driver บันทึกค่าซ่อมเป็น "ค่าใช้จ่ายอื่น" ซ้ำกับใบซ่อม → นับซ้ำในบรรทัด 10 + 11 | แสดง `category` แยกในบรรทัด 10; เจ้าของตรวจตอน AC13 |
| บิลซ่อมลงเดือนที่ปิดงาน ถ้าแอดมินปิดช้า | F11 แสดงงานที่ส่งบิลแล้วยังไม่ปิด |
| ตัวเลขเดือนเก่าเปลี่ยนหลังแก้ข้อมูลต้นทาง (คำนวณสด) | ตั้งใจ (ADR 0031 §9); ป้าย + ไฟล์ HTML เป็นสำเนาแช่แข็ง |
| rollback | ฟีเจอร์อ่านอย่างเดียวต่อข้อมูลเดิม: ถอดเมนู/route (revert web) ได้ทันที; collection ใหม่ไม่กระทบอะไร (ปล่อยไว้ได้) |

**Rollout (เจ้าของทำ):** deploy `firestore.rules` → deploy web (ไม่มี functions) → เข้า ตั้งค่า P&L กรอก 4 อัตรา (มีผลตั้งแต่เดือนแรกที่ต้องการดู เช่น `2026-10-01`) + รายการต้นทุนคงที่ (ค่างวด 4 คัน, GPS, ค่าทำบัญชี, สอบบัญชี, ประกันสังคม, โบนัส) → เปิด P&L เดือน ต.ค. 2569 ตรวจ AC13

## 9. Open questions / follow-ups

> **Resolved (2026-11-16, owner):** OQ1 = **C** — multi-drop = round trip to the farthest stop ("ไม่มีระยะระหว่างจุด → ใช้ระยะทางไกลสุดเพียงพอแล้ว"); FU2 dropped. OQ2 = standby = previous route × 2 ("ยังไม่เกิดงานเที่ยวใหม่ ใช้ระยะทางของ route ก่อนหน้า ×2 เพราะรวมกลับ yard") — interpreted in R8 as the same truck's last delivered trip within 3 days, its one-way leg × 2, fallback the standby task's route (interpretation **confirmed by owner** 2026-11-16). OQ3 = bills dated by completion and `insurance_claim` excluded. OQ4 = show both break-even figures. The original question text below is kept as the record.

- **OQ1 (ต้องตัดสินก่อน build) — multi-drop ไม่มีระยะระหว่างจุด:** ตาราง `hub_soc_distances`/`soc_hub_distances` มีแค่คู่ Hub↔SOC (`lib/hubSocDistances.ts:96-104`); leg จุด→จุด (SOC→SOC หรือ Hub→Hub) ไม่มีเลย → งาน multi-drop (Lotus ที่เริ่ม 11 พ.ย.) จะติด F2 ทุกเที่ยว. ทางเลือก: (A) **fail loud ตาม spec นี้** + follow-up ขยาย callable ระยะทางให้ cache คู่จุดส่ง (แตะ Cloud Function — นอก ADR 0031); (B) ประมาณ leg ที่ขาดด้วยระยะเส้นตรงจากพิกัด `hubs` × 1.3 และติด badge "ระยะประมาณ"; (C) ใช้ระยะไป-กลับของจุดไกลสุด. **แนะนำ A** สำหรับ v1 แล้วทำ cache คู่จุดเป็นงานถัดไป
- **OQ2 — standby job distance = 0** (§4.3 ข้อ 3): ถ้าเจ้าของอยากให้ standby แบกน้ำมันไป-กลับจุดรับ ต้องมีคู่ระยะ ฐานรถ→จุดรับ ซึ่งยังไม่มี (ไม่มี field ฐานรถ)
- **OQ3 — บิลซ่อมลงเดือนวันเสร็จ** (§4.3 ข้อ 1) และ **`insurance_claim` ไม่นับ** (ข้อ 7) — ยืนยัน
- **OQ4 — เที่ยวคุ้มทุน 2 ตัวเลข** (§4.3 ข้อ 8) — ยืนยัน หรือเหลือตัวเดียวตาม ADR
- FU1. แก้ `transactions.date` ให้เป็นวันที่กรุงเทพ (`RenewalForm.tsx:183` ใช้ UTC) — งานแยก เล็ก
- ~~FU2. cache ระยะทางคู่จุดส่ง multi-drop~~ — ไม่ทำ (OQ1 = C)
- FU3. tenant scoping ของ P&L เมื่อ ADR 0026 ขึ้นจริง (`tenantId` บน `pnl_*` + rescope query)
- FU4. เทรนด์หลายเดือน / เปรียบเทียบเส้นข้ามเดือน
- FU5. ถ้า payroll กลายเป็นระบบจ่ายจริง → พิจารณาให้ P&L อ่าน `driver_compensation_config` แทนอัตราของตัวเอง (ADR 0031 Alternatives)
