# Spec: Mobile GO-TEST — core loop บน Go API (flavor `gotest`)

> **Status:** 🔵 In progress (2026-11-18 — โค้ดครบ G1–G9, F1–F9, D1–D2; integration test ผ่านแล้ว; เหลือ F10 ทดสอบบนเครื่องจริงกับ staging ดู §9 "สถานะ build")
> **Owner:** Samart Kas
> **Created:** 2026-11-18
> **Domain:** mobile / operations / auth
> **Related:** `developer-spec.md` §11 (Mobile phase), §4.4, §5.3, §12.2 แถว P7; Appendix B §B.2.21 (`shared-docs/specs/mv-go/B-api-catalog.md`); ADR 0007 (version gate), ADR 0018/0019 (รูป + screenshot), ADR 0028 (ลำดับคิว); PR #239 (base ของ branch `mv-go-T55-mobile-gotest`); งาน T55 (slice แรก)
>
> ⚠️ ไฟล์นี้ถูก ignore โดย rule `*.md` ใน `.gitignore` — ต้อง `git add -f shared-docs/specs/mobile-go-core-loop.md`

---

## 1. Problem & Goal (ทำไมต้องทำ)

แอปคนขับคุยกับ Firebase ตรงทั้งหมด (Firestore / Storage / callables) ส่วน Go API มีแค่ shim targets + heartbeat/settings จึงยังทดสอบ "แอป 4.x คุย Go" กับ staging (`https://logi-api.showkhun.co`, PostgreSQL-owned) ไม่ได้เลย
เป้าหมาย: APK flavor `gotest` ทำ **ฐาน + core loop** ผ่าน Go ได้ครบ — login → โหลดโปรไฟล์ → รายการงาน → เช็คอิน → รับงาน (สร้าง trip + รูป) → ส่งงาน (ปลายทางเดียว) โดย **dev/prod ไม่เปลี่ยนพฤติกรรมเลย** และ contract ฝั่ง server พร้อมสำหรับ slice outbox (T56) โดยไม่ต้องแก้

## 2. Scope

**In scope:**
- **Go (additive):** `GET /v1/mobile/me` แบบ driver token, `GET /v1/mobile/tasks`, `GET /v1/mobile/trucks`, `POST /v1/mobile/uploads/presign`, `POST /v1/mobile/tasks/{id}/check-in`, `POST /v1/mobile/trips`, `POST /v1/mobile/trips/{id}/deliver`, `GET /v1/mobile/trips/pending-in-transit`, `GET /v1/mobile/trips/by-no/{tripNo}`; ต่อ `Idempotency-Key` middleware (มีโค้ดแล้วแต่ยังไม่ถูก wire)
- **Flutter (เฉพาะ `AppFlavor.isGoTest`):** REST client (dio + `QueuedInterceptor`), session (login/refresh/logout/change-password), driver resolve, version gate + heartbeat ผ่าน Go, หน้า Home/Check-in/Loading/Delivery ใหม่แบบ BLoC (single-stop)
- หน้าอื่นทั้งหมดใน gotest: ซ่อน/ปิด พร้อมข้อความ "ยังไม่เปิดใช้ใน GO-TEST" (en+th)
- i18n en+th, Go tests, Flutter unit/bloc tests, device checklist บน staging

**Out of scope (slice ถัดไป / ไม่ทำ):**
- Offline outbox + drift cache (T56), multi-drop / add-stop, standby, incident, expense, maintenance, leave, holidays, chat, broadcast, trip history, hubs create, manual task (`POST /v1/mobile/tasks/manual`), helper picker (`GET /v1/mobile/drivers`)
- OCR/geo/static-map proxy ผ่าน Go (T57), ~~Google sign-in~~ (ทำแล้ว 2026-11-20 — ดู §9 "Slice Google sign-in"), `POST /v1/auth/exchange`, SSE/FCM refresh, `PUT /v1/mobile/me/devices`, iOS
- Server-side `426 version_blocked` (`MOBILE_VERSION_FLOOR_ENFORCE` ยังไม่มีโค้ดอ่าน — ดู §9)
- Write-back mode (Firestore-owned operations) สำหรับ route ใหม่ — รองรับเฉพาะ PostgreSQL path
- แตะ `pubspec.yaml` version (prod คง `3.6.6+11`)

## 3. Requirements

**Functional — Flutter (gotest เท่านั้น)**
- **R1 — Composition root ตาม flavor.** `main()` แยกทาง: `AppFlavor.isGoTest` → `runGoTestApp()` (ไม่ init Firebase เลย); อื่นๆ → `MyApp` เดิมทุกบรรทัด
- **R2 — REST client.** dio instance เดียวต่อ `AppFlavor.apiBaseUrl`; ส่ง header ตาม §11.2: `Authorization`, `X-App-Version`, `X-App-Build`, `X-App-Flavor` (=`gotest`), `X-Install-Id`, `X-Platform` (=`android`), `Accept-Language` (`en|th` ตาม locale), `X-Request-Id` (UUIDv4 ต่อ request); JSON camelCase; error envelope `{error:{code,message,details,requestId}}` → `ApiException`; network/timeout → `NetworkException` (แยกชนิด **ไม่ใช่** auth error)
- **R3 — Session.** `POST /v1/auth/login {email,password,platform:"android",installId,appVersion}`; access token เก็บใน memory, refresh token เก็บใน `flutter_secure_storage`; `QueuedInterceptor` refresh แบบ single-flight เมื่อ `401 token_expired` (ทุก `details.reason`) หรือเหลืออายุ < 60 s แล้ว retry **ครั้งเดียว**; `401 session_revoked|invalid_token` หรือ refresh ล้มด้วย 401 → ล้าง session → หน้า login + ข้อความ; **network error ไม่ logout เด็ดขาด**
- **R4 — Cold start.** มี refresh token → silent refresh → `GET /v1/mobile/me` → Home; ไม่มี/refresh 401 → login; refresh ล้มเพราะ network → หน้า "เชื่อมต่อไม่ได้ + ลองใหม่" (ไม่ลบ token)
- **R5 — ต้องเปลี่ยนรหัสผ่าน.** `403 password_change_required` → หน้าเปลี่ยนรหัส (ใช้ `details.passwordChangeTicket`) → `POST /v1/auth/password/change {passwordChangeTicket,newPassword}` → login อัตโนมัติด้วยรหัสใหม่
- **R6 — Logout.** `POST /v1/auth/logout {refreshToken,installId}` แล้วล้าง secure storage เสมอ (แม้ network ล้ม)
- **R7 — Driver resolve.** หลัง login/cold start เรียก `GET /v1/mobile/me`; `driver == null` → logout + ข้อความ `driver_profile_required`
- **R8 — Version gate.** ใช้ logic ADR 0007 เดิม (cache 30 วัน, fail-open) แต่อ่านจาก `GET /v1/mobile/settings?flavor=dev` (`AppFlavor.backendFleet`) ตอน login, resume, หลัง driver resolve
- **R9 — Heartbeat.** `POST /v1/mobile/heartbeat {installId,platform,appVersion,buildNumber,flavor:"dev"}` debounce 45 s; เรียกหลัง driver resolve, resume, check-in/loading/deliver สำเร็จ; error = log เงียบ
- **R10 — รายการงาน.** `GET /v1/mobile/tasks` (ลำดับจาก server, ADR 0028); refresh เมื่อ resume, pull-to-refresh, และทุก 60 s **เฉพาะตอน Home มองเห็นอยู่และแอป foreground**; แสดงเวลาอัปเดตล่าสุด; error → แถบ error + ปุ่ม "ลองใหม่" (คงรายการเดิมไว้)
- **R11 — Queue gate.** (client = UX, server = ตัวบังคับจริง R22) เช็คอินได้เฉพาะงานที่ไม่มีงานก่อนหน้าขวาง; server ตอบ `queue_blocked` → ข้อความ `task_blocked_by_queue` + รีเฟรชรายการ; (port ของ `taskBlocksSuccessorInQueue` / `isQueueEligibleForCheckIn` ใน `task_repository.dart:79-115` ไปใช้ canonical status + `trip.status`)
- **R12 — เช็คอิน.** รูปกล้อง (overlay เหมือนเดิม) + screenshot แอปลูกค้า (บังคับ, ไม่ overlay — ADR 0019) + เลือกรถจาก `GET /v1/mobile/trucks` (default = รถของ task); ลำดับ: presign → PUT → `POST /v1/mobile/tasks/{id}/check-in`
- **R13 — รับงาน (single-stop).** Trip ID จาก QR/barcode (`QrScanPage`) หรือพิมพ์เอง; seal, จำนวนพัสดุ, น้ำหนัก, เวลา seal; รูป `pre_close`, `closing`, `seal`, `truck_release` (screenshot, ไม่ overlay), `runsheet` + `runsheet_extra_1..3`; ต้นทาง/ปลายทาง = ของ task (read-only); `POST /v1/mobile/trips`; งาน multi-drop → ปิดปุ่มพร้อมข้อความ
- **R14 — ส่งงาน (ปลายทางเดียว).** รูป `arrived` (screenshot), `pre_open`, `opening`, `empty_container`, `runsheet_received`; `POST /v1/mobile/trips/{id}/deliver`; Home กู้งานค้างจาก `GET /v1/mobile/trips/pending-in-transit` และตรวจสถานะด้วย `GET /v1/mobile/trips/by-no/{tripNo}` (กติกาเดียวกับ `checkPendingTripOnServer`: delivered/cancelled/404 → เคลียร์, in_transit → คงไว้, network → คงไว้)
- **R15 — Idempotency ฝั่ง client.** ทุก mutation (check-in, trips, deliver) ส่ง `Idempotency-Key` = UUIDv7 ที่สร้าง **ครั้งเดียวต่อการกด submit** และใช้ซ้ำเมื่อกด "ลองใหม่"; ถ้าผู้ใช้แก้ข้อมูลในฟอร์ม → สร้าง key ใหม่ (fingerprint เปลี่ยน); key รูปที่อัปโหลดแล้วใช้ซ้ำ ไม่อัปโหลดซ้ำ; presign หมดอายุ (PUT 403) → re-presign ด้วย `{key}` เดิม
- **R16 — ฟีเจอร์ที่ยังไม่ย้าย.** แท็บ Vehicle, Chat; เมนู profile, hubs reference, holiday calendar, broadcast, trip history, leave; ~~ปุ่ม Google sign-in~~ (เปิดแล้ว 2026-11-20), forgot password, manual check-in, เพิ่มจุดส่ง, standby, incident → แสดง `GoUnavailablePage` / ปุ่ม disabled + ข้อความ GO-TEST
- **R17 — ป้าย GO-TEST.** แสดงป้าย "GO-TEST" บนหน้า login และ app bar ทุกหน้า + version `4.0.0-gotest.N`

