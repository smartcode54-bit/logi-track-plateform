# Spec: Mobile — driver self-download of trip photos

> **Status:** ✅ Done (code implemented 2026-08-22; runtime AC pending device QA — no mobile CI)
> **Owner:** Samart Kas
> **Created:** 2026-08-22
> **Domain:** trip_history (logitrack-mobile)
> **Related:** [ADR 0018](../adr/0018-driver-self-download-trip-photos.md) · glossary: Evidence photo, Photo type, Assigned round

---

## 1. Problem & Goal (ทำไมต้องทำ)

พขร. อยากได้สำเนา **รูปหลักฐานการทำงาน** (loading/delivery) ของตัวเองเก็บไว้ในเครื่อง แต่วันนี้ **ไม่มีหน้าไหนในแอปให้ดูรูปของเที่ยวที่ทำเสร็จเลย** — หน้า Trip History เป็นข้อความล้วน (`trip_history_page.dart:586` ไม่แตะ `.photos`) และ `JobRecordPage` เป็น mock. เป้าหมาย: เปิดหน้าดูรูปต่อเที่ยว + ปุ่มเดียว "ดาวน์โหลดทั้งเที่ยว" ลงแกลเลอรี เรียงตามขั้นตอนการทำงาน เฉพาะเที่ยวของตัวเอง.

## 2. Scope

**In scope:**
- หน้าดูรูป **ต่อเที่ยว** เปิดจากการแตะแถวเที่ยว First Mile / Line Haul ในหน้า Trip History
- ปุ่มเดียว **"ดาวน์โหลดทั้งเที่ยว"** (bulk) → เซฟทุกรูปของเที่ยวลงแกลเลอรี อัลบั้ม `LogiTrack`
- **รูป incident ของเที่ยวนั้นด้วย** — ถ้ามี `incidentReport` ผูกกับเที่ยว (`tripId == trip.id`) ให้แสดง + โหลดเข้าอัลบั้มเดียวกัน (ส่วนต่อ ADR 0018 ตามที่เจ้าของสั่ง 2026-08-22)
- เรียงรูปตาม **workflow rank** (loading → single delivery → multi-stop by index → incident) — ไม่ใช่ลำดับใน array
- ชื่อไฟล์ฝัง **Trip ID + [[Assigned round]]** (จาก `task.date`+`task.time` ผ่าน `trip.taskId`, fallback `trip.createdAt`)
- Best-effort: บางรูปพลาด (404/เน็ตหลุด) ไม่ล้มทั้งชุด → รายงาน "เซฟ X/Y"
- Permission add-only photo access + จอ deny (เปิด settings) — ไม่ crash
- i18n `en` + `th`

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- รูปของ `standby_records` (ADR 0018 ยังจำกัดไว้; incident ถูกดึงเข้า scope แล้ว ดูด้านบน)
- ดาวน์โหลดรายรูป / share sheet / เปิด url ในเบราว์เซอร์ (ถูก reject ใน ADR)
- Server proxy / audit log / ล็อก Storage read (ADR เลือก in-app scope เท่านั้น)
- แก้ Firestore rules หรือ Storage rules (read เปิดอยู่แล้ว `storage.rules:35`)
- Web admin (ฟีเจอร์นี้ mobile-only)

## 3. Requirements

