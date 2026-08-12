## Rules
Before doing any task, read `shared-docs/.vibe-rules.md` and follow all rules defined there.

Also read `shared-docs/database-migration-plan.md` to understand the planned database architecture (Firestore → SQL Hybrid). Use this as context when working on any feature that touches billing, rate cards, vehicle expenses, transactions, or analytics. Do NOT begin SQL migration work unless explicitly instructed — the plan is reference only until a Phase is triggered.

---

## Spec-Driven Development (native Claude Code workflow)

สำหรับฟีเจอร์ที่ไม่ใช่ของเล็กๆ ให้ "วาง spec ก่อนเขียนโค้ด" ผ่าน 2 เฟส:

1. **Plan →** `/spec-new <feature>` — รวบรวม requirement, ถาม clarifying questions, ออกแบบ (อาจ delegate ให้ subagent `spec-architect`), เขียน spec ลง `shared-docs/specs/<kebab-name>.md` จาก template `shared-docs/specs/_TEMPLATE.md` **โดยยังไม่เขียนโค้ดฟีเจอร์** → หยุดให้ user approve
2. **Build →** `/spec-build <spec-name>` — implement ตาม spec ทีละ task, เคารพ `.vibe-rules.md` (billing sync 2 ไฟล์, i18n en+th, hub map แยก nameToCode/codeToName, Firestore rules), verify ตาม acceptance criteria, set `Status: ✅ Done`

ไฟล์ที่เกี่ยวข้อง:
- `shared-docs/specs/_TEMPLATE.md` — โครง spec มาตรฐาน (ผูกกับ stack: Firestore/i18n/feature architecture/billing)
- `.claude/commands/spec-new.md`, `.claude/commands/spec-build.md` — slash commands
- `.claude/agents/spec-architect.md` — subagent ออกแบบ spec (read-only, ไม่เขียนโค้ดฟีเจอร์)

> ⚠️ `.gitignore` มี rule `*.md` (กว้าง) → ไฟล์ spec/command/agent ใหม่ถูก ignore ต้อง `git add -f` ตอน commit

---

## Session Handover Summary (อัปเดตล่าสุด: 12 ส.ค. 2026 — **V3.1.0-web-n-mobile** — web + mobile ใช้เลขเวอร์ชันเดียวกันแล้ว)

> เอกสารนี้สรุปสิ่งที่ทำไปแล้วตั้งแต่ `shared-docs/.vibe-rules.md` ถูกสร้าง เพื่อให้ Antigravity และ AI อื่นๆ สามารถ sync ต่อได้ทันที

---

### 🌐 สถานะโปรเจกต์ปัจจุบัน

- **Release label ปัจจุบัน**: **V3.1.0-web-n-mobile** — ตั้งแต่รุ่นนี้ web กับ mobile **เดินเลขเวอร์ชันร่วมกัน** (`3.1.0`) ป้าย `-web-n-mobile` เป็น**ชื่อรุ่นในเอกสารเท่านั้น** — **ห้ามใส่ลงฟิลด์ `version`** เพราะทั้ง pnpm และ `pub_semver` (force-update gate, ADR 0007) parse แบบ semver เคร่งครัด
- **เวอร์ชัน Flutter (logitrack-mobile)**: `pubspec.yaml` = **3.1.0+1** — SafeArea/gesture inset ทุกหน้า + force-update pipeline (ADR 0007) + เวอร์ชันใน drawer
- **เวอร์ชัน Next.js (logitrack-web)**: **3.1.0** (`package.json` + `functions/package.json` + `shared-docs/package.json` + root `package.json` — bump พร้อมกันเสมอ) — Deploy ผ่าน Firebase Hosting + GitHub Actions (dev auto หลัง CI เขียว / prod manual `workflow_dispatch`)
- **Route path**: ย้ายจาก `app/admin/` → `app/app/` (internal structure เปลี่ยน, URL ผู้ใช้ยังเดิม)
- **Firestore Rules**: `logitrack-web/firestore.rules` — เป็น SSOT ทุก collection
- **Monorepo Structure**: `logitrack-platform/` ครอบ `logitrack-web/` + `logitrack-mobile/` + `shared-docs/`

---

### 📐 ADR — บันทึกการตัดสินใจเชิงสถาปัตยกรรม

ADR ตัวจริงอยู่ที่ **`shared-docs/adr/`** (ชื่อไฟล์ `NNNN-kebab-case.md` ไม่มี prefix อื่น) — **อ่านก่อนแตะเรื่องที่ ADR ครอบคลุม** และเขียน ADR ใหม่เมื่อมีการตัดสินใจที่ย้อนกลับยาก

| ADR | เรื่อง |
|-----|-------|
| `0000-adr-conventions.md` | กติกาการเขียน/ตั้งชื่อ ADR |
| `0001-checkin-time-on-trip-records.md` | เก็บเวลาเช็คอินบน `trip_records` |
| `0002-edit-job-category-on-delivered-trip.md` | แก้ หลัก/เสริม บนเที่ยวที่ส่งแล้ว + re-derive ราคาแบบ atomic |
| `0003-edit-forms-fail-loudly-on-legacy-docs.md` | ฟอร์มแก้ไขต้อง fail ดังๆ เมื่อเจอ doc เก่าที่ schema ไม่ครบ |
| `0004-shared-oninvalid-handler-for-all-forms.md` | `onInvalid` handler กลางตัวเดียวสำหรับทุกฟอร์ม |
| `0005-truck-plate-filter-billing-document-driver-monitor.md` | ฟิลเตอร์ทะเบียนรถใน Billing Document + Driver Monitor |
| `0006-origin-destination-filter-driver-monitor.md` | ฟิลเตอร์ต้นทาง/ปลายทางใน Driver Monitor |

ศัพท์ในโดเมน (หลัก/เสริม, SOC, hub, standby ฯลฯ) → **`shared-docs/glossary.md`**

---

### ✅ Feature ที่ Implement ล่าสุด (ก่อน 13 เม.ย. 2026)

#### 1. Mobile Client Monitor (Security Center) — [April 7–13, 2026]

ออกแบบและ implement ระบบตรวจสอบเวอร์ชันและ last update ของ Flutter app ต่อผู้ใช้แต่ละคน

**Flutter (logitrack-mobile) — ไฟล์ใหม่:**

| ไฟล์ | หน้าที่ |
|------|---------|
| `lib/core/services/mobile_install_id_service.dart` | Stable install ID ต่อการติดตั้ง — ใช้ Firebase Installations + SharedPreferences fallback |
| `lib/core/services/mobile_client_heartbeat_service.dart` | Singleton heartbeat — merge ข้อมูลลง `drivers/{driverId}/mobile_installations/{installId}` |
| `lib/core/services/mobile_app_version_service.dart` | เช็ก `settings/mobile_app.minAllowedVersion` (Semantic Versioning ผ่าน `pub_semver`) — แสดง blocking dialog + ลิงก์ดาวน์โหลด APK หากเวอร์ชันต่ำกว่า |

**Schema ใน Firestore (`drivers/{driverId}/mobile_installations/{installId}`):**
```
driverId       String  (denormalized สำหรับ collection group query)
driverName     String  (denormalized)
partnerId      String  (subcontractorId)
platform       "android" | "ios" | "other"
appVersion     String  (PackageInfo.version — เช่น "1.2.3")
buildNumber    String  (PackageInfo.buildNumber)
flavor         "dev" | "prod"
lastSeenAt     Timestamp (serverTimestamp)
```

**Heartbeat จุดที่เรียก:**
- หลัง driver profile resolved post-login (`main_layout.dart`)
- หลัง significant job writes (check-in, delivery complete, loading complete)
- Debounce 45 วินาที เพื่อลด Firestore writes

**Web (logitrack-web) — ไฟล์ใหม่/แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `app/admin/security-center/mobile-clients/page.tsx` | หน้าใหม่ — collectionGroup query บน `mobile_installations`, แสดงตาราง driver, version, platform, last seen |
| `components/security-center-sidebar.tsx` | เพิ่ม menu item "Mobile Clients" |
| `lib/capabilities.ts` | เพิ่ม `security_view_mobile_clients` capability |
| `lib/roles.ts` | มอบ capability ให้ role Admin |
| `context/locales/en/securityCenter.ts` | เพิ่ม keys ภาษาอังกฤษ |
| `context/locales/th/securityCenter.ts` | เพิ่ม keys ภาษาไทย |
| `firestore.rules` | เพิ่ม match `mobile_installations` subcollection (read: admin; write: owner) |
| `firestore.indexes.json` | เพิ่ม collection group index `mobile_installations` เรียงตาม `lastSeenAt` DESC |

**pubspec.yaml — package ที่เพิ่ม:**
- `firebase_app_installations` — stable install ID
- `pub_semver` — Semantic Version comparison (แก้ปัญหา string compare "1.10.0" > "1.2.0" ผิด)

**Firestore config — `settings/mobile_app`:**
```json
{
  "minAllowedVersion": "1.0.0",
  "apkDownloadUrl": "https://..."
}
```
ตั้งค่าผ่าน Firebase Console หรือ Admin เท่านั้น (client อ่านอย่างเดียว)

---

#### 2. Multi-task Assignment to Driver — [April 7, 2026]

- Web admin สามารถ assign หลาย task ให้ driver คนเดียวใน session เดียวได้
- Commit: `7585d5e`

#### 3. Edit Trip Button (Web) — [April 2026]

- เพิ่มปุ่ม Edit Trip ใน driver monitor dashboard
- Commit: `b00b2d1`

#### 4. Partner Filter (Web + Mobile) — [April 2026]

- เพิ่ม filter ตาม `partnerId` (subcontractorId) ในหน้า web
- เพิ่ม `partnerId` field ใน heartbeat และ mobile data model
- Commits: `dc45931`, `3325165`

---

### 📦 Dependencies ที่ใช้อยู่ (อัปเดตล่าสุด)

#### logitrack-mobile (Flutter)
- `firebase_app_installations` — install ID
- `pub_semver` — version comparison
- `package_info_plus` — อ่านเวอร์ชัน APK
- `shared_preferences` — fallback install ID storage
- `url_launcher` — เปิดลิงก์ดาวน์โหลด APK
- `easy_localization` — i18n (en.json / th.json)
- `firebase_app_check` — App Check (Play Integrity สำหรับ Android release)

#### logitrack-web (Next.js)
- ไม่มี `axios` — ใช้ `fetch` (native) + Firebase SDK (Firestore, httpsCallable)
- HTTP client ฝั่ง Cloud Functions ใช้ `fetch` ใน Node

---

#### 6. Sidebar App Version Badge — [13 เม.ย. 2026]

แสดงเวอร์ชัน Web Admin UI ใต้ข้อความ "Enterprise Admin" ใน Sidebar