**Functional — Go (additive)**
- **R18** `GET /v1/mobile/me` รับ Go driver token เพิ่ม โดย shim path (`X-Api-Key` cf_shim + Firebase token) ทำงานเหมือนเดิมทุก byte; caller แบบ driver token ได้ field `mobileSettings` เพิ่ม
- **R19** `GET /v1/mobile/tasks` (driver queue)
- **R20** `GET /v1/mobile/trucks` (รถของ tenant ตัวเอง)
- **R21** `POST /v1/mobile/uploads/presign` (URL absolute สำหรับแอป)
- **R22** `POST /v1/mobile/tasks/{id}/check-in` ✱ — บังคับ queue gate ฝั่ง server ด้วย (`409 failed_precondition` reason `queue_blocked`, Q2)
- **R23** `POST /v1/mobile/trips` ✱ (loading)
- **R24** `POST /v1/mobile/trips/{id}/deliver` ✱
- **R25** `GET /v1/mobile/trips/pending-in-transit`, `GET /v1/mobile/trips/by-no/{tripNo}`
- **R26** Idempotency middleware ทำงานบน route ✱ (R22–R24): ไม่มี header → `400`; replay → response เดิม byte-for-byte + `Idempotent-Replayed: true`; body ต่าง → `409 idempotency_conflict`
- **R27** route เขียนใหม่ทั้งหมดรองรับเฉพาะ operations ที่ PostgreSQL-owned; โหมด write-back → `422` (`photos`: `write_back_unsupported`) — **ไม่มีการเขียน Firestore จาก route เหล่านี้**

**Non-functional**
- **N1 — i18n:** ทุกข้อความใหม่มี key ใน `assets/translations/en.json` **และ** `th.json`; แปลจาก `error.code` เท่านั้น ไม่แสดง `error.message` ดิบ (ยกเว้นแนบท้ายข้อความ unknown)
- **N2 — Prod isolation:** dev/prod ไม่มี path ใหม่ที่ถึงได้; gotest ไม่ init Firebase; gotest publish ไม่ได้ (§4.7)
- **N3 — Security:** refresh token อยู่ใน secure storage เท่านั้น, ไม่ log token; dio log interceptor เฉพาะ `kDebugMode` และ redact `Authorization`; dio สำหรับ presigned PUT แยก instance (ไม่แนบ bearer)
- **N4 — API versioning:** additive only (`.vibe-rules.md` API Versioning); shim contracts ไม่เปลี่ยน; `logitrack-api/api/routes.txt` regen (`make gen`)
- **N5 — ต้นทุน:** poll ≤ 1 req/60 s ต่อเครื่องขณะ Home เปิด; รูป ≤ 500 KB (ใช้ `compressImageForUpload` เดิม)
- **N6 — สถาปัตยกรรม:** หน้าใหม่ใช้ BLoC (`flutter_bloc`) + Clean Architecture (presentation/domain/data); หน้าเดิมไม่ถูก refactor
- **N7 — Version:** gotest ใช้ `--build-name 4.0.0-gotest.N` จาก `scripts/build-mobile-gotest.mjs`; **ห้าม** bump `pubspec.yaml`

## 4. Design

### 4.1 การตัดสินใจหลัก (pragmatic route)

| # | ตัดสินใจ | เหตุผล |
|---|---|---|
| D1 | gotest ได้ **app shell + หน้า core loop ใหม่** (`lib/go/**`, BLoC) แทนการเสียบ seam ในหน้าเดิม | หน้าเดิมเรียก Firebase ตรง ~24 จุดใน `check_in_page.dart` + `loading_phase_page.dart` (12+12) และใช้ `FirebaseAuth.instance.currentUser` เป็น identity; 4.0.0 จะเป็น Go-only (§11, P7a) → หน้า Firebase จะถูกเลิกใช้ การยัด interface ลงหน้าที่จะทิ้งคือ diff เสี่ยงบน prod ที่ไม่มีมูลค่าระยะยาว หน้าใหม่ = หน้า 4.0 ตัวจริง |
| D2 | reuse ของที่ **ไม่แตะ Firebase**: `fetchOverlayContext` / `overlayGeocodingAndTimestamp` (`photo_overlay_service.dart`), `compressImageForUpload`, `QrScanPage`, `TruckPickerField` + `SelectableTruck` | ไม่ต้องเขียนกล้อง/overlay ใหม่, พฤติกรรมรูปเหมือนเดิม |
| D3 | gotest **ไม่เรียก `Firebase.initializeApp`** (return ก่อน) | การเรียก Firebase โดยไม่ตั้งใจจะ throw `[core/no-app]` ทันที = fence ชั้น runtime; App Check debug provider ที่เพิ่มใน branch นี้ไม่จำเป็นอีก → คืนบรรทัดนั้นใน `main.dart` ให้เหมือน mv-go |
| D4 | OCR: QR/barcode + พิมพ์เอง เท่านั้น (ไม่มี Cloud Vision text fallback) / overlay: เหมือนเดิม (geocode + static map ด้วย key ใน `.env.dev` ที่ gotest โหลดอยู่แล้ว, มี map-less fallback เดิม) | Vision เรียก API ด้วย APK key ซึ่ง T57 จะย้ายไป proxy; overlay ไม่ผูก Firebase และไม่ถึง prod |
| D5 | seam ใน dev/prod มีที่เดียว: `MobileAppVersionService` รับ `VersionConfigSource` (default = Firestore เดิม) | ไม่ทำซ้ำ logic cache/fail-open ของ ADR 0007 (~130 บรรทัด) ซึ่งเสี่ยงกว่า diff ~20 บรรทัด |
| D6 | heartbeat gotest = `GoHeartbeatService` ใหม่ (ไม่แก้ `MobileClientHeartbeatService`) | ตัวเดิมอ่าน Firestore driver doc ก่อนเขียน |
| D7 | DTO เขียนมือ (`fromJson`, ignore unknown field, unknown enum → `unknown`) | `logitrack-api/api/openapi.yaml` ยังไม่มีใน repo (มีแค่ `api/routes.txt`) — UNVERIFIED ว่าจะ generate ได้เมื่อไร |
| D8 | ไม่มี `clientOpId` ใน slice นี้ | §11.6 กำหนด `client_op_id` เฉพาะ manual task / standby / incident / expense / leave — ไม่มีอันไหนใน scope; backstop = state machine (check-in), `trip_no` UNIQUE + seal (loading), delivered state (deliver) |
| D9 | key รูป loading ใช้ `entityId` = Idempotency-Key ของ submit (UUIDv7) เพราะ trip ยังไม่มี id | `storage.Service.Commit` ผูก owner ด้วย `OwnerID` ใน tx ไม่ได้ตรวจ prefix ของ key (`service.go:418-513`) → key `trips/{opId}/…` commit เข้า trip ได้ (ทางเลือก: ให้ client mint trip id — ดู §9 Q4) |
| D10 | `{id}` ของ trip route รับ uuid **หรือ** `tripNo` | `SavedTripSummary.tripId` ของแอปคือ trip_no; ไม่ต้องเพิ่ม field ใน model ที่ dev/prod ใช้ร่วม |