**Functional**
- **R1.** แตะแถวเที่ยว **First Mile / Line Haul** ในหน้า Trip History → เปิดหน้า `TripPhotosPage(trip)`.
- **R2.** หน้าแสดงรูปของเที่ยวนั้นทั้งหมดจาก `TripRecord.photos` **+ รูป incident ที่ผูกกับเที่ยว** เป็น thumbnail เรียงตาม **workflow rank** (ดู Design); รูป incident แสดงเป็นกลุ่มแยกท้ายสุด. แตะ thumbnail เปิดดูเต็มจอ (zoom).
- **R3.** ปุ่ม **"ดาวน์โหลดทั้งเที่ยว"** เซฟทุกรูปของเที่ยว **รวมรูป incident** ลงแกลเลอรี อัลบั้ม `LogiTrack`.
- **R4.** ดาวน์โหลด/เซฟ **ตามลำดับ workflow**; ชื่อไฟล์ trip = `LogiTrack_{tripId}_{roundStamp}_{NN}-{type}`, ชื่อไฟล์ incident = `LogiTrack_{tripId}_{roundStamp}_{NN}-incident{S}-{type}` (`{S}` = ลำดับ report เมื่อมีหลายใบ, `{type}` = `map`/`situation1`/`situation2`). `NN` = 2 หลักของ workflow rank; `roundStamp` = [[Assigned round]] รูปแบบ `yyyyMMdd-HHmm`.
- **R5.** `roundStamp` มาจากการดึง task ผ่าน `trip.taskId` (อ่าน `task.date`+`task.time`) **ตอนกดโหลด** (1 read/tap). ถ้าไม่มี `taskId` หรืออ่าน task ไม่ได้ → fallback `trip.createdAt`. รูปแบบเวลาไม่ครบต้องไม่บล็อกการโหลด.
- **R6.** Best-effort: fetch+save แต่ละรูปแยกกัน — พลาดรูปหนึ่งไม่หยุดที่เหลือ. จบแล้วรายงาน **"เซฟ X/Y รูป"**; ถ้า X=0 แสดง error ให้ลองใหม่.
- **R7.** หน้าดูรูปแสดงเฉพาะเที่ยวของ พขร. เอง — สืบทอดจาก query เดิม (`trip_history_page.dart:66-78` → `getTripHistoryByDriver`, `trip_records_repository.dart:83-107`). ไม่มี read surface ใหม่.
- **R8.** ครอบ `trip_records` (First Mile + Line Haul) **+ `incidentReport` ที่ผูกด้วย `tripId`**. ไม่แตะการ์ด Standby.
- **R9.** ก่อนเซฟ ขอ permission add-only (`Gal.requestAccess(toAlbum: true)`). ถ้า deny → snackbar/dialog อธิบาย + ปุ่ม "เปิดการตั้งค่า". ไม่ crash.
- **R10.** ปุ่มดาวน์โหลด **ซ่อน/disable** เมื่อเที่ยวมี 0 รูป (นับรวม trip photos + incident photos).
- **R11.** ดึง incident ของเที่ยวด้วย query `collection('incidentReport').where('tripId', isEqualTo: trip.id)` (`incidentReportCollection = 'incidentReport'` — camelCase, ไม่ใช่ `incident_reports`). แต่ละ report แปลง 3 ฟิลด์ URL (`mapPhotoUrl`/`situation1PhotoUrl`/`situation2PhotoUrl`) เป็น `{url, type}` เฉพาะที่ไม่ว่าง; เที่ยวที่ไม่มี incident → ไม่เพิ่มอะไร (ไม่ error). Incident ที่ `tripId == null` ไม่ผูกเที่ยว → ไม่ปรากฏ (ถูกต้อง).

**Non-functional** (perf / security / i18n / cost)
- **N1.** i18n ครบทั้ง `en` และ `th` (flat keys ใน `assets/translations/{en,th}.json`, easy_localization `.tr()`).
- **N2.** ไม่แก้ Firestore/Storage rules; read เปิดอยู่แล้ว; ขอบเขตความปลอดภัย = in-app scope (own trips) เท่านั้น.
- **N3.** เพิ่ม dependency `gal` (^2.3.x). iOS: `Info.plist` `NSPhotoLibraryAddUsageDescription`. Android: `gal` จัดการ MediaStore เอง (API 33+ ไม่ต้องขอ permission; API <29 ต้อง `WRITE_EXTERNAL_STORAGE` ใน manifest — ตาม `gal` docs).
- **N4.** `dart analyze` ไม่มี error ใหม่. ทำตามแพตเทิร์นเดิมของ feature `trip_history` = `StatefulWidget`/`setState` (ไม่บังคับ Bloc — ให้เข้ากับโค้ดรอบข้าง).
- **N5.** bump `logitrack-mobile/pubspec.yaml` `version` (minor + build number) ตาม versioning ร่วม web+mobile.