**ไฟล์ที่แก้ไข/สร้างใหม่:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `lib/app-version.ts` | ไฟล์ใหม่ — export `WEB_APP_VERSION` จาก `package.json` (ปัจจุบัน `1.6.0`) |
| `components/app-sidebar.tsx` | เพิ่ม `<span>` แสดง version ใต้ "Enterprise Admin" ใน SidebarHeader |
| `context/locales/en/common.ts` | เพิ่ม key `nav.appVersion` = `"Version {version}"` |
| `context/locales/th/common.ts` | เพิ่ม key `nav.appVersion` = `"เวอร์ชัน {version}"` |

**Pattern ที่ใช้:**
- ดึงเวอร์ชันจาก `package.json` ผ่าน `resolveJsonModule` ของ TypeScript (ไม่ต้องใช้ env var)
- ใช้ i18n `t("nav.appVersion", { version: WEB_APP_VERSION })` ตามมาตรฐานโปรเจกต์
- เวอร์ชันจะ auto-update เมื่อ bump `version` ใน `package.json` แล้ว build ใหม่

---

#### 7. Driver Monitor: Add Photo to Empty Trip Steps — [17 เม.ย. 2026]

ปรับหน้า `/admin/driver-monitor` ใน dialog แก้ไขเที่ยวให้แอดมินสามารถ **เพิ่มรูปใน step ที่ยังไม่มีรูป** ได้ (ไม่ใช่แค่ replace รูปเดิม)

**ไฟล์ที่แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `features/drivers/components/EditTripDetailsDialog.tsx` | แสดง photo slots ครบ 8 ประเภท (Loading 4 + Delivery 4) แบบแบ่ง phase; ช่องว่างแสดง placeholder + ปุ่ม Add photo; ช่องที่มีรูปยัง Replace ได้เหมือนเดิม |
| `context/locales/en/driverMonitor.ts` | เพิ่ม i18n keys: `driverMonitor.editTrip.addPhoto`, `driverMonitor.editTrip.loadingPhase`, `driverMonitor.editTrip.deliveryPhase` |
| `context/locales/th/driverMonitor.ts` | เพิ่ม i18n keys ภาษาไทยสำหรับ 3 key เดียวกัน |

**พฤติกรรมสำคัญ:**
- Header รูปแสดงจำนวนแบบ `มีรูปแล้ว/ทั้งหมด` (เช่น `3/8`)
- Save flow เดิมยังใช้ได้: ถ้า type ยังไม่มี จะถูกเพิ่มเข้า `trip_records.photos[]`; ถ้ามีแล้วจะ replace ตาม type เดิม
- ทดสอบหน้า UI แล้วสามารถเพิ่มรูปใน step ว่างได้จริงตาม flow

---

#### 8. Hub–SOC: กลุ่ม SPX vs SPK, SOC normalization, ป้าย Hub/SOC ใน Admin — [30 เม.ย. 2026]

คำนวณและ cache ระยะ Hub↔SOC **แยกกลุ่มเครือข่าย** — ไม่จับคู่ข้าม Shopee (SPX) กับ J&T/SPK — และใช้ **รหัส SOC มาตรฐาน** (SOCE/SOCN/SOCW + `normalizeSocIdToKey`) เพื่อ dedup แถว SOC; แถวใน `hub_soc_distances` / `soc_hub_distances` มีฟิลด์ optional **`network`** (`"SPX"` \| `"SPK"`).

**Logic หลัก (แชร์แนวคิดเดียวกัน):**

| ที่ | ไฟล์ / สิ่งที่ทำ |
|----|------------------|
| กฎจัดกลุ่ม Hub | `logitrack-web/validate/hubSchema.ts` — `hubDistanceNetworkGroup`, `hubSourceIdHasSpxSuffix`, `STATION_TYPE_ENUM` |
| Pure helpers (Functions) | `logitrack-web/functions/src/core/distances.ts` — `normalizeSocIdToKey`, `normalizeStationType`, doc ID helpers |
| Callable คำนวณ Matrix | `logitrack-web/functions/src/distances.ts` — `computeHubSocDistances` (แบ่ง batch ต่อกลุ่ม SPX/SPK, โหลด `linkedCustomerId` → โค้ดลูกค้าจาก `customers`) |
| Client batch (เช่น Sources) | `logitrack-web/lib/hubSocDistances.ts` — โค้งเดียวกับ callable สำหรับ build ฝั่ง web |
| SSOT + validate task | `shared-docs/schemas/taskSchema.ts`, `logitrack-web/validate/taskSchema.ts` — `SOC_DESTINATIONS`, `SOC_KEYS`, `socIdMatchesKey`, `normalizeSocIdToKey` |
| ป้ายแสดงใน First Mile / Line Haul / งาน | `logitrack-web/lib/hubDisplay.ts` (ใหม่) — `primaryHubLabelFromFirestoreData`, `buildHubCodeToDisplayMap*`, `resolveHubOrSocDisplay` |

**Web admin ที่ปรับ:** `app/admin/sources/page.tsx`, `HubDistancePanel.tsx`, `pickup-import-dialog.tsx`, `components/map/SourcesMap.tsx`, `first-mile/page.tsx`, `line-haul/page.tsx`, `context/locales/{en,th}/firstMile.ts`, `FirstMileTaskDialog.tsx`, `LineHaulTaskDialog.tsx`, `features/tasks/services/taskService.ts`, hooks, `features/customers/api/customers.ts`, `useDriverMonitor.ts`, `components/ui/select.tsx`

**Mobile:** `logitrack-mobile/lib/features/home/data/repositories/hubs_repository.dart`, `hub_soc_distances_repository.dart` — สอดคล้องกลุ่มเครือข่ายและการแสดงจุด

**เอกสารเพิ่ม:** รายละเอียดและ Change Log → `shared-docs/.vibe-rules.md` (ส่วน Hub–SOC Distance + Change Log 2026-04-30)

---

#### 5. Security & Access Tab ใน Role Matrix — [13 เม.ย. 2026]

เพิ่ม tab ใหม่ใน `/admin/security-center/roles` (Role & Permission Matrix):

- **7 capabilities ใหม่:** `security_view_overview`, `security_manage_users`, `security_manage_roles`, `security_view_audit`, `security_manage_api_keys`, `security_view_status`, `security_view_mobile_clients`
- **Default:** Admin ได้ทุก (`"*"`), Partner ได้ `security_view_mobile_clients`
- **ไฟล์ที่แก้:** `roles/page.tsx`, `context/locales/en/securityCenter.ts`, `context/locales/th/securityCenter.ts`
- Admin สามารถ toggle ให้ role อื่นได้จาก UI โดยไม่ต้องแตะ code

---

#### 9. HubDialog: แก้ Select (ประเภทสถานี / การผูก / อ้างอิง) เลือกไม่ได้ใน Dialog — [1 พ.ค. 2026]

**อาการ:** ในฟอร์ม "เพิ่มจุดรับงานใหม่" / แก้ไขจุดรับส่ง (`HubDialog`) dropdown แสดงค่าได้แต่เปิดเมนูแล้วเลือกไม่ได้

**สาเหตุ:** Radix `Select` พอร์ทัล `SelectContent` ที่ `z-[1000]` (ค่าเริ่มต้นใน `components/ui/select.tsx`) ต่ำกว่า `DialogOverlay` `z-[1001]` และ `DialogContent` `z-[1002]` ใน `components/ui/dialog.tsx` → เมนูอยู่ใต้เลเยอร์ overlay

**แก้ไข:** `app/admin/first-mile/hub-dialog.tsx` — ใส่ `className="z-[1005]"` และ `position="popper"` ให้ทั้งสาม `SelectContent` (station type, customer link kind, linked customer)

**การใช้งาน:** `HubDialog` import จากหน้า `app/admin/sources/page.tsx`; re-export `app/admin/line-haul/hub-dialog.tsx`

**Pattern:** เวลาเพิ่ม Select ใหม่ใน Dialog อื่น ให้ตั้ง z-index ของ content ให้สูงกว่า dialog (อ้างอิง `LineHaulTaskDialog.tsx`)

**เอกสาร:** `shared-docs/.vibe-rules.md` — Key patterns, Form Patterns, Change Log 2026-05-01, Confirmed Patterns

---

#### 10. Accounting: Import ค่าผ่านทาง (Excel/CSV) + ตารางแสดงทะเบียน + Quick Date Filters — [1 พ.ค. 2026]

เพิ่ม flow นำเข้าค่าผ่านทางสำหรับแอดมินในหน้า `/admin/accounting/other` โดยผูกข้อมูลกับรถ/คนขับ และทำให้ตารางแสดงทะเบียนรถชัดเจน

**ไฟล์ที่แก้ไขหลัก:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/app/admin/accounting/toll-expense-import-dialog.tsx` | ไฟล์ใหม่: import `.xlsx/.xls/.csv`, map หัวคอลัมน์แบบยืดหยุ่น, กรองเฉพาะ `ผ่านทาง`, เลือกรถ+คนขับ, batch write ลง `vehicle_expenses` |
| `logitrack-web/app/admin/accounting/actions.client.ts` | เพิ่ม model ฟิลด์ toll (`tollImportSequence`, `tollLocation`, `tollLane`, `tollSourceType`), helper ดึง driver assignment, ฟังก์ชัน `batchCreateTollExpenseImports` |
| `logitrack-web/app/admin/accounting/other/page.tsx` | เพิ่มปุ่ม import, เพิ่มคอลัมน์ทะเบียน, แสดงวันเวลา, เพิ่ม quick filter `เดือนนี้` / `30 วันล่าสุด` + ปุ่ม reset |
| `logitrack-web/app/admin/accounting/audit/page.tsx` | เพิ่มคอลัมน์ทะเบียน และแสดงวันเวลา |
| `logitrack-web/context/locales/en/accounting.ts` | เพิ่ม i18n keys ของ toll import และ quick date filters |
| `logitrack-web/context/locales/th/accounting.ts` | เพิ่ม i18n keys ภาษาไทยของ toll import และ quick date filters |
| `logitrack-web/firestore.rules` | อนุญาต admin create `vehicle_expenses` พร้อม validate ฟิลด์ขั้นต่ำ |
| `.gitignore` | เพิ่ม `file example/` เพื่อไม่ track ไฟล์ตัวอย่างนำเข้า |

**พฤติกรรมสำคัญ:**
- เก็บค่าผ่านทางใน collection เดิม `vehicle_expenses` (`type: "other"`, `category: "toll"`) โดยบันทึก `truckId` ชัดเจน
- parser รองรับวันที่จากไฟล์รูปแบบ `dd/MM/yyyy HH:mm:ss` และคงเวลาไว้ใน `date`
- parser มี fallback หลายชั้นสำหรับ header เพี้ยน/มีบรรทัดนำ และกัน keyword ชนกัน (`วันที่เกิดรายการ` vs `ประเภท`)
- ใช้ `status: "PENDING"` สำหรับรายการที่นำเข้า เพื่อเข้า flow audit เดิม

---

#### 11. Accounting: Rate Card + Fuel Adjustment UX Enhancements — [1 พ.ค. 2026]

ปรับหน้า `/admin/accounting/rate-card` ให้ใช้งานจริงได้ครบขึ้นทั้ง flow import, manual add, และกฎน้ำมัน

**ไฟล์ที่แก้ไขหลัก:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/app/admin/accounting/rate-card/page.tsx` | ปรับโครง UI/filters, manual add, fuel adjustment, template export |
| `logitrack-web/app/admin/accounting/actions.client.ts` | เพิ่ม update/delete สำหรับ fuel adjustment |
| `logitrack-web/context/locales/en/accounting.ts` | เพิ่ม/แก้ i18n keys สำหรับ edit/delete/actions/createdAt |
| `logitrack-web/context/locales/th/accounting.ts` | เพิ่ม/แก้ i18n keys ภาษาไทยสำหรับ edit/delete/actions/createdAt |