### 4.2 Go — route ใหม่/ที่แก้ (ทั้งหมดใต้ `app.MobileGroups`, `internal/app/mobile.go`)

Guard ร่วม: `d.Auth.RequireAuth()` (Go access token) → capability → `driver:self` (`driverPrincipal`, `internal/operations/read.go:65`); route ✱ ต่อด้วย `idempotency.Middleware.Handler()` หลัง auth

| Route | Cap | Request | Response | Errors | Reuse |
|---|---|---|---|---|---|
| `GET /v1/mobile/me` | authenticated | — | `MobileMe` เดิม + `mobileSettings` (= `Settings.Public(flavor)` จาก `X-App-Flavor` ถ้า valid) **เฉพาะเมื่อ `p.APIKeyScope != cf_shim`** | 401, 403 `driver_profile_required` | `auth.Service.MobileMe`; guard ใหม่ `RequireShimOrAuth(ShimSetDriverClaims)` |
| `GET /v1/mobile/tasks` | `mobile:view_tasks` | `?status&limit&cursor` | `{data:[QueueTask], nextCursor}` (`TaskBase`+`queueAt`, ไม่มี billing party) | 422 status/limit/cursor | `operations.Service.DriverQueue` (`read_tasks.go:603`) — มีแล้ว, แค่ mount |
| `GET /v1/mobile/trucks` | `mobile:checkin` | `?q&limit&cursor` | `{data:[{id,licensePlate,typeRaw,vehicleClass,model,status}]}` เรียงตามทะเบียน, เฉพาะ `status` ใช้งานได้ | 422 | RLS `p_driver_read ON trucks` (own tenant, `0003_master.sql:423`); method ใหม่ `fleet.Service.DriverTrucks` หรือ driver branch ใน `ListTrucks` (UNVERIFIED ว่า `ListTrucks` รับ driver principal) |
| `POST /v1/mobile/uploads/presign` | `driver:self` + rl `presign_user` | `PresignInput` (`purpose` ∈ {`checkin_photo`,`checkin_app_screenshot`,`trip_photo`}, `entityId`, `contentType`, `sizeBytes`, `variant`, `key?`) | `Presigned{key,url,method,headers,expiresAt,storageBackend}` (url **absolute**) | 422 purpose/contentType/sizeBytes/variant | `storage.Service.Presign(..., PutOptions{APIPath:false})`; upload local = `PUT /media/{key}` (มีแล้ว) |
| `POST /v1/mobile/tasks/{id}/check-in` ✱ | `mobile:checkin` | `{truckId, checkInAt, lat, lng, photoKey, appScreenshotKey, installId?}` | `200 {data: TaskBase}` | 403 `not_task_owner`; 404 `task_not_found`; 409 `failed_precondition` reason `task_not_open` {status} \| `queue_blocked` {blockingTaskId}; 422 `truckId` `not_found`\|`unknown_vehicle_class`, `photoKey`/`appScreenshotKey` (`not_found`,`purpose_mismatch`,`upload_missing`), `helperDriverId` `not_supported`, `photos` `write_back_unsupported` | `TaskOwners.TaskOwner` + owner check แบบ `AddTaskStopForDriver` (`mobile.go:123-176`); `LockTask`, `WriteTruck`, `storage.Commit`, `CheckInTask` (`queries/tasks.sql:95`); query ใหม่ `SetDriverActiveTask`; outbox `task.checked_in` (+ `taskTopics`) |
| `POST /v1/mobile/trips` ✱ | `mobile:submit_trip` | `{tripNo, taskId, sealCode?, partnerCode?, ocrData?, truckId?, parcelCount?, sealTime?, totalWeight?, lat?, lng?, photos:[{type,key,lat?,lng?,address?,geocodedAt?}]}` | `201 {data: MobileTripView}` | 409 `duplicate_trip_id` {tripNo}, `duplicate_seal` {tripNo?}, `failed_precondition` reason `task_not_checked_in`\|`multi_drop_not_supported`\|`task_has_open_trip`; 403 `not_task_owner`; 422 `tripNo` (ValidateTripNo), `truckId` `must_match_task`, `origin`/`destination`/`distance`/`isMultiDelivery` `not_supported`, photos | `CheckSeal` แล้ว `CreateTrip` (`trips.go:55,120`), `Stamp(…,"trip_records",…)` (`stamp.go:66`), `PutTripPhoto`, `SetTaskStatus(in_transit)`; outbox `trip.created` + `task.updated` |
| `POST /v1/mobile/trips/{id}/deliver` ✱ | `mobile:submit_trip` | `{deliveredAt, lat, lng, photos:[…]}` | `200 {data: MobileTripView}` | 403 `not_task_owner`; 404; 409 `failed_precondition` reason `trip_not_open` {status}; 422 photos | แยก body ของ `DeliverTripAPI` (`write_trips.go:370-450`) เป็น helper `deliverTripTx(…, via)` ใช้ร่วม staff (`admin_web`) และ driver (`mobile`); staff behaviour ไม่เปลี่ยน |
| `GET /v1/mobile/trips/pending-in-transit` | `mobile:submit_trip` | — | `{data: MobileTripView\|null}` | — | query `PendingInTransitTrip` (`queries/trips.sql:287`) |
| `GET /v1/mobile/trips/by-no/{tripNo}` | `mobile:submit_trip` | — | `{data: MobileTripView}` | 404 | `TripByNo` ใต้ driver RLS |

**กติกา service ที่ต้องเขียนให้ชัด**
- **Owner (R28):** task.driver_id = me หรือ task.helper_driver_id = me; trip route ใช้ owner ของ task ของ trip
- **Check-in state machine:** อนุญาตจาก `pending|assigned`; ถ้า `checked_in` อยู่แล้ว **และ** `check_in_at` = body และ photo file = key เดิม → `200` (replay หลัง `IDEMPOTENCY_TTL`); อื่นๆ → `409 task_not_open`. **Queue gate (Q2):** ใน tx เดียวกับ `LockTask` ไล่งานของคนขับคนนี้ (driver หรือ helper) ตามลำดับเดียวกับ `DriverQueue` (ADR 0028) — มีงานก่อนหน้าที่ `pending|assigned|in_transit` หรือ `checked_in` ที่ trip ยังไม่ `delivered` → `409 queue_blocked {blockingTaskId}` (กติกาเดียวกับ `queue_gate.dart` ฝั่ง client, R11; ตาราง test ชุดเดียวกัน). รถต้องอ่านได้ใต้ RLS ของ driver และมี `vehicle_class` (กติกา "Vehicle identity" — รถ class ไม่รู้จักห้ามใช้)
- **Loading:** task ต้อง `checked_in`, ไม่ multi-drop, ยังไม่มี trip `in_transit` ของ task นี้; `CheckSeal` ก่อน insert ใน tx เดียวกัน; trip รับ `job_type`/`job_category`/`truck_*`/`billing_party_id`/origin/destination จาก task; `created_at = std = now()`; `sta = seal_time + duration_minutes` เมื่อพบคู่ใน distance table (lookup function UNVERIFIED — `hubs.Service.ListDistances` `distances.go:130` หรือ query ใหม่) ไม่พบ = NULL; คัดลอก `checkin_app` จาก `tasks.check_in_app_screenshot_file_id` เข้า `trip_photos` (ADR 0019); task → `in_transit`. ไม่ stamp `billing_date` เอง — ปล่อยให้ `billing.compute` (UNVERIFIED ว่า pipeline stamp ให้ trip ที่ insert จาก Go)
- **Deliver:** `delivered_via='mobile'`, `delivered_at` = body (required); task → `completed`; `ClearDriverActiveTask`; outbox `trip.delivered` (→ `billing.compute`, `notify.line` ตาม `mq/topology.go:109`); trip `delivered` แล้วด้วย `delivered_via=mobile` และ `delivered_at` เท่า body → `200` (replay), อื่นๆ → `409 trip_not_open`
- **`MobileTripView` (DTO ใหม่, ไม่มี billing/review/ocr):** `{id, tripNo, status, taskId, jobType, jobCategory, origin, destination, sealCode, truckId, licensePlate, createdAt, deliveredAt, photos:[{type}]}`
- **Write-back mode:** ทุก route ✱ ใน slice นี้ → `422 photos write_back_unsupported` ก่อนเปิด tx (แบบเดียวกับ `PatchTripAPI` `write_trips.go:179-181`)

**`RequireShimOrAuth(callable)`** (`internal/auth/shim.go`): มี header `X-Api-Key` → `RequireShim(callable)` เดิม (counter `shim_requests_total` เดิม); ไม่มี → `RequireAuth()`. Firebase token ที่ไม่มี key → `401 invalid_token` (เหมือนเดิม)