## 4. Design

> สอดคล้อง feature architecture ฝั่ง mobile: `lib/features/<domain>/{presentation,data}` และแพตเทิร์นเดิมของ `trip_history`.

**Data model (Firestore)**
- **ไม่มี field/collection ใหม่ — read only.** ใช้ `trip_records.photos[]` = `{url, type, geocoding}` ที่มีอยู่ (`trip_record.dart:274`). อ่าน `tasks/{taskId}` `.date` + `.time` ตอนโหลด (R5). อ่าน `incidentReport` ผ่าน query `tripId == trip.id` (R11).
- **ไม่มี denormalize** — [[Assigned round]] ดึงสด ไม่คัดลอกลง trip (เหตุผลเดียวกับ ADR 0001/0010).
- **ความครบของรูป trip:** `TripRecord.photos` = ชุดเต็มอยู่แล้ว (multi-stop merge เข้า flat array ที่ `delivery_trip_repository.dart:187-195`; model อ่าน flat ที่ `trip_record.dart:101`) → ไม่ต้องแก้ model/query.
- **รูป incident รูปทรงต่างจาก trip:** เก็บเป็น **3 ฟิลด์ URL** บน doc (`mapPhotoUrl`/`situation1PhotoUrl`/`situation2PhotoUrl`, nullable — `incident_report_repository.dart:81-96`), ไม่ใช่ array. ต้องมี adapter แปลงเป็น `{url, type}` (เฉพาะที่ไม่ null). Storage `incident_reports/**` read เปิด (`storage.rules:41`).

**Workflow rank (SSOT ใหม่ ฝั่ง mobile)**
สร้าง helper (เช่น `tripPhotoWorkflowRank(String type) -> int`) จัดลำดับ:

| ช่วง | types (ตามลำดับ) |
|------|------------------|
| 1. Loading | `runsheet`, `runsheet_extra_1`, `runsheet_extra_2`, `runsheet_extra_3`, `pre_close`, `closing`, `seal` |
| 2. Delivery (single) | `pre_open`, `opening`, `empty_container`, `runsheet_received` |
| 3. Multi-stop | `stop_{index}_{type}` — เรียง `index` น้อย→มาก, ภายใน stop เรียง `pre_open, opening, empty_container, runsheet_received` |
| 4. Incident | ต่อ report (เรียง `createdAt`): `map, situation1, situation2` — เป็นกลุ่มแยกท้ายสุดของเที่ยว |
| 5. Unknown/legacy | ไม่รู้จัก → ต่อท้าย (stable) |

Ground: loading types `loading_phase_page.dart:32,36`; delivery `delivery_phase_page.dart:22`; multi `stop_{index}_{type}` (`delivery_phase_page_multi.dart`); incident types `map/situation1/situation2` (`incident_report_repository.dart:56-79`). **เหตุผลที่ต้อง sort เอง:** trip array คือ insertion/replace order (`mergeTripPhotosReplacingTypes`, `delivery_trip_repository.dart:187`) ไม่ใช่ลำดับงาน; incident เป็นคนละ collection จึงต่อท้ายเป็นกลุ่ม.

**Cloud Functions / billing**
- ไม่แตะ. (ไม่เกี่ยว billing — ไม่ต้อง sync `lib/billingCompute.ts` / `functions/src/core/billingCompute.ts`.)

**Web (Next.js)**
- ไม่มี.