**พฤติกรรมสำคัญ:**
- **Fuel adjustment**:
  - รับค่าเป็นเปอร์เซ็นต์โดยตรง (`+/- %`) แล้วแปลงเป็น `rateMultiplier` ก่อนบันทึก
  - ถอดช่อง `addThbPerTrip` ออกจาก UI (เขียนค่า `0` ที่ payload)
  - ตารางแสดงค่าเป็น `%` และเปลี่ยนคอลัมน์หมายเหตุเป็น `Created At`
  - เพิ่ม action แบบสามจุด (`Dropdown`) ต่อแถว: `แก้ไข` / `ลบ`
  - รองรับแก้ไขกฎเดิม (prefill form + update)
- **Manual add rate card**:
  - ช่อง `ประเภทรถ` เปลี่ยนเป็น dropdown
  - ดึงตัวเลือกจาก `COLLECTIONS.TRUCKS` ฟิลด์ `type` (fallback เป็น options จาก entries/`4WJ`)
- **Rate card filters/manual source-destination**:
  - ตัวเลือกต้นทาง/ปลายทางดึงจาก master (`HUBS` + `SOC_DESTINATIONS`) โดยตรง และ merge กับค่าที่มีใน entries
  - ย้ายบล็อก `ตัวกรอง` ไปอยู่ในการ์ด `Rate Card` เดียวกับตาราง
- **Template export**:
  - เพิ่มตัวอย่างข้อมูลที่ใช้ได้จริงมากขึ้น
  - เพิ่มชีท `instructions` ในไฟล์
  - เซลล์ `F2` ใส่ข้อความ `*ลบรายการตัวอย่างออกก่อน` พร้อมไฮไลต์สีเหลือง

---

#### 12. Maintenance (Web): ค่าใช้จ่ายแสดงผล + รวมจาก `invoiceAmount` — [2 พ.ค. 2026]

**อาการ:** การ์ดสรุป PM/CM/รวมแสดง ฿0 และเปอร์เซ็นต์ **NaN%** แม้งานเสร็จและคนขับส่ง **ยอดตามใบเสร็จ** (`invoiceAmount`) แล้ว เพราะ UI รวมเฉพาะ `totalCost`

**แนวทาง:** `maintenanceDisplayCost(record)` ใน `logitrack-web/features/maintenance/utils/maintenanceDisplayCost.ts` — ลำดับ: ใช้ `totalCost` ถ้า > 0; ไม่มี → `costLabor + costParts` ถ้ารวม > 0; ไม่มี → `invoiceAmount` ถ้า > 0; เหลือ 0

**ไฟล์ที่แก้ไขหลัก:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/features/maintenance/utils/maintenanceDisplayCost.ts` | ไฟล์ใหม่ — pure helper สำหรับยอดแสดง/รวม |
| `logitrack-web/features/maintenance/components/MaintenanceDashboard.tsx` | การ์ด PM/CM รวมด้วย `maintenanceDisplayCost` |
| `logitrack-web/features/maintenance/components/MaintenanceOverview.tsx` | การ์ดรวม, PM/CM % (กัน NaN เมื่อรวม ≤ 0), คอลัมน์รวมในตาราง |
| `logitrack-web/features/maintenance/components/maintenance/MaintenanceHistoryList.tsx` | ยอดหลักทางขวาต่อรายการ |

**หมายเหตุ:** คอลัมน์ค่าแรง/อะไหล่อาจยังเป็น `-` ถ้ามีแต่ `invoiceAmount` (ยังไม่แยกจากใบเสร็จคนขับ); ยังไม่ prefill ฟอร์มแก้ไขจาก `invoiceAmount` อัตโนมัติ

**เอกสาร:** `shared-docs/.vibe-rules.md` — Change Log 2026-05-02 (Maintenance), Confirmed Patterns

---

#### 13. Accounting Income: Billing Bug Fixes + UI Enhancements — [3 พ.ค. 2026]

แก้ bug billing snapshot และปรับปรุง UX หน้า Income ให้ครบถ้วน

**Bug Fixes (Cloud Functions + billingCompute):**

| ปัญหา | สาเหตุ | แก้ไข |
|-------|--------|-------|
| Backfill admin function ล้ม | `enforceAppCheck: true` สำหรับ function ที่ไม่มี App Check token | ปิด `enforceAppCheck` สำหรับ `backfillTripBillingSnapshots` (มี auth check อยู่แล้ว) |
| "ไม่พบ Rate Card สำหรับ SPK-GW → SPK890103" | รหัสปลายทาง `SPK890103-ลาดกระบัง26` ไม่ match กับ `SPK890103` | `normalizeDestinationCode` strip suffix หลัง dash สำหรับ SPK codes |
| เที่ยวก่อน effective date ทุกตัวไม่ได้ billing | ไม่มี rate entry ที่ `effectiveFrom <= billDateMs` → return null | Fallback ใช้ Rate Card เก่าที่สุดสำหรับ route/vehicle class นั้น |

**ไฟล์ที่แก้ไข (Billing Logic):**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/functions/src/tripBillingOnDelivered.ts` | `enforceAppCheck: false` สำหรับ backfill; `consumeAppCheckToken: true` สำหรับ compute |
| `logitrack-web/lib/billingCompute.ts` | `normalizeDestinationCode` strip SPK suffix; `selectBillingRateEntry` fallback to oldest |
| `logitrack-web/functions/src/core/billingCompute.ts` | Sync กับ `lib/billingCompute.ts` |
| `logitrack-web/app/admin/accounting/actions.client.ts` | ใช้ `normalizeDestinationCode` ตอนบันทึก Rate Card; เพิ่ม `deleteCustomerRateEntry`, `updateCustomerRateEntry` |

**Income Page UI Enhancements (`app/admin/accounting/income/page.tsx`):**

| Feature | รายละเอียด |
|---------|------------|
| **Names instead of codes** | แสดงชื่อ Hub/SOC แทนรหัสในคอลัมน์ต้นทาง/ปลายทาง โดยใช้ `hubNameMap` + `resolveHubName` / `getDestinationDisplayName` |
| **Export Excel (With Billing)** | แยกคอลัมน์ Origin Code / Origin Name / Destination Code / Destination Name; ชีท Summary แสดงสถิติรวมตามลูกค้า |
| **Pagination** | เลือก page size (10/25/50/100); แสดง "หน้า X จาก Y" + ปุ่ม prev/next |
| **Column rename** | "Estimated income (THB)" → "Billing Amount" (EN) / "ยอดวางบิล" (TH) |
| **Backfill improvements** | สถิติ: Total missing, Can fix, Need Rate Card; ปุ่ม "Fix N trips now" + "Backfill by date range" แยก |
| **Missing Billing tab** | Filter by status (ทั้งหมด/แก้ไขได้/ต้องเพิ่ม Rate Card); 4 summary cards; Export Excel; Pagination |
| **Date format hints** | เพิ่ม `(dd/MM/yyyy)` / `(วว/ดด/ปปปป)` ใน label ของ filter dates และ backfill dates |

**Rate Card Page Enhancements (`app/admin/accounting/rate-card/page.tsx`):**

| Feature | รายละเอียด |
|---------|------------|
| **Export data** | ปุ่มส่งออก Rate Card ที่ filter เป็น Excel |
| **Edit/Delete entries** | Action column มี `DropdownMenu` (Edit/Delete); Edit dialog prefill จากแถวที่เลือก |
| **Stats summary** | 3 cards: Total Rate Cards, Customers with rates, Matching Filter |

**i18n Keys ที่เพิ่ม/แก้:**

| ไฟล์ | Keys |
|------|------|
| `context/locales/en/accounting.ts` | `income.table.finalRate` → "Billing Amount"; `income.table.originCode/Name`, `destinationCode/Name`; `income.filter.deliveredFrom/To` + format hint; `income.backfill.*` stats keys; `income.missing.*` filter/stats/export keys; `rateCard.stats.*`, `rateCard.actions.*`, `rateCard.edit*` |
| `context/locales/th/accounting.ts` | Thai translations ตามข้างต้น |

**Firestore Index ที่เพิ่ม (`firestore.indexes.json`):**
```json
{
  "collectionGroup": "trip_records",
  "queryScope": "COLLECTION",
  "fields": [
    { "fieldPath": "status", "order": "ASCENDING" },
    { "fieldPath": "createdAt", "order": "DESCENDING" }
  ]
}
```

---

#### 14. Backfill Task Customer Links (Cloud Function) — [4 พ.ค. 2026]

**ปัญหา:** งาน (tasks) เก่าขาดฟิลด์ `sourceHubLinkedCustomerId` และ `destinationLinkedCustomerId` ซึ่งจำเป็นสำหรับการคำนวณ billing snapshot

**แก้ไข:** Cloud Function `backfillTaskCustomerLinks` (onCall, admin-only) — สแกนงาน ทั้งหมด เติมฟิลด์ลูกค้าที่หายไปด้วย default customer (docId: `7gbnX0Tv9xNQgTKrgp0F`, code: `TTP`), batch update กลุ่มละ 100 รายการ, คืนสถิติ (totalProcessed, updated, alreadyComplete, errors)