**Idempotency wiring:** เพิ่ม `Idempotency *idempotency.Middleware` ใน `APIDeps` (`internal/app/apiserver.go:53`), สร้างใน `cmd/api` ด้วย `idempotency.New{Redis, Keys, Store: NewPGStore(pool), TTL: IDEMPOTENCY_TTL, Scope: user id ของ principal}` + register metrics (UNVERIFIED จุด wiring ใน `cmd/api` — ยังไม่มี call site ใดเลย)

**SQL ใหม่** (`internal/operations/queries/writes.sql` + `sqlc generate`):
```sql
-- name: SetDriverActiveTask :one
-- check-in ของคนขับ: รถที่รับผิดชอบตอนนี้ (vehicle identity) — RLS p_self_update + trg_driver_self_columns (active_* เท่านั้น)
UPDATE drivers SET active_truck_id = sqlc.arg(truck_id), active_truck_plate = sqlc.arg(plate),
  active_task_id = sqlc.arg(task_id), active_started_at = sqlc.arg(started_at)::timestamptz
WHERE id = sqlc.arg(driver_id) RETURNING id;
```
ไม่มี migration ใหม่ (column + RLS มีแล้ว: `0003_master.sql:418-438`, `0004_operations.sql:519-525`, `app_rls_driver_rows('trip_records',…)` `:512`)

**ตัวอย่าง body**
```json
// POST /v1/mobile/tasks/{id}/check-in   (Idempotency-Key: 0193f…)
{"truckId":"0193…","checkInAt":"2026-11-18T08:15:00+07:00","lat":13.75,"lng":100.50,
 "photoKey":"checkin/<taskId>/1763427300000.jpg","appScreenshotKey":"checkin/<taskId>/app_screenshot_1763427301000.jpg","installId":"fis_x"}
// POST /v1/mobile/trips
{"tripNo":"ZXJB26111800001","taskId":"0193…","sealCode":"S-1234","parcelCount":120,"sealTime":"2026-11-18T09:00:00+07:00",
 "totalWeight":350.5,"lat":13.7,"lng":100.5,"photos":[{"type":"pre_close","key":"trips/<opId>/pre_close-1763….jpg","lat":13.7,"lng":100.5,"geocodedAt":"…"},
 {"type":"truck_release","key":"trips/<opId>/truck_release-1763….jpg"}]}
// POST /v1/mobile/trips/{id|tripNo}/deliver
{"deliveredAt":"2026-11-18T13:40:00+07:00","lat":13.6,"lng":100.6,"photos":[{"type":"arrived","key":"trips/<tripId>/arrived-….jpg"}]}
```

### 4.3 Data model
- **PostgreSQL:** ไม่มี table/column ใหม่; เขียน `tasks` (check-in columns), `drivers.active_*`, `trip_records`, `trip_photos`, `file_objects` (commit) ใต้ `db.WithPrincipal` ของคนขับ
- **Firestore:** ไม่แตะ (ไม่มี collection/field/rules/index ใหม่); `logitrack-web/firestore.rules` ไม่เปลี่ยน
- **Billing:** ไม่แก้ `lib/billingCompute.ts` / `functions/src/core/billingCompute.ts` — ราคาเกิดจาก outbox `trip.delivered` → `billing.compute` ที่ server ตามกติกา "ราคาเที่ยวคิดที่ server เท่านั้น"
- **Hub/SOC:** ไม่มี resolution ใหม่ฝั่ง client (ต้นทาง/ปลายทางมาจาก task); ฝั่ง Go ใช้ `directory.resolve` เดิม — ไม่ merge `nameToCode`/`codeToName`

### 4.4 Flutter — โครงไฟล์ใหม่

| Path | หน้าที่ |
|---|---|
| `lib/core/api/api_client.dart` | factory dio: base URL, timeout (connect 10 s / receive 30 s), header interceptor (R2), error mapping → `ApiException` / `NetworkException` |
| `lib/core/api/auth_interceptor.dart` | `QueuedInterceptor`: แนบ bearer, refresh single-flight, retry 1 ครั้ง, ส่ง `SessionEvent.expired` เมื่อ refresh 401 |
| `lib/core/api/token_store.dart` | access token (memory + `expiresAt`), refresh token (`flutter_secure_storage`, key `lt_refresh_token`) |
| `lib/core/api/api_exception.dart`, `op_id.dart` | ชนิด error + `newOpId()` = `Uuid().v7()` |
| `lib/core/api/upload_client.dart` | presign → PUT bytes (dio แยก ไม่มี bearer) → คืน key; re-presign `{key}` เมื่อ PUT 403/expired |
| `lib/go/app/go_test_app.dart` | `runGoTestApp()`, `MaterialApp` + routes ของ gotest, `GoSessionListener`, ป้าย GO-TEST |
| `lib/go/app/go_unavailable_page.dart` | หน้า/วิดเจ็ต "ยังไม่เปิดใช้ใน GO-TEST" (R16) |
| `lib/go/core/session/session_bloc.dart` | `SessionBloc`: `unknown → unauthenticated \| passwordChangeRequired(ticket) \| authenticated(MobileMe)` |
| `lib/go/core/services/go_heartbeat_service.dart`, `go_version_config_source.dart` | R9, R8 |
| `lib/go/features/auth/{data,domain,presentation}/` | `GoAuthRepository` (login/refresh/logout/changePassword/me), `LoginBloc`, `GoLoginPage`, `GoChangePasswordPage` |
| `lib/go/features/tasks/{data,domain,presentation}/` | `GoTaskRepository`, `DriverTask` (+`TaskStatus` enum canonical + `unknown`), `queue_gate.dart` (R11), `TaskListBloc` (poll), `GoHomePage` (+แท็บ disabled) |
| `lib/go/features/check_in/…` | `GoTruckRepository`, `CheckInBloc`, `GoCheckInPage` |
| `lib/go/features/loading/…` | `GoTripRepository`, `LoadingBloc`, `GoLoadingPage` |
| `lib/go/features/delivery/…` | `DeliveryBloc`, `GoDeliveryPage` |
| `lib/go/shared/photo_capture.dart` | ห่อ image_picker + overlay (D2) + compress → `CapturedPhoto{bytes,lat,lng,takenAt,overlaid}` |
| `test/go_fence_test.dart` | fence แบบ static (§4.7) |

**ไฟล์ dev/prod ที่แตะ (ทั้งหมด):** `lib/main.dart` (branch R1 + คืนบรรทัด App Check), `lib/core/services/mobile_app_version_service.dart` (seam D5), `pubspec.yaml` + `pubspec.lock` (deps), `assets/translations/en.json` + `th.json` (key ใหม่ additive)

**Session flow (สรุป)**

| เหตุการณ์ | ผล |
|---|---|
| `401 token_expired` | refresh (single-flight) → retry 1 ครั้ง; ล้มด้วย 401 → logout + `session_expired_relogin` |
| `401 session_revoked` / `invalid_token` | ล้าง session → login + `session_expired_relogin` |
| `NetworkException` / 5xx / 429 | คง session; แสดง error + ปุ่ม `retry` (429 แสดง `retryAfterSeconds`) |
| `423 locked` | `login_error_locked` พร้อมนาที |

**State mapping task (canonical จาก Go)** — UI ใช้ enum ตรงๆ ไม่แปลงเป็น literal เดิม: `pending|assigned` → เช็คอินได้ (ถ้าผ่าน gate), `checked_in` → ไป Loading, `in_transit` → ไป Delivery, `completed|cancelled` → ซ่อนจากคิว, อื่นๆ → `unknown` (แสดง, ไม่ให้กด)

### 4.5 Web (Next.js)
- ไม่มีการเปลี่ยนแปลง (แอดมิน staging ใช้หน้าที่มีอยู่บน `logi.showkhun.co` สร้างงาน)

### 4.6 Mobile — deps + version

| Package | สถานะปัจจุบัน | การเปลี่ยน |
|---|---|---|
| `dio` | ไม่มี | เพิ่ม direct |
| `flutter_secure_storage` | ไม่มี | เพิ่ม direct (เช็ค `minSdk` กับ `flutter.minSdkVersion` — UNVERIFIED) |
| `uuid` | transitive `4.5.3` (`pubspec.lock:1293`) | เลื่อนเป็น direct `^4.5.3` (มี `v7()`) |
| `flutter_bloc` | ไม่มี (ยังไม่มี BLoC ในโค้ดเลย) | เพิ่ม direct — BLoC ตัวแรกของโปรเจกต์ |

นโยบาย pin: ใช้รูปแบบ caret เหมือนไฟล์เดิม ที่ **stable ล่าสุด ณ วันทำ F1** (ห้าม `any`), commit `pubspec.lock`, บันทึกเวอร์ชันที่ resolve ได้ใน Change Log; ไม่อัปเกรด package เดิมพ่วง
Version: **ไม่ bump `pubspec.yaml`** (คง `3.6.6+11`); gotest = `4.0.0-gotest.<GOTEST_BUILD>` / versionCode `<GOTEST_BUILD>` จาก `scripts/build-mobile-gotest.mjs` (ต่ำกว่า `4.0.0` ตาม pub_semver → floor 4.0.0 จริงจะบล็อกมันในภายหลัง)