**Mobile (Flutter)**
- **หน้าใหม่:** `lib/features/trip_history/presentation/pages/trip_photos_page.dart` — grid thumbnails (เรียงตาม rank), ปุ่ม "ดาวน์โหลดทั้งเที่ยว", full-screen viewer (`InteractiveViewer` + `Image.network`), progress + ผลลัพธ์ X/Y.
- **Service ใหม่:** `lib/features/trip_history/data/services/trip_photo_download_service.dart` —
  - รวมรายการรูป = trip photos (จาก `TripRecord.photos`) **+ incident photos** (จาก query `tripId == trip.id`, adapter 3 ฟิลด์ → `{url, type}`) แล้ว sort ตาม rank,
  - `fetch bytes` ต่อรูปด้วย `http.get(Uri.parse(url))` (http มีใน pubspec แล้ว; url เป็น download URL สาธารณะ ทั้ง trip และ incident),
  - `Gal.putImageBytes(bytes, album: 'LogiTrack', name: <ชื่อไฟล์ R4>)`,
  - คืน result `{saved:int, total:int, failures:List<String>}`.
- **Repo/adapter ใหม่:** อ่าน incident ของเที่ยว — `collection('incidentReport').where('tripId', isEqualTo: trip.id).get()` → map เป็น `{url, type, createdAt}` (เฉพาะ URL ที่ไม่ null). วางใน `trip_history/data/` (เช่น `trip_incident_photos.dart`).
- **Helper ใหม่:** `tripPhotoWorkflowRank` + `roundStamp` resolver (อ่าน task ผ่าน `taskId`, fallback `trip.createdAt`). วางไว้ใน `trip_history/data/` (เช่น `trip_photo_order.dart`).
- **แก้:** `trip_history_page.dart` — เพิ่ม `onTap` บน `ListTile` ของแต่ละเที่ยว (`_SectionCard`, ~บรรทัด 586) → `Navigator.push` ไป `TripPhotosPage(trip: t)`.
- **pubspec.yaml:** เพิ่ม `gal: ^2.3.x`; bump `version`.

**Firestore Rules**
- ไม่มี (read เปิดอยู่แล้ว, own-trip scope บังคับที่ UI).

**i18n keys (flat, `assets/translations/en.json` + `th.json`)**

| key | en | th |
|-----|----|----|
| `trip_photos_title` | Trip photos | รูปเที่ยว |
| `trip_photos_empty` | No photos for this trip | เที่ยวนี้ยังไม่มีรูป |
| `trip_photos_download_all` | Download all | ดาวน์โหลดทั้งหมด |
| `trip_photos_downloading` | Saving photos… | กำลังบันทึกรูป… |
| `trip_photos_saved` | Saved {saved}/{total} to LogiTrack album | บันทึก {saved}/{total} ลงอัลบั้ม LogiTrack แล้ว |
| `trip_photos_save_none` | Couldn't save any photos. Check your connection and try again. | บันทึกรูปไม่สำเร็จ ตรวจสอบอินเทอร์เน็ตแล้วลองใหม่ |
| `trip_photos_permission_denied` | Allow photo access to save images | อนุญาตการเข้าถึงรูปภาพเพื่อบันทึก |
| `trip_photos_permission_settings` | Open settings | เปิดการตั้งค่า |

(`{saved}`/`{total}` ผ่าน `.tr(namedArgs: {...})`.) Step-label ต่อ thumbnail = optional (ดู §9).

## 5. Affected files

**ใหม่**
- `logitrack-mobile/lib/features/trip_history/presentation/pages/trip_photos_page.dart` (grid: trip section + incident section)
- `logitrack-mobile/lib/features/trip_history/data/services/trip_photo_download_service.dart`
- `logitrack-mobile/lib/features/trip_history/data/trip_photo_order.dart` (rank + roundStamp helper)
- `logitrack-mobile/lib/features/trip_history/data/trip_incident_photos.dart` (query `incidentReport` by tripId + adapter)