**ไฟล์:**
| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/functions/src/backfillCustomerLinks.ts` | ไฟล์ใหม่ — logic backfill |
| `logitrack-web/functions/src/index.ts` | export callable `backfillTaskCustomerLinks` |
| `logitrack-web/app/app/utilities/backfill/page.tsx` | ไฟล์ใหม่ — UI สำหรับรัน backfill |
| `logitrack-web/components/app-sidebar.tsx` | เพิ่ม "Utilities" menu item (admin-only) พร้อม Wrench icon |

**วิธีรัน:**
- Dash board admin → Utilities → Backfill → ปุ่ม "Run Backfill"
- หรือ Firebase Console → Functions → backfillTaskCustomerLinks → Test tab

**ขั้นตอนถัดไป:** หลังจาก backfill สำเร็จ ให้รัน `backfillTripBillingSnapshots` เพื่อคำนวณ billing สำหรับเที่ยวที่ส่งสินค้าแล้วทั้งหมด

---

#### 15. Multi-Delivery Stops & Photo Organization — [4–8 พ.ค. 2026]

**ลักษณะใหม่:** ระบบจัดการจุดส่งหลายจุดในเที่ยวเดียว (เฉพาะงาน J&T) พร้อมแยกรูปตามจุด

**บนมือถือ (Mobile):**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `lib/features/delivery_phase/presentation/pages/delivery_phase_page_multi.dart` | ไฟล์ใหม่ — หน้า multi-delivery (list จุดส่ง + ปุ่ม "Add stop") |
| `lib/features/home/data/models/trip_record.dart` | เพิ่ม `deliveryStops[]` field (list of stops with lat/lng/photos/sequence) |
| `lib/features/delivery_phase/data/repositories/delivery_trip_repository.dart` | เพิ่มตรรมชาติสำหรับอัปเดต stop ทีละรายการและหลายจุด |

**บน Web Admin:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `features/drivers/components/DriverMonitorDashboard.tsx` | ปรับแสดง destination: แทน single destination ให้เป็น "X stops" badge (clickable); dropdown เลือกจุด; แยกรูป Loading phase vs Delivery stops |
| `features/tasks/components/DeliveryStopsEditor.tsx` | ไฟล์ใหม่ — แก้ไขจุดส่งแต่ละอัน, เพิ่ม/ลบจุด, ปรับลำดับ |
| `features/drivers/utils/multiDeliveryProgress.ts` | ไฟล์ใหม่ — ฟังก์ชัน helper คำนวณความคืบหน้าจุด delivery |

**Firestore เอกสารตัวอย่าง (trip_records):**
```typescript
deliveryStops: [
  {
    sequence: 1,
    destinationName: "บ้าน 123",
    destinationLat: 13.7...,
    destinationLng: 100.5...,
    arrivedAt?: Timestamp,
    completedAt?: Timestamp,
    photos: [{ type: "delivery_step_1", url: "..." }, ...]
  },
  // ... more stops
]
```

**พฤติกรรมสำคัญ:**
- จุดส่งจะแสดง `destinationName` (ลบ " - " suffix เดิม) และสามารถดูรูปแบบจุด
- หน้า DriverMonitor แสดง "3 stops" พร้อม dropdown; ภาพจัดแยกตาม phase + stop sequence
- สำหรับงาน J&T เท่านั้น; งานอื่นยังใช้ flow เดิม (single destination)

---

#### 16. Admin Assign Tasks to Active Drivers with 'On run' Badge — [7 พ.ค. 2026]

**ลักษณะใหม่:** ปล่อยให้แอดมินส่งงานให้คนขับที่กำลังรันเที่ยวอยู่ (มี active trip) และแสดง "On run" badge เพื่อบ่งบอกสถานะ

**ไฟล์ที่แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `features/drivers/components/DriverMonitorDashboard.tsx` | เพิ่ม "On run" badge ตัวอักษรแดง/ส้ม เมื่อคนขับมี active trip |
| `features/tasks/services/taskService.ts` | ปลดล็อก assignment สำหรับคนขับที่ "On run" (ยังคงตรวจสอบสิ่งอื่น) |

**พฤติกรรม:**
- Combobox เลือกคนขับยังแสดงทั้งหมด (ไม่กรองใหม่); เพิ่ม badge ไปข้างหน้า
- ส่งงานได้ไม่ว่าสถานะ; แม่นยำสั่งเดินไปรับงานใหม่เมื่อเช็คอินจบที่จุดหน้าสุด

**Commit:** `3868bf0`, `c6c7c32` (fix/feat pair)

---

#### 17. Normalize Truck Type & Dropdown Display — [5–7 พ.ค. 2026]

**ลักษณะใหม่:** ทำให้ประเภทรถ (truck type) ถูกนำเสนออย่างสม่ำเสมอในทั่ว web admin

**ไฟล์ที่แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| Utilities & helpers | Normalize โค้ด TRUCK_TYPE ที่ต่างแปลงกัน + ใช้ dropdown ยืนยัน choices |
| UI components | ปรับเลขประเภท 4WJ/6WJ/2WJ ให้ตรงตัวอักษรเสมอ |

---

#### 18. Customer Permission System (customerScopeId) — [9 พ.ค. 2026] — V2.4.0-web

ให้ลูกค้าดูข้อมูล operations เฉพาะของตัวเองใน web admin ได้ โดย mirror pattern เดียวกับ `partnerScopeId`

**ไฟล์ที่แก้ไข/สร้างใหม่:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `hooks/useCustomerScope.ts` | Hook ใหม่ — อ่าน `auth.token.customerScopeId` จาก JWT |
| `functions/src/users.ts` | รับ `customerScopeId` parameter ใน `updateUserRole` / `createUser`; toggle `disabled` |
| `functions/src/auth.ts` | เขียน `forceLogout: true` หลัง role update |
| `app/app/security-center/users/page.tsx` | `CustomerScopeCell` UI; Edit user modal ครบถ้วน; Toggle enable/disable |
| `app/app/driver-monitor/page.tsx` | Filter trips ตาม `billingCustomerId` เมื่อ customer-scoped |
| `app/app/first-mile/page.tsx` | Filter tasks ตาม customer linked fields |
| `app/app/line-haul/page.tsx` | Filter tasks ตาม customer linked fields |
| `app/app/incident-reports/page.tsx` | Filter incidents ตาม customer |
| `firestore.rules` | เพิ่ม `isCustomer()` helper; อนุญาต Customer read trip_records/tasks |
| `lib/capabilities.ts` | เพิ่ม capabilities สำหรับ Customer role |
| `lib/permissions.ts` | เพิ่ม `getDefaultRouteForRole()` (Admin→dashboard, Customer→driver-monitor) |
| `lib/roles.ts` | มอบ operations view capabilities ให้ Customer role |
| `context/locales/{en,th}/users.ts` | i18n keys สำหรับ customer scope UI |

---

#### 19. Route Path Refactoring: /admin → /app — [9 พ.ค. 2026] — V2.4.0-web

ย้าย internal file structure ทุก admin page จาก `app/admin/` → `app/app/` (URL ผู้ใช้ไม่เปลี่ยน)

- ~150 ไฟล์ย้ายโดย script `logitrack-web/refactor-paths.js`
- `app/app/layout.tsx` ปรับ integrate PagePermissionGuard
- `tests/*.spec.ts` ปรับ path ทุกไฟล์แล้ว

---

#### 20. Route & Page-Level Permission Guards — [9 พ.ค. 2026] — V2.4.0-web

ป้องกัน unauthorized URL access แม้จะพิมพ์ URL ตรง

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `components/page-permission-guard.tsx` | Component ใหม่ — block + redirect ตาม role |
| `lib/permissions.ts` | `getDefaultRouteForRole()`, deny unmapped routes by default |
| `lib/capabilities.ts` | ROUTE_CAPABILITIES ครอบคลุม /utilities |
| `app/app/layout.tsx` | integrate permission guard + Firestore forceLogout listener |

Pages ที่ wrap: first-mile, line-haul, driver-monitor, incident-reports, trucks, maintenance, fuel, other, income, rate-card

---

#### 21. Force Logout System — [9 พ.ค. 2026] — V2.4.0-web

บังคับ logout user ทันทีผ่าน Firestore listener หลัง admin เปลี่ยน role หรือ disable account

- Cloud Function เขียน `forceLogout: true` ลง Firestore user doc
- Layout listener ตรวจ field → `signOut()` + redirect `/login`
- **Fix:** listener ไม่ kick customer กลับ `/login` ทุกครั้งที่ login อีกต่อไป (clear `forceLogout` หลัง signOut)
- **Fix:** Force refresh ID token หลัง self role change เพื่อให้ permissions ใหม่มีผลทันที
- **Fix:** `setAdminClaims` ไม่ override explicit role assignments; ลบ non-bootstrap emails ออกจาก `ADMIN_EMAILS`

---

#### 22. Standby Records: Web Admin Page — [17 พ.ค. 2026]

เพิ่มหน้า Admin สำหรับดู Standby Records ตาม Standby Transaction Spec

**ไฟล์ที่แก้ไข/สร้างใหม่:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/app/app/standby-records/page.tsx` | หน้าใหม่ — แสดง standby records (filter by driver, date range, status); แสดง duration + รูปหลักฐาน |
| `logitrack-web/app/app/standby-records/standby-backfill-dialog.tsx` | Dialog สำหรับ backfill ข้อมูลเก่าเข้า `standby_records` collection |
| `logitrack-web/context/locales/en/common.ts` | เพิ่ม i18n keys สำหรับ standby records |

---

#### 23. CI/CD: GitHub Actions Workflows — [18 พ.ค. 2026]

ตั้ง CI/CD pipeline ครั้งแรกด้วย GitHub Actions

**ไฟล์ที่สร้างใหม่:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `.github/workflows/ci.yml` | CI workflow: type-check (`tsc --noEmit`) + ESLint + Vitest ผ่าน pnpm; trigger บน PR และ push ไป main |
| `.github/workflows/deploy.yml` | Deploy workflow: Firebase Hosting deploy dev/prod ตาม branch; `channelId: live`; permissions สำหรับ GitHub token |

**หมายเหตุ:** `pnpm-lock.yaml` sync แล้วกับ `functions/package.json`; claude worktrees ย้ายออกจาก git submodule refs และเพิ่มใน `.gitignore`

---

#### 24. ESLint + TypeScript: CI-compatible Fixes — [18 พ.ค. 2026]

แก้ ESLint errors และ TypeScript errors เพื่อให้ CI ผ่านทุก step

**ESLint config (`eslint.config.mjs`):**
- `no-explicit-any` downgrade เป็น `warn` (widespread tech debt ไม่ใช่ blocker)
- ignore `functions/lib/**` (compiled JS output) และ `functions/scripts/**`
- แก้ remaining errors ใน `LocationPicker.tsx`, `command.tsx`, `language.tsx`, `file-viewer.tsx`, `sidebar.tsx`
- แก้ `prefer-const` ใน `lib/print-image-url.ts`

**TypeScript (`tsc --noEmit`):**
- แทนที่ `any` ด้วย proper types ใน `income/page.tsx`, `DashboardVehicleMapClient.tsx`
- แก้ `toDate()` type guard และ status cast ใน `custom-stops-review/page.tsx`

---

#### 25. Test: canAccessRoute Fix + Vitest Config — [18 พ.ค. 2026]

แก้ logic และ test config เพื่อให้ Vitest test suite ผ่าน

**ไฟล์ที่แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/lib/permissions.ts` | แก้ `canAccessRoute` — public routes (`/login`, `/join-network`, `/about` ฯลฯ) คืน `true` เสมอโดยไม่ขึ้นกับ role (เดิม block anonymous users) |
| `logitrack-web/vitest.config.ts` | เพิ่ม `exclude: ['functions/**', 'node_modules/**']` เพื่อ Vitest ไม่รัน Cloud Functions tests |

---

#### 26. Feature Architecture Refactor — [19 พ.ค. 2026]

ย้าย web admin ไปใช้โครง **feature-based** (`logitrack-web/features/<domain>/`) แบบเดียวกับ mobile

- โครงใหม่ต่อ domain: `features/<domain>/api/` (Firestore read/write + callables), `features/<domain>/components/`, `features/<domain>/utils/`
- domain ที่มีแล้ว: `accounting`, `companies`, `customers`, `drivers`, `maintenance`, `tasks`
- หน้า `app/app/**/page.tsx` ดึง logic จาก `features/<domain>/api/*` แทนการเขียน Firestore ตรงในหน้า
- Commit: `3dbd74f` (refactor: migrate to feature architecture)

**Pattern:** ฟีเจอร์ใหม่ฝั่ง web ให้สร้างใต้ `features/<domain>/` — แยก data access (`api/`) ออกจาก UI (`components/`)

---

#### 27. Billing Document Generation (Invoice/Receipt/Excel ZIP) — [22–31 พ.ค. 2026]

สร้างเอกสารวางบิลครบชุดจากมุมมอง "เราเป็นผู้ให้บริการขนส่ง วางบิลไปยังลูกค้า (เช่น CJSF)" — ดูสเปกเต็มที่ `.vibe-rules.md` หัวข้อ **📦 Billing Document Spec**

**ไฟล์หลัก:**

| ไฟล์ | หน้าที่ |
|------|---------|
| `logitrack-web/lib/billingDocument.ts` | สร้าง Invoice PDF + Excel detail (`generateDetailExcelBuffer`) + Receipt PDF; bundle เป็น ZIP |
| `logitrack-web/lib/billingStatement.ts` | helper รวมยอด/จัดกลุ่มรายการสำหรับ statement |
| `logitrack-web/app/app/accounting/billing-document/page.tsx` | หน้า UI เลือก period/ลูกค้า/บริษัท → generate + download ZIP |
| `logitrack-web/app/app/accounting/billing-result/page.tsx` | หน้าแสดงผลลัพธ์/ประวัติการวางบิล |
| `logitrack-web/features/accounting/api/billing.ts` | API layer สำหรับ billing (feature-based) |

**Invoice/Receipt PDF — รายละเอียดที่ทำ (หลาย commit):**
- ใช้ `bahttext` (named export) แปลงจำนวนเงินเป็นตัวอักษรไทย — **fix:** import แบบ named ไม่ใช่เรียก module object เป็น function (`8cd7ed0`)
- ป้าย `ผู้จัดทำ` / `ผู้ตรวจสอบ` ใต้ชื่อผู้ลงนาม + ช่องวันที่ใต้ทุกลายเซ็น
- ภาษีหัก ณ ที่จ่าย 1% (withholding tax) — receipt แสดงการหักเหมือน invoice (`e3bb23b`)
- checkbox ช่องทางชำระ **เงินสด / เงินโอน** ใน invoice PDF (`e26643c`)
- บรรทัด multi-drop/drop fee แสดง **ยอด drop จริง** ไม่ใช่ base rate (`2ffc902`)
- **Receipt แยกจาก billing ZIP** — receipt ออกเฉพาะตอน "Mark as paid" เท่านั้น (`25d5468`)

**Excel detail:** 1 sheet รวมทุก trip/คนขับ; **fix NaN** ในแถว footer (กำหนด type `ExcelRow = number | string`, แทน `NaN` ด้วย `""`)

---

#### 28. Company Profile + Bill Authorization (companyScope) — [25 พ.ค. 2026]

รองรับหลายบริษัท (multi-company) สำหรับการวางบิล + จำกัดสิทธิ์ subcontractor ตาม `companyId` claim (mirror pattern เดียวกับ `customerScopeId` / `partnerScopeId`)

**ไฟล์ใหม่/แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `hooks/useCompanyScope.ts` | Hook ใหม่ — อ่าน `auth.customClaims.companyId`; `isCompanyScoped` = มี companyId และไม่ใช่ admin/manager |
| `validate/companySchema.ts` | Zod schema สำหรับ company (ชื่อ, ที่อยู่, Tax ID, บัญชีธนาคาร ฯลฯ) |
| `features/companies/api/companies.ts` | CRUD บริษัทใน collection `companies` |
| `app/app/companies/page.tsx` | หน้าจัดการรายชื่อบริษัท (issuer ของ invoice) |
| `app/app/settings/company-profile/page.tsx` | หน้าตั้งค่า profile บริษัท (header ที่ใช้บน invoice/receipt) |
| `lib/capabilities.ts` + `lib/roles.ts` | capabilities สำหรับ company management |
| `context/locales/{en,th}/company.ts` | i18n keys (ไฟล์ใหม่) + ลงทะเบียนใน `context/locales/index.ts` |
| `firestore.rules` | rules สำหรับ `companies` |

---

#### 29. Standby Billing — [27–30 พ.ค. 2026]

คิดค่าบริการ standby (รถจอดรอ) เป็น **rate คงที่ต่อ event ต่อลูกค้า** ตาม effective date

**ไฟล์หลัก:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `functions/src/standbyBilling.ts` | ไฟล์ใหม่ — callable `computeStandbyBillingSnapshot` + `backfillStandbyBillingSnapshots` (admin) เขียน billing ลง `standby_records/{id}` |
| `functions/src/core/billingCompute.ts` + `lib/billingCompute.ts` | เพิ่ม `computeStandbyBilling`, `StandbyRateEntry` (sync 2 ไฟล์เสมอ) |
| collections ใหม่ | `STANDBY_RATE_ENTRIES` = `standby_rate_entries`, `CUSTOMER_SERVICE_FEES` = `customer_service_fees` |

**Logic:**
- resolve `customerId`: ใช้ field ตรงก่อน → ไม่มีก็ดูผ่าน `taskId` → `task.sourceHubLinkedCustomerId` / `destinationLinkedCustomerId`
- rate: เลือก `standby_rate_entries` ที่ match customer + effective date; **fallback** ไป `customer_service_fees` (`feeType: "standby"`) ถ้าไม่มี rate entry
- fields ที่เขียน: `billingEstimateThb`, `billingCustomerId`, `billingRateEntryId`, `billingEffectiveFromDateStr`, `billingComputedAt`
- **fix (`d0918db`):** standby record แสดง route แบบ `origin → destination` เหมือนเที่ยวปกติ

---

#### 30. Multi-Drop Billing Logic (Flat Extra-Stop Fee) — [27 พ.ค. 2026]

แก้ logic การคิดเงิน multi-delivery ใน `computeMultiDeliveryBilling` (sync ทั้ง `functions/src/core/billingCompute.ts` และ `lib/billingCompute.ts`)

- **โหมดใหม่ (flat fee):** ถ้ามี `extraStopFeeThb` (จาก `customer_service_fees`, `feeType: "extra_stop"`):
  - **base rate** มาจาก *planned destination* (`task.destination`) — order-independent (ไม่ขึ้นกับลำดับที่คนขับส่งจริง)
  - ทุก stop อื่นคิด **flat fee** (ไม่ lookup rate card, **ไม่คูณ fuel multiplier**)
  - fallback เลือก base เป็น stop แรกที่มี rate card ถ้าไม่ตรง planned dest
- **โหมด legacy:** ถ้าไม่มี `extraStopFeeThb` → stop[0] = base rate, stop[1+] = lookup rate card ต่อ route (คูณ fuel multiplier)
- มี scripts ช่วย ops: `functions/scripts/` — `migrate-standby-trips.js`, `recompute-trip-billing.js`, `reset-multidrop-billing.js`, `set-service-fee.js`, `diagnose-billing.js`, `inspect-trip.js`, `check-rates.js`

---

#### 31. Income Page: Trip Type Filter — [27 พ.ค. 2026]

เพิ่ม filter ประเภทเที่ยว **normal / multi-drop / standby** ในหน้า `app/app/accounting/income/page.tsx` พร้อมปรับ Excel detail sheet format (`c320c91`, `a9a1c9b`); เพิ่ม `edit-billing-dialog.tsx` สำหรับแก้ billing รายเที่ยว

---

#### 32. Rebrand → LogiTrack Pro + App Icons — [31 พ.ค. 2026]

- Mobile: launcher icon ใหม่ (Android mipmap ทุกขนาด + iOS AppIcon set + web), login screen แสดงโลโก้แทน truck icon, เพิ่ม `assets/app_icon.jpg`
- Web: favicon เป็น `icon.jpg` (Next.js App Router auto-handling), sidebar logo เป็นรูปโลโก้, เปลี่ยนชื่อ "Logistics Pro" → "LogiTrack Pro" ใน sidebar + i18n (en/th)
- Commit: `001cc8f`

---

#### 33. Billing Recompute: Bug fixes + Performance — [1 มิ.ย. 2026]

แก้ 3 ปัญหาหลักใน billing recompute:

| ปัญหา | สาเหตุ | แก้ไข |
|-------|--------|-------|
| `forceRecompute` ถูก skip | `tryWriteBillingSnapshotFromTripData` มี early-return ก่อนตรวจ flag | pass `forceRecompute` เข้าไปและเช็คก่อน early-return |
| Backfill timeout (trips จำนวนมาก) | sequential reads ทีละ trip | `timeoutSeconds: 540`; pre-fetch tasks ด้วย `Promise.all`; cache rate entries + fuel adjustments ต่อ customer ด้วย `Map` |
| 7 trips ล้ม "No matching rate entry" | `sourceHub` ชื่อเต็มไม่ถูก resolve เป็นรหัส | `hubNameToCode` map ครอบ `sourceHub` ด้วย; เพิ่ม reverse lookup code→displayName; error message แสดง route จริง |

**ไฟล์:** `logitrack-web/functions/src/tripBillingOnDelivered.ts`

---

#### 34. Billing Document: Display + Export Improvements — [1 มิ.ย. 2026]

| Feature | รายละเอียด |
|---------|------------|
| **`getDocsFromServer`** | `trip_records` + `standby_records` + `billing_statements` ใช้ `getDocsFromServer` bypass cache — กด "โหลดข้อมูล" เห็นราคาที่ recompute ทันที |
| **`resolveDisplayName("SPK-GW")` fix** | try raw code ก่อน normalize → "SPK-GW" resolve เป็น "J&T EXPRESS บางปู" ถูกต้อง |
| **Invoice PDF: unit price = final rate** | `groupToLineItems` ใช้ `billingEstimateThb` (หลัง fuel adjustment) แทน `billingBaseRateThb` → quantity × unitPrice = total |
| **Excel styled** | เปลี่ยน `xlsx` → `xlsx-js-style`; border thin gray, zebra-stripe, header น้ำเงิน bold, footer เทาอ่อน bold + numFmt, A4 landscape fit-to-width |

**ไฟล์:** `lib/billingDocument.ts`, `lib/billingStatement.ts`, `app/app/accounting/billing-document/page.tsx`

---

#### 35. Income Page: Inline deliveredTimestamp Edit + EditTripDetailsDialog — [1 มิ.ย. 2026]

- **Income page:** ปุ่ม ✏️ (admin) ข้างช่องวันที่ส่ง → แสดง `datetime-local` inline → write `deliveredTimestamp` ลง Firestore + update row ทันที
- **EditTripDetailsDialog:** เพิ่มช่อง `datetime-local` "วันที่-เวลาส่งสำเร็จ"; prefill จาก `trip.deliveredTimestamp`; warning สีเหลืองถ้าว่าง
- **Pattern:** `deliveredTimestamp` ≠ `updatedAt` — billing ใช้ `deliveredTimestamp` เลือก effective rate card date

**ไฟล์:** `app/app/accounting/income/page.tsx`, `features/drivers/components/EditTripDetailsDialog.tsx`

---

#### 36. Mobile + Web: truckId/plate Denormalization + Backfill — [2 มิ.ย. 2026]

ชุดงานทำให้ truck identifiers ปรากฏครบทุก transaction collection ทั้ง write-time และ backfill

**Mobile (v2.6.1+1):**

- `standby_records`, `incident_reports`, `vehicle_expenses` เขียน `truckId`/`truckLicensePlate` ตั้งแต่สร้าง
- `SavedTripSummary` เพิ่ม `truckId`/`truckLicensePlate` (toJson/fromJson) พา truck info จาก loading → delivery/incident
- Standby submission: เปลี่ยนเป็น `ref.set({...})` เดี่ยว (กัน double write) + update `tasks/{taskId}.status`

**Web:**

- `features/accounting/api/billing.ts`: `getVehicleExpensesByType` อ่าน stored `truckLicensePlate` ก่อน fallback lookup `trucks`
- Fuel page: แก้ทะเบียนผ่าน `Select` จาก trucks (set ทั้ง `truckId` + `truckLicensePlate` พร้อมกัน)
- LineHaul dialog SOC source เป็น searchable Popover combobox; Fuel inputs เพิ่ม `step="any"` รองรับทศนิยม

**Cloud Functions:**

- `backfillTripTruckData` (callable): 3-pass backfill `trip_records` / `vehicle_expenses` / `tasks`; trucks master โหลดครั้งเดียว; UI แสดงสถิติแยก collection

---

#### 37. Mobile fix: OtherExpenseFormPage truckId + re-fetch driver (v2.6.2+1) — [13 มิ.ย. 2026]

**ปัญหา:** (1) `OtherExpenseFormPage` ไม่มี constructor params สำหรับ truckId/plate → `vehicle_expenses` จาก "Other expense" ขาด truck identifiers; (2) คนขับสำรองที่สลับรถกลางกะอาจบันทึกทะเบียนรถเก่าเพราะ cache ค้าง

**แก้:**

- `OtherExpenseFormPage`: เพิ่ม `truckId`/`truckLicensePlate` constructor params + include ใน VehicleExpense ตอน save
- `VehicleExpensePage`: เพิ่ม `_fetchFreshDriverData()` — ดึง driver doc ล่าสุดก่อนเปิดทุก form (Refuel + Other); fail gracefully (ใช้ cached data ถ้า Firestore error)
- Bump: 2.6.1+1 → 2.6.2+1

---

#### 38. Web: driverName helper + billingHubLabel + fuel km/L by truck + hub source_name_th — [13 มิ.ย. 2026]

| เรื่อง | ไฟล์ | สิ่งที่เปลี่ยน |
|-------|------|---------------|
| `driverDisplayName()` helper | `lib/driverName.ts` (ใหม่) | priority fullNameTh → name → firstName+lastName → email → fallbackId; รายงาน/billing แสดงชื่อไทยเสมอ |
| Hub label สำหรับ billing | `lib/hubDisplay.ts` เพิ่ม `billingHubLabelFromFirestoreData` | EN → TH → code (สำหรับใบแจ้งหนี้); `primaryHubLabelFromFirestoreData` ยังใช้ TH → EN → code ใน UI ทั่วไป |
| Billing document driver name | `billing-document/page.tsx` | ใช้ `driverDisplayName` resolve ชื่อไทย; ใช้ `billingHubLabelFromFirestoreData` สำหรับ hub label |
| Fuel km/L by truck | `accounting/fuel/page.tsx` | จัดกลุ่มตาม `truckId`/ทะเบียน (ไม่ใช่ driverId); เรียงตามเลขไมล์; `FuelFlag` type + range KMPL 2–25 |
| Hub dialog `source_name_th` | `first-mile/hub-dialog.tsx`, `validate/hubSchema.ts` | `source_name_th` กลาย required (ชื่อไทย); `source_name_en` = optional (billing name); organic migration hub เก่าที่เก็บชื่อไทยใน `source_name_en` |
| Driver schema `fullNameTh` | `validate/driverSchema.ts`, EditDriverForm, NewDriverForm | เพิ่ม field `fullNameTh` (required); ช่อง "ชื่อ-นามสกุล (ภาษาไทย)" ใน form |

---

#### 39. Billing: Fix Hub/SOC Destination Code Resolution — [15 มิ.ย. 2026]

**ปัญหา:** `buildHubNameToCodeMap` รวม `name→code` และ `code→name` ไว้ใน map เดียว — เมื่อ `task.destination` เป็นโค้ดอยู่แล้ว (เช่น `"SPK890174"`) จะไปตี reverse entry และแปลงเป็นชื่อไทย (`"ห้วยขวาง10"`) แทน → ไม่ match rate card → `"No rate"` → recompute ล้มเหลว → `billingEstimateThb` ค้างค่าเก่า (เช่น fuel-adjustment ที่ลบไปแล้ว `-10`) ยังแสดงบนหน้า Income + Billing Document

**แก้ไข:**
- แยก `buildHubMaps` เป็น `nameToCode` / `codeToName` สองออบเจ็กต์แยกกัน
- Resolve `sourceHub`/`destination` ผ่าน `nameToCode` เท่านั้น (โค้ดผ่านทะลุได้เลย)
- Fallback retry ด้วย `codeToName` สำหรับลูกค้าที่ rate card key โดยชื่อแสดงผล
- Income page backfill: เพิ่ม checkbox `force-recompute` (เดิมไม่ได้ overwrite ถ้ามีค่าอยู่แล้ว)

**ไฟล์ที่แก้ไข:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/functions/src/tripBillingOnDelivered.ts` | `buildHubMaps()` → `nameToCode` + `codeToName` แยก; resolver ใช้ `nameToCode` + fallback `codeToName` |
| `logitrack-web/app/app/accounting/income/page.tsx` | เพิ่ม force-recompute checkbox ใน backfill section |
| `logitrack-web/context/locales/en/accounting.ts` | เพิ่ม `income.backfill.forceRecompute` i18n key |
| `logitrack-web/context/locales/th/accounting.ts` | เพิ่ม i18n key ภาษาไทย |
| `shared-docs/.vibe-rules.md` | เพิ่มบันทึก bug pattern: "ห้ามรวม bidirectional map" + Change Log 2026-06-15 |

**Pattern:** เมื่อแตะ billing destination/hub resolution ให้คง `nameToCode` และ `codeToName` เป็น map แยก ไม่เคย merge; direction สำหรับ rate-card lookup คือ `nameToCode` (rate card key ด้วยโค้ด)

---

#### 40. Per-task Truck Selection (รถผูกกับงาน ไม่ใช่คนขับ) — [13 ก.ค. 2026] — Mobile 2.9.0+1

**ปัญหาเดิม:** task ไม่เคยเก็บว่า "รถคันไหนวิ่ง" — เก็บแค่ `truckType` (แอดมินเลือกมือ) + `licensePlate` (copy จาก `drivers.currentAssignment.truckPlate`) และ **ไม่มี `tasks.truckId`** ทั้งที่ `backfillTripTruckData.ts` + mobile `loading_phase_page.dart` อ่านอยู่ → `trip_records.truckId` เป็น null เสมอ. ที่หนักกว่าคือ dropdown คนขับ **ซ่อนคนขับที่ไม่มีรถผูก** → จะให้คนขับใช้รถคันอื่นต้องไปปลด/ผูกรถใหม่ที่หน้า Truck Assignment ก่อน. และถ้าคนขับ **สร้างงานเอง** (manual check-in) โดยไม่มีรถผูก จะได้ `truckType='4W'`, `licensePlate='-'` → **bill ผิด vehicle class เงียบๆ**

**Data model — 3 concept แยกกัน (ห้ามยุบรวม):**

| Field | ความหมาย |
|-------|----------|
| `drivers.currentAssignment` | **รถประจำ** — คงไว้ แต่กลายเป็นแค่ **ค่า default** ตอน assign ไม่ใช่ filter |
| `tasks.truckId` + `licensePlate` + `truckType` | **รถของงานนี้** (admin เลือก → คนขับยืนยัน/แก้ตอนเช็คอิน) |
| `drivers.activeTruck` **(ใหม่)** | **รถที่รับผิดชอบตอนนี้** `{truckId, truckPlate, taskId}` — เขียนตอนเช็คอิน ลบตอนจบงาน |

`activeTruck` จำเป็นเพราะ Firestore rules **query task ไม่ได้** — maintenance gate เดิมอ่าน `currentAssignment` อย่างเดียว ถ้าคนขับวิ่งรถ B แต่ผูกรถ A จะโดน permission-denied

**Flow ใหม่:** แอดมินเลือก **ประเภทรถ → ทะเบียน (จาก fleet)**, driver dropdown แสดง**ทุกคน**; คนขับ**ยืนยันหรือแก้รถได้ตอนเช็คอิน**; คนขับที่สร้างงานเอง**ต้องเลือกรถ** (เฉพาะรถของบริษัท/พาร์ทเนอร์ตัวเอง ตาม `subcontractorId`)

**ไฟล์หลัก:**

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `logitrack-web/lib/truckType.ts` (+ `.test.ts`) | **ใหม่ — SSOT** map `trucks.type` ("6 Wheels") → task enum ("6WH"); ไม่รู้จัก = คืน `undefined` (ห้ามเดา) |
| `logitrack-web/validate/taskSchema.ts` | เพิ่ม `truckId`, export `TASK_TRUCK_TYPE_ENUM` |
| `logitrack-web/validate/driverSchema.ts` | เพิ่ม `activeTruck` + `currentAssignment.truckModel` |
| `shared-docs/schemas/taskSchema.ts` | **แก้ของเก่าที่ stale** (enum `4WH`/`PICKUP` + ขาด jobCategory/runOrder/helperDriverIds) |
| `features/tasks/components/TruckPlateField.tsx` | **ใหม่** — combobox ค้นหาทะเบียนจาก fleet (filter ตามประเภทรถ) |
| `FirstMileTaskDialog.tsx` / `LineHaulTaskDialog.tsx` | ลบ binding filter ออกจาก driver dropdown; plate เป็น combobox; เลือกทะเบียนแล้ว derive `truckType` จาก truck doc |
| `functions/src/tasks.ts` | รับ `truckId`/`licensePlate`/`driverName`/`driverPhone`; **strip `undefined` ก่อน write** (Admin SDK reject undefined; เดิม `driverName: undefined` ค้างอยู่) |
| `app/app/{first-mile,line-haul}/import-dialog.tsx` | resolve ทะเบียน → fleet (`truckId`); ทะเบียนที่ไม่มีในระบบ = reject พร้อมเหตุผล (ทะเบียนว่าง = import ได้ ค่อย assign ทีหลัง); แก้ header collision `plate` |
| `firestore.rules` | maintenance gate อ่าน `activeTruck.truckId` ‖ `currentAssignment.truckId` (null-safe ด้วย `is map`) |
| `logitrack-mobile/lib/core/utils/truck_type.dart` | **ใหม่** — mirror ของ `lib/truckType.ts` |
| `logitrack-mobile/lib/features/home/data/repositories/trucks_repository.dart` | **ใหม่** — รถของบริษัทตัวเอง (scope `subcontractorId`) |
| `logitrack-mobile/lib/components/truck_picker_field.dart` | **ใหม่** — ตัวเลือกรถ (ค้นหาทะเบียน) |
| `check_in_page.dart` | **2 flow:** งานที่แอดมินมอบ (default = รถของ task, แก้ได้) + สร้างงานเอง (บังคับเลือกรถ); เขียน `activeTruck` |
| `driver_repository.dart` | `setActiveTruck()` / `clearActiveTruck()` (ใช้ `FieldValue.delete()` ไม่ใช่ null) |
| `vehicle_expense_page.dart` | อ่าน `activeTruck` ก่อน `currentAssignment` |
| `delivery_trip_repository.dart` | ล้าง `activeTruck` ตอนจบงาน (ทั้ง single + multi-stop) |

**Pattern:** ดู `.vibe-rules.md` → Confirmed Patterns → "🚚 Vehicle identity" (MANDATORY)

---

#### 41. Rate Card: Vehicle Class ตรงกับที่ task ถืออยู่ — [14–15 ก.ค. 2026]

ต่อจาก #40 — rate card เคยเสนอ "ชื่อประเภทรถ" จาก truck master ซึ่งไม่ตรงกับ enum ที่ task ใช้ ทำให้ lookup ราคาไม่เจอ

- `rate-card`: เสนอ **task vehicle classes** (ไม่ใช่ชื่อจาก truck master), dedup + เรียงตาม task enum, badge สี Vehicle/Job type, เติม i18n ที่ขาด
- `billing`: fold legacy vehicle classes ลงบน class ที่ task ถือจริง
- `tasks` import: รับคอลัมน์ **หลัก/เสริม** (`jobCategory`) จาก Excel template
- Select ใน edit-entry dialog ยกขึ้นเหนือ overlay (pattern z-index เดิม — ดู #9)

**Commits:** `52e4f40`, `dbad116`, `6c1cb56`, `53455ce`, `766675f`, `6a265df`, `f9201be`, `da631ef`

---

#### 42. Driver Monitor: คอลัมน์เช็คอิน/ออกเดินทาง + แก้ หลัก/เสริม บนเที่ยวที่ส่งแล้ว — [15–16 ก.ค. 2026]

- Driver Monitor เพิ่มคอลัมน์ check-in + depart, ถอด estimated revenue ออก (`7853401`) — ดู **ADR 0001**
- แก้ `jobCategory` (หลัก/เสริม) บนเที่ยวที่ **delivered แล้ว** และ re-derive ราคาแบบ atomic (`71446cc`) — ดู **ADR 0002**
- Fix: ปุ่ม Save เปิดใช้งานเมื่อแก้แค่ job category (`469cfc1`)
- Mobile: คง session ไว้เมื่อ token refresh ล้มแบบชั่วคราว แทนที่จะเตะออก (`803881c`, v2.9.2+1)

---

#### 43. ฟอร์มต้อง fail ดังๆ — validation errors ไม่เงียบอีกต่อไป — [17 ก.ค. 2026]

**อาการ:** แก้โปรไฟล์คนขับแล้วกด Save ไม่มีอะไรเกิดขึ้น ไม่มี error — เพราะ doc เก่าขาดฟิลด์ที่ schema ใหม่บังคับ react-hook-form จึงบล็อก submit เงียบๆ

- `ac1070d` — surface validation errors ให้เห็นจริง (ADR 0003)
- `3fef379` — **`onInvalid` handler กลางตัวเดียว** ใช้ร่วมทุกฟอร์ม ไม่ให้ฟอร์มไหน save เงียบได้อีก (ADR 0004) — test: `lib/formInvalidHandler.test.ts`

**Pattern:** ฟอร์มใหม่ทุกตัวต้องผูก shared `onInvalid` handler — อย่าเขียน handler เองต่อฟอร์ม

---

#### 44. ฟิลเตอร์ทะเบียนรถ + ต้นทาง/ปลายทาง — [22–23 ก.ค. 2026] — V2.10.0-web

- **ทะเบียนรถ** ใน Billing Document + Driver Monitor (ADR 0005) — helper `lib/truckPlate.ts` (+ test)
- **ต้นทาง/ปลายทาง** ใน Driver Monitor (ADR 0006) — helper `lib/placeFilter.ts` (+ test)
- `27896ee` — ปุ่มล้างฟิลเตอร์ + date range picker ทั้ง Driver Monitor และ Billing Document

---

#### 45. CI: ปลด lint block จาก agent tooling — [27 ก.ค. 2026]

**อาการ:** CI บน `main` แดง 2 รันติด (23 ก.ค.) → job Deploy ถูก `skipped` (deploy.yml รอ `workflow_run.conclusion == 'success'`) → **dev auto-deploy หยุดตั้งแต่ 17 ก.ค.**

**สาเหตุ:** ESLint 14 errors — ไม่มีอันไหนอยู่ในโค้ดแอปเลย ทั้งหมดมาจาก AI agent tooling ที่ commit ไว้ใต้ `logitrack-web/`:

| ที่มา | error |
|------|-------|
| `.agents/skills/wds-5-.../dev-mode.js` + `.claude/skills/wds-5-.../dev-mode.js` | rule `n/no-unsupported-features/node-builtins` ไม่มีนิยาม (ไม่ได้ติดตั้ง `eslint-plugin-n`) |
| `_bmad/wds/scripts/wds-*.js` (6 ไฟล์) | `@typescript-eslint/no-require-imports` |

**แก้:** `logitrack-web/eslint.config.mjs` — เพิ่ม `_bmad/**`, `.claude/**`, `.agents/**` เข้า `globalIgnores` → เหลือ 0 errors / 518 warnings

**หมายเหตุ:** `.claude/` **ที่ root ไม่เกี่ยว** — นั่นคือ tooling ของโปรเจกต์จริง (spec-architect agent, `/spec-new`, `/spec-build`, firebase skills) ต้อง track ต่อ; ที่ ignore คือชุด BMAD/WDS ที่ติดตั้งซ้ำเข้าไปใน `logitrack-web/` เท่านั้น

---

#### 46. Mobile Forced-Update Pipeline — [28 ก.ค. 2026] — ดู **ADR 0007**

build → ประกาศเวอร์ชันขึ้น Firestore อัตโนมัติ → แอดมินเห็นว่าใครตกรุ่น → กดปุ่มบังคับเมื่อพร้อม

**หลักการที่ห้ามพัง:** `settings/mobile_app` มี **2 writer ที่เขียน field คนละชุด** — script **ห้ามแตะ `minAllowedVersion`** (มี assert ในโค้ด) เพราะนั่นคือ field เดียวที่ mobile อ่านเพื่อบล็อก การ publish บิลด์จึงล็อกใครไม่ได้ ต้องมีคนกดปุ่ม

| ไฟล์ | สิ่งที่เปลี่ยน |
|------|---------------|
| `package.json` (root) | **🐛 แก้ flavor bug** — เติม `--flavor prod --dart-define=FLAVOR=prod`, ถอด `--split-per-abi`, เพิ่ม `build:mobile:dev` + 4 script `release:mobile:*` |
| `logitrack-web/scripts/publish-mobile-release.mjs` | **ใหม่** — อ่าน version จาก pubspec, เลือก APK จาก `output-metadata.json`, sha256 แบบ stream, อัป Storage, HEAD เช็คลิงก์ก่อนเขียน Firestore; `--project=dev\|prod` required |
| `logitrack-web/storage.rules` | block `app_releases/**` — `read: true` / **`write: false`** (writer เดียวคือ Admin SDK) |
| `logitrack-web/lib/mobileVersion.ts` (+ test) | **ใหม่** — เทียบ semver เขียนเอง ไม่ลง `semver`; 24 tests เคสสำคัญ `2.10.0 > 2.9.3` |
| `logitrack-web/lib/firestoreWrite.ts` | **ใหม่** — ยก `stripUndefined` ออกจาก `companies.ts` มาใช้ร่วม |
| `logitrack-web/app/app/security-center/mobile-release/page.tsx` | **ใหม่** — หน้าคุมเวอร์ชัน + ปุ่มบังคับ |
| `logitrack-web/features/mobile-release/api/mobileAppSettings.ts` | **ใหม่** — `setDoc(merge)` ไม่ใช่ `updateDoc` (doc อาจยังไม่มี) |
| `logitrack-web/app/app/security-center/mobile-clients/page.tsx` | badge blocked/outdated/ahead + สรุปเหนือตาราง + **badge แดงเมื่อ `flavor === "dev"`** |
| `logitrack-mobile/.../mobile_app_version_service.dart` | cache floor ลง SharedPreferences (TTL 30 วัน) ปิดช่องหลบด้วย airplane mode |
| capability + i18n | `security_manage_mobile_release` (admin only) 6 จุด + en/th ครบ (337 คีย์เท่ากันทั้งสองภาษา) |

**🔴 Guard ที่สำคัญที่สุด:** ปุ่มบังคับ**กดไม่ได้ถ้า `apkDownloadUrl` ว่าง** — mobile render ปุ่มดาวน์โหลดเฉพาะเมื่อ URL ไม่ว่าง บังคับโดยไม่มีลิงก์ = คนขับติด dialog ที่ออกไม่ได้

**⚠️ Sequencing:** gate hardening เป็นโค้ด mobile → ไปกับ APK ตัวถัดไป **ป้องกัน force ครั้งหน้า ไม่ใช่ครั้งแรก**

---

### ⚠️ สิ่งที่ยังค้างอยู่ (Pending)

1. **RBAC — กำหนด `security_view_mobile_clients` ให้ role ใน Firestore `permissions_config`**  
   ยังต้องทำผ่าน Role Matrix UI ใน Admin dashboard (ไม่มี seed script)

2. ~~**"เวอร์ชันล่าสุดที่ปล่อย" จาก CI**~~ — **ทำแล้วบางส่วน (#46 / ADR 0007)**  
   `scripts/publish-mobile-release.mjs` เขียน `latestVersion` ลง `settings/mobile_app` และหน้า Mobile Clients มี badge blocked/outdated/ahead แล้ว  
   **ที่ยังเหลือ:** ต้องรัน script เองบนเครื่อง — ยังไม่มี CI build mobile (ดูข้อ 5) จึงยังไม่ auto จากทุก build

3. **OTA / APK Auto-update**  
   ยังไม่ implement — flow ปัจจุบันคือบังคับให้ผู้ใช้กดโหลด APK เอง (ADR 0007) ไม่ใช่ OTA จริง

4. **Standby Transaction: Mobile + Billing integration**  
   Web admin page สร้างแล้ว แต่ Mobile flow ("Standby งานหมด" button + StandbyPage), billing line item, และ rate ยังไม่ implement

5. **Mobile ไม่มี CI เลย**  
   `.github/workflows/ci.yml` filter paths แค่ `logitrack-web/**` + `shared-docs/**` → `flutter analyze` / `flutter test` ไม่เคยรันบน CI  
   สถานะปัจจุบัน (27 ก.ค. 2026): **0 errors, 17 warnings, 102 infos** — warning ที่ควรเก็บ เช่น dead code + `dead_null_aware_expression` ใน `delivery_phase_page_multi.dart:298,311`, unused import ใน `loading_phase_page.dart` / `standby_page.dart`

6. **`dart format` drift ฝั่ง mobile**  
   60 ไฟล์ใต้ `lib/` ยังไม่ตรงกับ `dart format` (บางไฟล์ต่าง 100–300 บรรทัด) → **ห้ามรัน format ปนกับ commit ฟีเจอร์** เพราะ diff จะกลบเนื้อจริง ควรทำเป็น commit แยกล้วนๆ ครั้งเดียว

7. **GitHub Actions ใช้ Node 20 (deprecated)**  
   `actions/checkout@v4`, `setup-node@v4`, `pnpm/action-setup@v4` ถูก force ไปรันบน Node 24 แล้วและขึ้น deprecation warning ทุกรัน — ยังไม่พัง แต่ควรอัป

8. ~~**BMAD/WDS tooling ถูก track ใต้ `logitrack-web/` (261 ไฟล์)**~~ — **เอาออกแล้ว (9 ส.ค. 2026 — ADR 0017)**  
   ลบ `logitrack-web/.claude/skills/` + `_bmad/` + `.agents/` (บนดิสก์จริง ~2,141 ไฟล์ เพราะส่วนใหญ่ถูก `*.md` gitignore ไม่ได้ track) — เก็บ `logitrack-web/.claude/settings.json` + `settings.local.json` ไว้ (ไม่ใช่ BMAD). ADR ของ BMAD 6 ตัว migrate เข้า canonical `shared-docs/adr/0011–0016`, glossary fold เข้า `shared-docs/glossary.md`, ส่วน PRD/architecture/epics/stories/decision-log ย้ายไป `shared-docs/driver-compensation/`. ดู **ADR 0017** และ #47.  
   **หมายเหตุ:** skills ชุด `bmad-*` ยังติดตั้ง global อยู่ (`~/.claude/skills/`) → ยังโผล่ในเมนู skill (การลบระดับเครื่องเป็น opt-in แยก); `~/.claude/commands/` ไม่มี WDS leak. ยังไม่ได้ใส่ `.gitignore` กัน `_bmad-output/` ถูกสร้างซ้ำ (follow-up ใน ADR 0017)

---

### 🔒 Firestore Security Rules สำหรับ `mobile_installations`

เพิ่มใน `logitrack-web/firestore.rules` ภายใต้ `drivers/{driverId}`:

```
match /mobile_installations/{installId} {
  allow read: if isAdmin();
  allow create, update: if isAppCheckVerified()
    && request.auth != null
    && get(/databases/$(database)/documents/drivers/$(driverId)).data.authId == request.auth.uid
    && request.resource.data.driverId == driverId;
  allow delete: if false;
}
```

---

### 📁 ไฟล์สำคัญที่ควรอ่านก่อน

1. `shared-docs/.vibe-rules.md` — กฎทั้งหมด, Tech Stack, Patterns, Change Log
2. `logitrack-web/firestore.rules` — Firestore security rules ทุก collection
3. `logitrack-web/lib/capabilities.ts` — RBAC capabilities ทั้งหมด
4. `logitrack-mobile/lib/core/services/` — Services ใหม่ทั้ง 3 ตัว (mobile client monitor)
5. `logitrack-web/app/app/security-center/mobile-clients/page.tsx` — หน้า monitor mobile ใหม่
6. `logitrack-web/lib/app-version.ts` — Web UI version (อ่านจาก package.json)
7. `logitrack-web/lib/hubDisplay.ts` — ป้าย Hub/SOC สำหรับ FM / LH / Sources
8. `logitrack-web/validate/hubSchema.ts` + `validate/taskSchema.ts` — กลุ่ม SPX/SPK และ SOC helpers
9. `logitrack-web/functions/src/core/distances.ts` + `functions/src/distances.ts` — Hub–SOC Matrix (callable)
10. `logitrack-web/app/app/first-mile/hub-dialog.tsx` — เพิ่ม/แก้จุดรับงาน (hubs); Select ใน Dialog ต้อง `z-[1005]` + `position="popper"` ให้คลิกเมนูได้
11. `logitrack-web/app/app/accounting/toll-expense-import-dialog.tsx` — import ค่าผ่านทางจากไฟล์ และ fallback map คอลัมน์
12. `logitrack-web/app/app/accounting/actions.client.ts` — source of truth สำหรับอ่าน/เขียน `vehicle_expenses` และ rate card/fuel adjustments ฝั่ง web admin
13. `logitrack-web/app/app/accounting/rate-card/page.tsx` — หน้า Rate Card หลัก (filters/manual add/fuel rules/template export)
14. `logitrack-web/features/maintenance/utils/maintenanceDisplayCost.ts` + `MaintenanceDashboard.tsx` / `MaintenanceOverview.tsx` / `maintenance/MaintenanceHistoryList.tsx` — ยอดซ่อมบำรุงบน Web (รวม `invoiceAmount` จากคนขับเมื่อไม่มี `totalCost`)
15. `logitrack-web/lib/billingCompute.ts` + `functions/src/core/billingCompute.ts` — Pure billing logic ที่ต้อง sync ทั้งสองไฟล์ (`normalizeDestinationCode`, `selectBillingRateEntry` with fallback)
16. `logitrack-web/functions/src/tripBillingOnDelivered.ts` — Callable `computeTripBillingSnapshot` + `backfillTripBillingSnapshots`; `buildHubMaps()` แยก `nameToCode` / `codeToName` — ห้าม merge เป็น map เดียว (cause: code แปลงกลับเป็นชื่อ → No rate)
17. `logitrack-web/app/app/accounting/income/page.tsx` — หน้า Income หลัก (names display, pagination, export, backfill stats, missing billing tab)
18. `logitrack-web/functions/src/backfillCustomerLinks.ts` — Cloud Function backfill ลูกค้าให้งานเก่า
19. `logitrack-web/app/app/utilities/backfill/page.tsx` — หน้า Utilities สำหรับรัน backfill
20. `logitrack-mobile/lib/features/delivery_phase/presentation/pages/delivery_phase_page_multi.dart` — หน้า delivery หลายจุด (J&T only)
21. `features/drivers/components/DriverMonitorDashboard.tsx` — Driver Monitor แสดง multi-stops + "On run" badge
22. `features/tasks/components/DeliveryStopsEditor.tsx` — Editor สำหรับจัดการจุดส่ง
23. `logitrack-web/hooks/useCustomerScope.ts` — Hook อ่าน customerScopeId จาก JWT (V2.4.0-web)
24. `logitrack-web/components/page-permission-guard.tsx` — Route/page permission guard component (V2.4.0-web)
25. `logitrack-web/functions/src/users.ts` — Cloud Function users: customerScopeId support + toggle disabled (V2.4.0-web)
26. `logitrack-web/app/app/security-center/users/page.tsx` — User management: Edit modal, CustomerScopeCell, Toggle enable/disable (V2.4.0-web)
27. `logitrack-web/app/app/layout.tsx` — Layout หลัก: integrate PagePermissionGuard + forceLogout Firestore listener (V2.4.0-web)
28. `logitrack-web/app/app/standby-records/page.tsx` + `standby-backfill-dialog.tsx` — Standby Records admin view + backfill dialog
29. `.github/workflows/ci.yml` + `.github/workflows/deploy.yml` — GitHub Actions CI (type-check/lint/test) + Firebase Hosting deploy
30. `logitrack-web/eslint.config.mjs` — ESLint config: no-explicit-any=warn, ignore functions/lib compiled output
31. `logitrack-web/lib/permissions.ts` — canAccessRoute แก้ให้ public routes คืน true เสมอ; getDefaultRouteForRole()
32. `logitrack-web/vitest.config.ts` — exclude functions/** and node_modules/** from Vitest
33. `logitrack-web/lib/driverName.ts` — helper `driverDisplayName()` priority fullNameTh→name→firstName+lastName→email→id; ใช้ทุกรายงาน/billing
34. `logitrack-web/lib/hubDisplay.ts` — `primaryHubLabelFromFirestoreData` (UI: TH→EN→code) + `billingHubLabelFromFirestoreData` (billing docs: EN→TH→code)
35. `logitrack-web/functions/src/backfillTripTruckData.ts` — callable 3-pass backfill truck identifiers สำหรับ trip_records/vehicle_expenses/tasks
36. `logitrack-mobile/lib/features/vehicle_expense/presentation/pages/vehicle_expense_page.dart` — `_fetchFreshDriverData()` re-fetch ก่อนเปิด expense form