### 4.7 Prod isolation — สิ่งที่รับประกัน

| ชั้น | กลไก | พิสูจน์ด้วย |
|---|---|---|
| Compile | `AppFlavor.isGoTest` เป็น `const` → branch ใน `main()` ถูก tree-shake ใน dev/prod | AC12 (strings ใน `libapp.so`) |
| Runtime | gotest ไม่ init Firebase → Firestore/Storage/Functions/Auth ใช้ไม่ได้ (throw) | AC13 |
| Static | `test/go_fence_test.dart`: ไฟล์ใน `lib/go/**`, `lib/core/api/**` ห้าม import `cloud_firestore`, `firebase_storage`, `cloud_functions`, `firebase_auth`, `firebase_app_check`, `core/services/cloud_functions_service.dart` และห้าม import `*/data/repositories/*` ยกเว้น `trucks_repository.dart` (ใช้แค่ type `SelectableTruck`); ไฟล์นอกสองโฟลเดอร์นี้ห้าม import `lib/go/**` ยกเว้น `main.dart` | AC14 |
| Server | route ใหม่รับเฉพาะ Go driver token; APK 3.x (Firebase token ไม่มี cf_shim key) ได้ `401`; โหมด write-back ไม่เขียน Firestore (R27) | AC9, AC10 |
| Package/publish | applicationId `com.wrt.logitrack.gotest` (ติดตั้งคู่ prod ได้); `publish-mobile-release.mjs` ปฏิเสธชื่อ APK ที่มี `gotest` (มีแล้ว `:106-107`); build script ปฏิเสธ google-services / `.env.dev.mobile` ที่เป็น prod project (มีแล้ว) | AC15 |

### 4.8 Staging — ข้อมูลและ identity
- Staging VM: `PG_OWNED_DOMAINS=all`, `ETL_SYNC_ENABLED=false` → ข้อมูลคือ seed ใน PG ไม่ใช่ Firestore dev
- **คนขับทดสอบ (ทางที่แนะนำ):** platform admin บน `logi.showkhun.co` → สร้าง driver (`/v1/drivers`) → สร้าง user role driver (`POST /v1/users`) → `PUT /v1/users/{id}/driver-link` → `POST /v1/users/{id}/password/temporary` (แสดงครั้งเดียว) → ส่งให้ tester → login ในแอปจะเจอ R5 (เปลี่ยนรหัส). UNVERIFIED ว่าหน้า web staging มีปุ่มครบทุกขั้น (ถ้าไม่มี ใช้ API ตรงผ่าน BFF)
- local: `cmd/seed --profile smoke --temporary-password-file <path>` ให้ `d7.kitti@logitrack.test` (verified ใน `cmd/seed/bootstrap_integration_test.go`) — UNVERIFIED ว่า staging seed profile มี driver/รถ/งานพร้อม
- ต้องมี: รถใน tenant ของคนขับที่มี `vehicle_class` และงาน `assigned` ให้คนขับ (สร้างจากหน้า First Mile / Line Haul)
- **Env flags:** ไม่ต้องเพิ่มใหม่ ตรวจค่าที่มีใน `deploy/vm/env.vm.example`: `PUBLIC_ROUTE_GROUPS` มี `/v1/mobile,/v1/auth,/media` ✓, `STORAGE_BACKEND=local` ✓ + `LOCAL_MEDIA_SIGNING_KEY` ต้องตั้ง, `LOCAL_MEDIA_PUBLIC_BASE_URL=https://logi.showkhun.co/media` (⚠️ ต้องถึง public listener ของ API จากมือถือ — UNVERIFIED, ดู §9 Q3), `IDEMPOTENCY_TTL=168h` ✓, `RATE_LIMIT_ENABLED=true` ✓, `REFRESH_TOKEN_TTL_MOBILE=2160h` ✓, worker/relay ต้องรันอยู่ (outbox → `billing.compute`, `notify.line`), `MOBILE_SSE_ENABLED=false` / `AUTH_FIREBASE_BRIDGE_MODE=off` ใช้ได้

### 4.9 i18n keys ใหม่ (`assets/translations/{en,th}.json`)

| Key | en | th |
|---|---|---|
| `gotest_badge` | GO-TEST | GO-TEST |
| `gotest_not_available_title` | Not available in GO-TEST | ยังไม่เปิดใช้ใน GO-TEST |
| `gotest_not_available_body` | This feature still runs on the old system, so it is turned off in the GO-TEST app. | ฟีเจอร์นี้ยังใช้ระบบเดิม จึงปิดไว้ในแอป GO-TEST |
| `retry` | Retry | ลองใหม่ |
| `api_error_network` | Can't reach the server. Check your internet and try again. | เชื่อมต่อเซิร์ฟเวอร์ไม่ได้ ตรวจสอบอินเทอร์เน็ตแล้วลองใหม่ |
| `api_error_server` | The server had a problem. Please try again. | เซิร์ฟเวอร์ขัดข้อง กรุณาลองใหม่ |
| `api_error_rate_limited` | Too many requests. Try again in {seconds} s. | ส่งคำขอถี่เกินไป ลองใหม่ใน {seconds} วินาที |
| `api_error_validation` | Please check: {fields} | กรุณาตรวจสอบ: {fields} |
| `api_error_unknown` | Something went wrong ({code}). | เกิดข้อผิดพลาด ({code}) |
| `login_error_invalid_credentials` | Wrong email or password. | อีเมลหรือรหัสผ่านไม่ถูกต้อง |
| `login_error_account_disabled` | This account is disabled. Contact dispatch. | บัญชีนี้ถูกปิดใช้งาน กรุณาติดต่อฝ่ายจัดรถ |
| `login_error_locked` | Too many failed sign-ins. Try again in {minutes} min. | เข้าสู่ระบบผิดหลายครั้ง ลองใหม่ใน {minutes} นาที |
| `login_error_driver_profile_required` | This account is not linked to a driver. | บัญชีนี้ยังไม่ได้ผูกกับคนขับ |
| `session_expired_relogin` | Your session has ended. Please sign in again. | เซสชันหมดอายุ กรุณาเข้าสู่ระบบใหม่ |
| `password_change_title` | Set a new password | ตั้งรหัสผ่านใหม่ |
| `password_change_body` | You signed in with a temporary password. Set your own password to continue. | คุณเข้าด้วยรหัสผ่านชั่วคราว กรุณาตั้งรหัสผ่านของคุณเองเพื่อใช้งานต่อ |
| `password_new_label` / `password_confirm_label` | New password / Confirm password | รหัสผ่านใหม่ / ยืนยันรหัสผ่าน |
| `password_mismatch` | Passwords do not match. | รหัสผ่านไม่ตรงกัน |
| `password_change_invalid` | This password can't be used. Choose a stronger one. | ใช้รหัสผ่านนี้ไม่ได้ กรุณาตั้งให้รัดกุมขึ้น |
| `password_change_expired` | The change-password step expired. Sign in again. | หมดเวลาตั้งรหัสผ่าน กรุณาเข้าสู่ระบบใหม่ |
| `password_change_submit` | Save password | บันทึกรหัสผ่าน |
| `tasks_last_updated` | Updated {time} | อัปเดตล่าสุด {time} |
| `tasks_empty` | No jobs assigned. | ยังไม่มีงานที่ได้รับมอบหมาย |
| `task_blocked_by_queue` | Finish the earlier job first. | กรุณาทำงานก่อนหน้าให้เสร็จก่อน |
| `checkin_error_not_task_owner` | This job belongs to another driver. | งานนี้เป็นของคนขับคนอื่น |
| `checkin_error_task_not_open` | This job can no longer be checked in. Pull down to refresh. | งานนี้เช็คอินไม่ได้แล้ว ดึงหน้าจอลงเพื่อรีเฟรช |
| `truck_error_not_in_fleet` | This truck is not in your company's fleet. | รถคันนี้ไม่อยู่ในฟลีทของบริษัทคุณ |
| `truck_error_unknown_class` | This truck has no vehicle class. Ask dispatch to fix it. | รถคันนี้ยังไม่ได้กำหนดประเภทรถ กรุณาแจ้งฝ่ายจัดรถ |
| `loading_error_duplicate_trip_id` | Trip ID {tripNo} is already used. | Trip ID {tripNo} ถูกใช้ไปแล้ว |
| `loading_error_duplicate_seal` | This seal code is already used by another trip. | เลขซีลนี้ถูกใช้กับเที่ยวอื่นแล้ว |
| `loading_error_not_checked_in` | Check in to this job first. | กรุณาเช็คอินงานนี้ก่อน |
| `loading_error_open_trip` | This job already has a trip in transit. | งานนี้มีเที่ยวที่กำลังนำส่งอยู่แล้ว |
| `gotest_multi_drop_unavailable` | Multi-drop jobs are not available in GO-TEST. | งานส่งหลายจุดยังไม่เปิดใช้ใน GO-TEST |
| `delivery_error_trip_not_open` | This trip is already closed. | เที่ยวนี้ปิดไปแล้ว |
| `upload_progress` | Uploading photos {done}/{total} | กำลังอัปโหลดรูป {done}/{total} |
| `upload_error` | Photo upload failed. Tap Retry. | อัปโหลดรูปไม่สำเร็จ กด "ลองใหม่" |