**แก้**
- `logitrack-mobile/lib/features/trip_history/presentation/pages/trip_history_page.dart` (ListTile onTap → push)
- `logitrack-mobile/pubspec.yaml` (`gal` + version bump)
- `logitrack-mobile/ios/Runner/Info.plist` (`NSPhotoLibraryAddUsageDescription`)
- `logitrack-mobile/android/app/src/main/AndroidManifest.xml` (WRITE_EXTERNAL_STORAGE maxSdk 28 ตาม gal docs)
- `logitrack-mobile/assets/translations/en.json`
- `logitrack-mobile/assets/translations/th.json`

## 6. Task breakdown
- [x] **T1.** เพิ่ม `gal ^2.3.0` ใน pubspec (`flutter pub get` → gal 2.3.3); iOS `NSPhotoLibraryAddUsageDescription` + Android `WRITE_EXTERNAL_STORAGE` maxSdk 28.
- [x] **T2.** `trip_photo_order.dart`: `tripPhotoWorkflowRank(type)` + `incidentPhotoRank(seq,type)` (group 4) + `resolveAssignedRoundStamp(trip)` (fetch task, fallback createdAt).
- [x] **T2b.** `trip_incident_photos.dart`: query `incidentReport where tripId == trip.id` → adapter 3 URL fields → `IncidentPhoto{url,type,reportSeq}` (skip null, order by createdAt).
- [x] **T3.** `trip_photo_download_service.dart`: `loadOrderedTripPhotos` (trip + incident, sorted) + `saveTripPhotosToGallery` (permission gate → http bytes → `Gal.putImageBytes(album:'LogiTrack', name:...)`, best-effort, X/Y result).
- [x] **T4.** `trip_photos_page.dart`: grid (trip section + incident section) + full-screen `InteractiveViewer` + ปุ่มดาวน์โหลด (ซ่อนเมื่อ 0 รูป) + progress + snackbar X/Y + permission-denied action.
- [x] **T5.** `trip_history_page.dart`: ListTile `onTap` → `TripPhotosPage(trip)` (เฉพาะ `_SectionCard` = First Mile/Line Haul; Standby card ไม่มี).
- [x] **T6.** i18n en + th — 9 คีย์ `trip_photos_*` (รวม `trip_photos_incident_section` ที่เพิ่มสำหรับ section header).
- [x] **T7.** bump `pubspec.yaml` `3.2.0+1 → 3.2.0+2` (build number; ชื่อ 3.2.0 คงไว้ตามเลขร่วม).
- [x] **T8.** `.vibe-rules.md` Change Log + spec Status ✅ + ADR 0018 impl note.

## 7. Acceptance criteria (ตรวจรับ)
- [~] **AC1. (R1,R7,R8)** โค้ด: onTap อยู่บน `_SectionCard` ListTile เท่านั้น (First Mile/Line Haul), Standby ไม่มี; query เดิม scope เที่ยวตัวเอง. **ต้อง QA บน device.**
- [~] **AC2. (R2,R11)** โค้ด: viewer แยก section trip/incident, sort ตาม rank. **ต้อง QA บน device.**
- [~] **AC3. (R3,R4)** โค้ด: `saveTripPhotosToGallery` → album `LogiTrack`, ชื่อไฟล์ `LogiTrack_{tripId}_{roundStamp}_{NN}-...` (incident มี `incident{S}`). **ต้อง QA บน device (gallery จริง).**
- [~] **AC3b. (R11)** โค้ด: `fetchIncidentPhotosForTrip` คืน `[]` เมื่อไม่มี/tripId ว่าง → ไม่มี section/error. **ต้อง QA บน device.**
- [~] **AC4. (R5)** โค้ด: `resolveAssignedRoundStamp` ดึง task แล้ว fallback `createdAt`, ไม่ throw. **ต้อง QA บน device.**
- [~] **AC5. (R6)** โค้ด: loop best-effort ต่อรูป, `savedNone` → error copy. **ต้อง QA บน device (เน็ตหลุด).**
- [~] **AC6. (R9)** โค้ด: `Gal.requestAccess(toAlbum:true)` → `PhotoPermissionDeniedException` → snackbar + settings action. **ต้อง QA บน device.**
- [~] **AC7. (R10)** โค้ด: ปุ่ม (bottomNavigationBar) render เฉพาะเมื่อ `_items.isNotEmpty`; ว่าง → `trip_photos_empty`. **ต้อง QA บน device.**
- [x] **AC8. (N1)** 9 คีย์ครบ en + th; JSON parse ผ่านทั้งสองไฟล์.
- [x] **AC9. (N4)** `flutter analyze lib/features/trip_history` = 0 error, 0 issue ใหม่ (4 ไฟล์ใหม่สะอาด; เหลือ 10 infos/warnings เดิมใน `trip_history_page.dart`).