ข้อความเดิมที่ reuse (มีอยู่แล้ว): `checkin_app_screenshot_required`, `truck_required`, `email_required`, `password_required`, `login`, `nav_*`, `mobile_force_update_*`

## 5. Affected files

**Go (`logitrack-api/`)**
- `internal/app/mobile.go`, `internal/app/apiserver.go` (`APIDeps.Idempotency`), `cmd/api/*` (wiring — ไฟล์จริง UNVERIFIED)
- `internal/auth/shim.go` (`RequireShimOrAuth`), `internal/auth/mobile_me.go` (`mobileSettings`)
- `internal/operations/mobile.go` (+ `mobile_checkin.go`, `mobile_trips.go` ใหม่), `write_trips.go` (แยก `deliverTripTx`), `queries/writes.sql` + `operationsdb/*` (sqlc)
- `internal/fleet/trucks.go` (driver trucks), `internal/storage/http.go` หรือ handler ใหม่ใน mobile group (presign)
- tests: `internal/operations/mobile_core_integration_test.go`, `internal/auth/*_integration_test.go`, `internal/storage/storage_integration_test.go`, `internal/fleet/*`
- `api/routes.txt` (generated), `README.md`

**Flutter (`logitrack-mobile/`)** — ตาม §4.4 + `pubspec.yaml`/`.lock`, `assets/translations/{en,th}.json`, `test/go_*_test.dart`

**Docs** — `shared-docs/.vibe-rules.md` (Change Log, Tech Stack, Confirmed Patterns), `shared-docs/specs/mv-go/B-api-catalog.md` (หมายเหตุ "As built (T55 core-loop slice)")

## 6. Task breakdown
> ทำตามลำดับ — แต่ละข้อ verify ได้เดี่ยวๆ (Go route + test ก่อน, แล้ว client, แล้วทีละหน้า)

- [x] **G1.** Wire idempotency middleware (`APIDeps.Idempotency`, `cmd/api`) + test route ✱ จำลอง: ไม่มี header 400, replay byte-identical + `Idempotent-Replayed`, body ต่าง 409 — R26
- [x] **G2.** `RequireShimOrAuth` + `GET /v1/mobile/me` driver token + `mobileSettings` (ไม่ใส่ให้ shim) + test: shim test เดิมผ่านโดยไม่แก้, driver bearer 200, Firebase token ไม่มี key 401 — R18
- [x] **G3.** Mount `GET /v1/mobile/tasks` → `DriverQueue` + test: own+helper, staff token 403, ลำดับ ADR 0028, cursor — R19
- [x] **G4.** `GET /v1/mobile/trucks` + test: เห็นแค่ tenant ตัวเอง (RLS), รถ status ปิดถูกกรอง — R20
- [x] **G5.** `POST /v1/mobile/uploads/presign` + test: URL absolute (local = `LOCAL_MEDIA_PUBLIC_BASE_URL`), purpose นอก allowlist 422, re-sign `{key}` — R21
- [x] **G6.** Check-in (+ `SetDriverActiveTask` sqlc) + test: owner/helper ok, คนอื่น 403, ไม่มี task 404, status ผิด 409, งานก่อนหน้าขวาง 409 `queue_blocked` (ตารางเดียวกับ AC3), replay 200, รถ tenant อื่น 422, class ไม่รู้จัก 422, รูป commit + owner=task, `drivers.active_*` ถูกตั้ง, outbox `task.checked_in`, write-back mode 422 — R22, R27
- [x] **G7.** `POST /v1/mobile/trips` + test: 201 + task `in_transit`, `duplicate_trip_id`, `duplicate_seal` (ข้าม tenant, ไม่ leak tripNo), ยังไม่เช็คอิน 409, multi-drop 409, truck/job category snapshot จาก task, `checkin_app` ถูกคัดลอก, STA เมื่อมี/ไม่มี distance, outbox `trip.created` — R23
- [x] **G8.** แยก `deliverTripTx` + `POST /v1/mobile/trips/{id}/deliver` + `pending-in-transit` + `by-no` + test: staff deliver test เดิมผ่าน, driver deliver `delivered_via=mobile`, task completed, active truck cleared, outbox `trip.delivered`, ref เป็น tripNo ได้, ปิดแล้ว 409, replay 200 — R24, R25
- [x] **G9.** `make gen` (`api/routes.txt`), อัปเดต `logitrack-api/README.md` route list, `go test ./...` + integration เขียว — N4
- [x] **F1.** Deps (§4.6) + `lib/core/api/**` + unit test: header ครบ, error mapping, refresh single-flight (2 request พร้อมกันได้ refresh ครั้งเดียว), retry 1 ครั้ง, network error ไม่ trigger logout, presigned PUT ไม่มี bearer; build dev+prod ผ่าน — R2, R3, N3
- [x] **F2.** `main.dart` branch + `GoTestApp` + `GoUnavailablePage` + ป้าย GO-TEST + `test/go_fence_test.dart` — R1, R16, R17, N2
- [x] **F3.** `SessionBloc` + login / change-password / logout / cold start / `GET /v1/mobile/me` + bloc tests — R3–R7
- [x] **F4.** Seam `VersionConfigSource` (Firestore default ไม่เปลี่ยนพฤติกรรม) + `GoVersionConfigSource` + `GoHeartbeatService` — R8, R9
- [x] **F5.** `TaskListBloc` (poll 60 s เฉพาะ visible+foreground) + `queue_gate.dart` + test ตาราง gate + `GoHomePage` + กู้งานค้าง — R10, R11, R14 (ส่วน pending)
- [x] **F6.** `GoCheckInPage` + `CheckInBloc` (upload → check-in, retry ด้วย key เดิม) — R12, R15
- [x] **F7.** `GoLoadingPage` + `LoadingBloc` — R13, R15
- [x] **F8.** `GoDeliveryPage` + `DeliveryBloc` — R14, R15
- [x] **F9.** i18n en + th ครบตาม §4.9 (จำนวน key เท่ากันสองไฟล์) — N1
- [ ] **F10.** Device checklist บน staging (§7 AC16) ด้วย `pnpm build:mobile:gotest`
- [x] **D1.** อัปเดต `.vibe-rules.md`: Change Log (2026-11-18 mobile-go core loop), Tech Stack (dio / flutter_secure_storage / flutter_bloc + เวอร์ชันที่ resolve), Confirmed Pattern "flavor composition root + `lib/go/` fence" (ตามกฎโปรเจกต์)
- [x] **D2.** หมายเหตุ "As built (T55 core-loop slice)" ใน Appendix B §B.2.21 (field ที่ยังไม่รับ: `helperDriverId`, `origin`/`destination`/`distance`/`isMultiDelivery`)

## 7. Acceptance criteria (ตรวจรับ)
- [ ] **AC1** (R18) driver bearer → `GET /v1/mobile/me` 200 มี `driver{id,tenantId,legacyDocId}` + `mobileSettings`; shim integration tests เดิมผ่าน **โดยไม่แก้ไฟล์ test** และ response shim ไม่มี `mobileSettings`
- [ ] **AC2** (R19, R10) staging: งานที่แอดมินมอบโผล่ในแอปภายใน ≤ 60 s โดยไม่ต้องกดอะไร (Home เปิดอยู่) และทันทีเมื่อ pull-to-refresh; ปิดหน้า Home/พักแอป → ไม่มี request `/v1/mobile/tasks` (ดูจาก access log)
- [ ] **AC3** (R11) unit test ตาราง gate: งานก่อนหน้า `checked_in` ที่ trip `delivered` ไม่ขวาง; `pending`/`in_transit` ขวาง; `completed`/`cancelled` ไม่ขวาง
- [ ] **AC4** (R12, R22) เช็คอินบนเครื่องจริง: task เป็น `checked_in`, `check_in_photo_file_id` + `check_in_app_screenshot_file_id` committed, `drivers.active_truck_id` = รถที่เลือก; หน้างาน/Driver Monitor บน staging web เห็นสถานะใหม่
- [ ] **AC5** (R13, R23) รับงาน: trip `in_transit` มีรูปครบทุกประเภทที่ถ่าย + `checkin_app`; Trip ID/seal ซ้ำ → ข้อความตาม §4.9 และฟอร์ม/รูปยังอยู่ (ไม่ต้องถ่ายใหม่)
- [ ] **AC6** (R14, R24) ส่งงาน: trip `delivered` (`delivered_via=mobile`), task `completed`, `drivers.active_*` ว่าง, outbox มี `trip.delivered` 1 แถว (และ `billing.compute` ประมวลผล — ราคาขึ้นใน staging web)
- [ ] **AC7** (R15, R26) ตัดเน็ตหลัง request ถึง server แล้วกด "ลองใหม่" → ได้ response เดิม (`Idempotent-Replayed: true`) ไม่มี trip/check-in ซ้ำ; แก้ฟอร์มแล้วส่ง → key ใหม่
- [ ] **AC8** (R3, R4, R6) access token หมดอายุ (รอ > 15 นาที) → request ถัดไปสำเร็จโดยไม่เห็นหน้า login; โหมดเครื่องบินตอน cold start → หน้า "ลองใหม่" ไม่ใช่ login; logout → เปิดแอปใหม่ต้อง login; admin revoke session → request ถัดไปพาไปหน้า login พร้อม `session_expired_relogin`
- [ ] **AC9** (R27) integration test: operations write-back mode → route ✱ ทั้งสามตอบ 422 `write_back_unsupported` และ `writeback.Memory` ไม่มี commit
- [ ] **AC10** (N2) Firebase ID token ไม่มี `X-Api-Key` ยิง route ใหม่ทุกเส้น → `401`
- [ ] **AC11** (R5, R7) login ด้วยรหัสชั่วคราว → หน้าตั้งรหัส → เข้า Home; user ที่ไม่ใช่คนขับ → ข้อความ `login_error_driver_profile_required` และไม่มี token ค้างใน secure storage
- [ ] **AC12** (R1, N2) `flutter build apk --flavor prod --dart-define=FLAVOR=prod` แล้ว `strings libapp.so` ไม่พบ `/v1/mobile/tasks` และ `GoTestApp`; `git diff mv-go -- logitrack-mobile/lib` นอก `lib/go/**`, `lib/core/api/**` มีแค่ `main.dart` + `mobile_app_version_service.dart`
- [ ] **AC13** (D3) gotest ไม่เรียก `Firebase.initializeApp` (debug log/breakpoint) และ flow core loop ครบโดยไม่มี exception จาก Firebase
- [ ] **AC14** (N2) `test/go_fence_test.dart` ผ่าน และล้มจริงเมื่อเติม `import 'package:cloud_firestore/...'` ใน `lib/go/` (ทดลองแล้ว revert)
- [ ] **AC15** (N7) `pubspec.yaml` ยัง `3.6.6+11`; APK gotest แสดง `4.0.0-gotest.N`; `publish-mobile-release.mjs --apk <gotest apk>` ถูกปฏิเสธ
- [ ] **AC16** (R8, R9, R16, R17) device checklist: ป้าย GO-TEST ทุกหน้า; แท็บ/เมนูใน R16 แสดงหน้า GO-TEST (th และ en); heartbeat ขึ้นในหน้า Mobile Clients ของ staging (`flavor=dev`, version `4.0.0-gotest.N`); ตั้ง floor บน staging สูงกว่า → dialog บังคับอัปเดต
- [ ] **AC17** (N1) en.json/th.json มี key ใหม่ครบทั้งสองไฟล์ (จำนวนเท่ากัน), ไม่มี string hardcode ใน `lib/go/**` (review + `grep "Text('"`)
- [ ] **AC19** (R22, Q2) Go test: งานที่ถูกงานก่อนหน้าขวางตาม AC3 → `409 queue_blocked` + `blockingTaskId` และไม่มีการเปลี่ยน task/driver; บนเครื่อง กดเช็คอินงานข้ามคิว (เช่น รายการค้างก่อนรีเฟรช) → ข้อความ `task_blocked_by_queue`
- [ ] **AC18** `go test ./...` + integration (pgtest) เขียว, go-ci `gen-check` ผ่าน (`routes.txt`), `flutter analyze` ไม่มี error ใหม่, `flutter test` เขียว, web CI ไม่กระทบ

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|---|---|
| แก้ `main.dart` / version service พลาดแล้วกระทบ prod | diff จำกัด 2 ไฟล์ (AC12), seam มี default เดิม + test; rollback = revert 2 ไฟล์ — โค้ด `lib/go/**` ไม่ถูกเรียกใน dev/prod |
| เพิ่ม plugin native (`flutter_secure_storage`) เข้า APK prod (register แม้ไม่ใช้), minSdk ไม่พอ | ตรวจ minSdk ตอน F1; build prod/dev ใน F1 ก่อนเขียนหน้า; ถ้ามีปัญหา → ใช้ plugin เวอร์ชันที่รองรับ minSdk เดิม |
| `RequireShimOrAuth` ทำ shim `setDriverClaims` เพี้ยน (APK 3.x ทั้ง fleet พึ่ง) | branch ตาม `X-Api-Key` เท่านั้น, ไม่แตะ `RequireShim`; shim tests เดิมต้องผ่านโดยไม่แก้ (AC1); rollback = mount `/me` กลับเป็น `RequireShim` |
| แยก `deliverTripTx` แล้ว staff deliver เปลี่ยนพฤติกรรม | test staff deliver เดิมต้องผ่าน (G8); `delivered_via` เป็น parameter เดียวที่ต่าง |
| Idempotency middleware ล่ม (Redis) | middleware ลงไปใช้ PG อย่างเดียวได้ตาม design (`idempotency.go:8-18`); natural key เป็นด่านสุดท้าย |
| ไม่มี outbox: แอปถูก kill ระหว่างส่ง → ข้อมูลฟอร์ม/รูปหาย | ยอมรับใน slice นี้ (ทดสอบเท่านั้น); server idempotent → ส่งซ้ำไม่เกิดข้อมูลซ้ำ; T56 แก้ |
| Presigned URL ชี้ host ที่มือถือเข้าไม่ถึง (`logi.showkhun.co/media`) | ตรวจใน G5/F10; แก้ค่า `LOCAL_MEDIA_PUBLIC_BASE_URL` บน VM (ไม่ต้องแก้โค้ด) |
| ข้อมูล staging ไม่พอ (ไม่มีรถที่มี vehicle class / ไม่มีคนขับ) | checklist §4.8 ก่อน F10 |
| คนขับใช้แอป gotest กับงานจริง | applicationId แยก, Firebase dev, Go staging เท่านั้น, ป้าย GO-TEST, ไม่ publish — ข้อมูล staging ไม่ไหลกลับ Firestore (`ETL_SYNC_ENABLED=false`, ไม่มี write-back) |
| Rollback ทั้ง slice | Go: route additive → ถอด mount ได้ทันที (ไม่มี migration); Flutter: ไม่ build gotest ต่อ — dev/prod ไม่ได้รับผลกระทบ |

## 9. Open questions / follow-ups

**ตัดสินแล้ว (owner, 2026-11-18 — ตามค่าแนะนำ):**
- **Q1 → ส่ง header `X-App-*` ตั้งแต่ slice นี้; server ยังไม่อ่าน/ไม่บังคับ 426** — `MOBILE_VERSION_FLOOR_ENFORCE` ไปทำใน slice ถัดไป; `X-App-Flavor=gotest` ได้ (heartbeat/settings ยังส่ง `dev`)
- **Q2 → ใช่: server บังคับ queue gate** (`409 queue_blocked`, R22, AC19)
- **Q3 → ตรวจตอน G5/F10**; ถ้ามือถือเข้า `logi.showkhun.co/media` ไม่ถึง → แก้ `LOCAL_MEDIA_PUBLIC_BASE_URL` บน VM (ไม่แก้โค้ด)
- **Q4 → ใช้ opId ตาม D9** (ไม่แก้ `InsertTrip`)
- **Q5 → ใช้ `defaultTenantId` จาก login** ไปก่อน; ตัวเลือก tenant เป็น follow-up
- **Q6 → ตรวจระหว่าง G7/AC6**; ถ้า `billing.compute` ไม่ stamp `billing_date` ให้ trip จาก Go → แก้ใน slice นี้แล้วบันทึกใน Change Log
- **Q7 → ลำดับถัดไป:** helper picker + manual task → multi-drop → outbox (T56)
- **Q8 → ยอมรับ:** gotest ไม่มี OCR ข้อความจนถึง T57

- **Follow-up:** overlay ของ gotest อ่าน key จาก `.env.dev` ซึ่ง `scripts/build-mobile-gotest.mjs:8` copy มาจาก `envs/.env.dev.mobile` ก่อน build (verified) — ถ้าภายหลังย้ายไป `--dart-define-from-file` (§11.13) ต้องแก้ทั้ง script และ `main.dart`
- **Follow-up:** crash reporting ที่ §11 ระบุว่าเลือกใน T55 — ยังไม่อยู่ใน slice นี้