> `[~]` = โค้ดครบตาม requirement + analyzer สะอาด แต่เป็นพฤติกรรม runtime ที่ต้องยืนยันบนเครื่องจริง (ไม่มี mobile CI, รัน device ที่นี่ไม่ได้).

## 8. Risks & rollback
| Risk | Mitigation / rollback |
|------|----------------------|
| `gal` API/permission ต่าง version (เช่น `putImageBytes(name:)`/`requestAccess(toAlbum:)`) | ยืนยัน API กับ version ที่ pin ตอน build; หุ้ม save ด้วย try/catch คืน failure ต่อรูป |
| iOS Photos ไม่โชว์ชื่อไฟล์ที่ตั้ง (จัดตามวันถ่าย) | ชื่อ/round เป็น label อำนวยความสะดวก ไม่ใช่ field ค้นหา — อัลบั้ม `LogiTrack` คือหลักในการหา |
| Android API<29 write ล้มเพราะไม่มี permission | ใส่ `WRITE_EXTERNAL_STORAGE maxSdkVersion=28`; API33+ ไม่ต้องขอ |
| ข้อมูลหลุดออกเครื่องส่วนตัว (พัสดุ/ที่อยู่/ซีล) | ยอมรับตาม ADR 0018 (รูปของ พขร. เอง + read สาธารณะอยู่แล้ว); ถ้าต้อง hard boundary = ADR ใหม่ |
| ผิด collection name (`incidentReport` ≠ `incident_reports`) | ใช้ const `incidentReportCollection`; อย่า hardcode; unit-test query key |
| Incident query ต้องมี index (`tripId ==`) | equality เดี่ยว ไม่ต้อง composite index; ถ้า Firestore ขอ ให้เพิ่ม single-field (ปกติ auto) |
| Rollback | ฟีเจอร์ additive ล้วน (หน้าใหม่ + onTap + dep) — ถอด onTap/หน้า/dep ออกได้โดยไม่กระทบข้อมูลหรือ flow เดิม |

## 9. Open questions / follow-ups
- **Version number:** pubspec บนดิสก์ = `3.1.0+1` แต่ handover ใน CLAUDE.md อ้าง `3.2.0+1` — ยืนยันเลขที่จะ bump จริงก่อน build (ผูกกับ ADR 0007 force-update gate).
- **Step label ต่อ thumbnail** (เช่น "ปิดตู้", "ซีล", "จุดส่ง #1") — optional enhancement; ต้องเพิ่ม i18n map ต่อ type ถ้าทำ. v1 ไม่บังคับ.
- **Package `gal` vs `saver_gallery`** — spec เลือก `gal` (album + add-only access ครบ). เปลี่ยนได้ถ้า build เจอปัญหา iOS.
- หลัง build เสร็จ: ต่อยอด standby/incident photos และใช้หน้านี้เป็น read-only proof view ทั่วไป (นอก scope).

> ⚠️ ไฟล์นี้อยู่ใต้ `shared-docs/specs/` ซึ่งโดน `*.md` gitignore — ตอน commit ต้อง `git add -f shared-docs/specs/mobile-download-trip-photos.md`.