**สถานะ build (2026-11-18, `/spec-build`):**
- ตรวจแล้ว: `go build ./...`, `go vet` (รวม `-tags integration`), unit test ของ `internal/app`, `internal/auth`, `internal/operations`, `internal/fleet`, `internal/authz`, `cmd/api` ผ่าน; `api/routes.txt`, `api/support-policies.json`, `api/support-discovery.json` และ fixture `support-routes.json` regen แล้ว (เฉพาะส่วนที่เพิ่ม); `flutter analyze` ไม่มี error/warning ใหม่; `flutter test test/go test/go_fence_test.dart` 19/19 ผ่าน และ fence ล้มจริงเมื่อลอง import `cloud_firestore` (AC14)
- AC12 ผ่าน: APK prod ที่ build ใหม่ (`--flavor prod`) ไม่มี `/v1/mobile/tasks`, `GoTestApp`, `runGoTestApp`, `gotest_badge`, `/v1/mobile/uploads/presign` ใน `libapp.so` (APK gotest มี); APK gotest build ผ่าน (`4.0.0-gotest.1`) และ `.env.prod` ที่ bundle เป็นไฟล์ว่าง
- Integration test (testcontainers, 2026-11-18) ผ่านครบ: `internal/operations` (รวม `driver_core_integration_test.go`) และ `internal/auth` ผ่านบน Windows; `internal/iam` (รวมเคส shim ใน `shims_integration_test.go` — AC1), `internal/app`, `internal/fleet`, `internal/platform/httpx/idempotency`, `internal/mobileapp` ผ่านใน container `golang:1.27.3` (Linux) — บน Windows สามชุดนี้ล้มตอน setup เพราะ `storage.NewLocal` รับเฉพาะ path ที่ขึ้นต้นด้วย `/` แต่ `t.TempDir()` เป็น `C:\...` (โค้ดเดิม ไม่เกี่ยวกับงานนี้) → รัน integration บนเครื่อง Windows ให้ใช้ container. AC1, AC4–AC7, AC9, AC19 พิสูจน์ด้วยชุดนี้
- `operationsdb/writes.sql.go` ของ `SetDriverActiveTask` เขียนตามรูปแบบ sqlc ด้วยมือ (sqlc ใช้ cgo ไม่ได้บนเครื่อง Windows นี้) — `make gen-check` ใน CI จะยืนยัน
- test ที่ล้มเพราะเครื่องนี้ใช้ CRLF (`core.autocrlf=true`) ไม่เกี่ยวกับงานนี้: `internal/platform/ingress` Caddyfile tests, `supportcontracts` บอกว่า `supportAccess.ts` stale
- **ต่างจาก spec:** (1) `sta` ของ trip ที่สร้างจากแอปยังเป็น NULL (ยังไม่ได้ lookup distance) → follow-up; (2) route ของ trip ให้เฉพาะคนขับที่เป็น `trip.driver_id` (ผู้ช่วยยังส่งงานแทนไม่ได้); (3) `GET /v1/mobile/trucks` ไม่ส่งรถที่ไม่มี vehicle class มาเลย (แทนการส่งมาแล้วให้แอปปิด)
- **เพิ่มจาก spec (กัน prod):** `scripts/build-mobile-gotest.mjs` แทน `.env.prod` ด้วยไฟล์ว่างระหว่าง build เพราะ `pubspec.yaml` bundle `.env.prod` เป็น asset ทุก flavor — APK gotest จึงไม่พก key/รหัสผ่านของ prod
- **Bug ที่เจอตอนรัน local (2026-11-19) แก้แล้ว:** `GET /v1/mobile/trucks` คืน 0 คันให้คนขับทุกคน เพราะ `DriverTrucks` เรียก `Service.ListTrucks` ที่ใช้ `Principal.TenantReach()` (reach ของ staff ซึ่งว่างสำหรับ driver) → `internal/fleet/driver_trucks.go` query `fleetdb.ListTrucks` ตรงด้วย tenant ของคนขับเอง (RLS `p_driver_read` บังคับซ้ำ); `driver_core_integration_test.go` mount fleet routes และยืนยันว่าได้รถ tenant ตัวเอง ไม่มีรถที่ไม่มี vehicle class และไม่มีรถ tenant อื่น — **staging ต้อง deploy api ตัวนี้ก่อนทำ F10** ไม่อย่างนั้นตัวเลือกรถตอนเช็คอินว่าง
- **รัน gotest กับ stack local (2026-11-20):** `pnpm build:mobile:gotest:local` → version `4.0.0-gotest.<N>.local`, API `http://127.0.0.1:18081` (container `logitrack-api-fwd-public` → `api:8081` เพราะ host port 8081 บนเครื่องนี้ค้าง) แล้ว `adb reverse` พอร์ต API + 9000 (MinIO presign) และ `adb install -r`; `pnpm gotest:reverse` ทำ reverse ซ้ำหลังเสียบสายใหม่. Android บล็อก http → `android/app/src/gotest/res/xml/network_security_config.xml` + `src/gotest/AndroidManifest.xml` เปิด cleartext เฉพาะ `localhost`/`127.0.0.1`/`10.0.2.2` (source set `gotest` ไม่ merge เข้า dev/prod). script ปฏิเสธ URL loopback ใน build staging และ URL ที่ไม่ใช่ loopback ใน build local
- F10 (device checklist บน staging) ยังไม่ทำ: ต้องมีคนขับ + รถที่มี vehicle class + งานที่ assign บน `logi.showkhun.co`

**Slice Google sign-in (2026-11-20, ส่วนหนึ่งของ T55 — developer-spec §11.4, Appendix C §C.4.10):**
- Go ไม่ต้องแก้: `POST /v1/auth/google` รับ token ของแอปคนขับอยู่แล้ว (`aud` = OAuth web client, `azp` = Android client ≠ `aud` → ไม่ต้องส่ง nonce; `internal/auth/google_signin.go`) ตอบเหมือน password login; ไม่มี self-signup → `403 no_account` ถ้าไม่มี user ที่ผูก Google sub หรือมีอีเมล Gmail/Workspace ตรงกัน (ผูกให้อัตโนมัติครั้งแรก)
- แอป: `lib/go/features/auth/data/google_identity_source.dart` (`PluginGoogleIdentitySource`: google_sign_in 7 `initialize(serverClientId)` ครั้งเดียวแบบ memoized + `authenticate()`; cancel = null ไม่มี request); `GoAuthRepository.loginWithGoogle()` → `POST /v1/auth/google {idToken, platform:'android', installId, appVersion}` แล้วใช้ทางเดียวกับ password login (`_openSession` + `_driverOrLogout`); ล้มแล้ว `forget()` ให้เลือกบัญชีใหม่ได้; logout ก็ `forget()`; `PasswordChangeRequired.email` = อีเมลของ Google (ใช้ล็อกอินซ้ำหลังตั้งรหัสใหม่); `LoginBloc` event `GoogleLoginSubmitted`; ปุ่มบน `GoLoginPage` เปิดเมื่อ build มี client id
- Config: `AppFlavor.googleOidcClientId` = `--dart-define=GOOGLE_OIDC_CLIENT_ID` (§11.13); `scripts/build-mobile-gotest.mjs` ส่งค่า `GOOGLE_OIDC_CLIENT_ID` ของ `envs/.env.gotest.mobile` หรือ fallback `FIREBASE_WEB_CLIENT_ID` ของ `envs/.env.dev.mobile` และ fail ถ้าไม่ใช่ OAuth web client (`client_type 3`) ของ `src/gotest/google-services.json`; ว่าง = ปุ่ม disabled
- Go ต้องมี client id นั้นใน `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` (ว่าง = route 404); Android OAuth client ของ `com.wrt.logitrack.gotest` ต้องมี SHA-1 ของ keystore ที่เซ็น APK (APK gotest เซ็นด้วย debug keystore ของเครื่องที่ build)
- i18n en+th: `login_error_google_no_account`, `login_error_google_invalid_token`, `login_error_google_failed`; ปุ่มใช้ key เดิม `sign_in_google`
- Test: `test/go/go_auth_google_test.dart` (6 เคส: body ไม่มี nonce + เก็บ session, cancel ไม่มี request, `no_account` + forget, password change พกอีเมล, phone-side failure, build ไม่มี client id); `flutter analyze lib/go lib/core test/go` ไม่มี issue; `flutter test test/go test/go_fence_test.dart` 25/25 ผ่าน
- ทดสอบแล้ว (2026-11-20) บน emulator Pixel 6 (API 36) กับ stack local: ล็อกอิน Google ด้วยบัญชี Gmail จริงสำเร็จ เข้า Home ของคนขับได้ (user คนขับที่อีเมลตรงกับ Gmail นั้น — ผูก Google sub ให้อัตโนมัติครั้งแรก); ยังไม่ได้ทดสอบบนเครื่องจริงกับ staging (ต้องใส่ client id ใน `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` ของ VM ก่อน)
