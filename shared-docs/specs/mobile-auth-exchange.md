# Spec: Mobile session exchange (`POST /v1/auth/exchange`) — คนขับอัปเกรด 3.x → 4.x โดยไม่ต้องล็อกอินใหม่

> **Status:** 🔵 In progress (approved 2026-11-20 via `/spec-build`)
> **Owner:** smartcode54-bit
> **Created:** 2026-11-20
> **Domain:** auth (Go `internal/auth`) + mobile (`lib/go/features/auth`)
> **Related:** `developer-spec.md` §11.4 (แถว "First 4.0.0 launch"), §11.12, §12.2 P7a; Appendix C §C.4.6, §C.6.1–§C.6.2, §C.8 (route row); Appendix B §B.1 (route row `/v1/auth/exchange`, `rl:exchange_ip`); task **T55** (ส่วนที่เหลือหลัง core loop + Google sign-in); spec `mobile-go-core-loop.md` (D3 fence, gotest flavor); branch `mv-go-T55-mobile-gotest`

---

## 1. Problem & Goal (ทำไมต้องทำ)

ตอนออก APK 4.0.0 (Go-only) คนขับทั้งฟลีตต้องเข้าใช้ต่อได้ทันทีโดย **ไม่ต้องพิมพ์รหัสผ่านใหม่** (R42) — คนขับหลายคนจำรหัสไม่ได้ และถ้าทุกคนต้องขอรหัสชั่วคราวจากแอดมินพร้อมกันวันปล่อยเวอร์ชัน งานหน้างานจะหยุด. APK 3.x เก็บ session ของ Firebase Auth ไว้ในเครื่อง → ตอนเปิด 4.x ครั้งแรก แอปเอา Firebase ID token นั้นไปแลกเป็น Go token pair หนึ่งครั้ง แล้วเลิกใช้ Firebase Auth.
เป้าหมาย: (1) route `POST /v1/auth/exchange` ใน Go ตาม Appendix C, (2) flow first-launch ในแอป, (3) พิสูจน์บนเครื่องจริงว่า session ของ FlutterFire **รอดการติดตั้งทับ** (developer-spec §11.4 ระบุว่า UNVERIFIED).

## 2. Scope

**In scope:**
- **Go:** `POST /v1/auth/exchange` (handler + service) ใช้ของที่มีแล้ว: `VerifyFirebaseIDToken(…, FirebaseAPK)` (`internal/auth/bridge_idtoken.go`), `openSession` (`internal/auth/login.go`), bucket `ratelimit.ExchangeIP`; amr mapping; must-change-password ticket; unit + integration tests; regen `api/routes.txt`
- **Mobile (gotest code path, `lib/go/**`):** ตอน cold start ที่ไม่มี Go refresh token → อ่าน Firebase session เดิม → exchange → เก็บ Go token → ปิด Firebase session; ไฟล์เดียวใน `lib/go` ที่ได้ import `firebase_core` + `firebase_auth` (ข้อยกเว้นของ fence D3)
- **Install-over build:** `scripts/build-mobile-gotest.mjs --upgrade-test` — build โค้ด gotest ภายใต้ **package ของ dev** (`com.example.logi_track_driver_app`, Firebase dev) เพื่อติดตั้งทับแอป dev 3.x ที่ล็อกอินค้างไว้ = สถานการณ์จริงของวันปล่อย 4.0
- **Local stack:** เปิด `AUTH_FIREBASE_BRIDGE_MODE=mobile` กับโปรเจกต์ **dev** (`logi-track-wrt-dev`) + dev service account; ผูก user local 1 คนกับ uid Firebase dev จริง
- i18n en + th, docs (spec นี้ + `mobile-go-core-loop.md` D3 note + Appendix B "As built" + `.vibe-rules.md` Change Log)

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- เปลี่ยน flavor dev/prod ให้รันโค้ด Go (= ตัว cut-over 4.0 จริง, T55 release/P7a) — slice นี้ทำแค่ build ทดสอบแบบ install-over
- เปิด mode `mobile` บน staging / prod (เป็นการตัดสินใจ rollout ของ P7a, §11.12)
- iOS (ยังไม่มี iOS build); `GoogleSignIn` session ของ 3.x (ไม่ต้องใช้ — exchange ใช้ Firebase ID token ไม่ว่า 3.x จะล็อกอินด้วยรหัสหรือ Google)
- Fallback "login form + รหัสชั่วคราวจากแอดมิน (R29)" มีอยู่แล้ว ไม่ต้องทำเพิ่ม
- SSE/FCM revocation, outbox (T56/T57)

## 3. Requirements

**Functional — Go**
- **R1 — Route.** `POST /v1/auth/exchange {idToken, platform, installId, appVersion?}` mount ใน `mountAuth` ข้าง `/login`, `/google` (listener เดียวกับสองตัวนั้น; BFF generic proxy คืน 404 ให้ `v1/auth/exchange` อยู่แล้วตาม §C.8 — ตรวจยืนยันใน T2); `Cache-Control: no-store` (ได้จาก `noStore` ของกลุ่ม)
- **R2 — Gate.** route ตอบ `404 not_found` เมื่อ mode ไม่รวม `mobile` (`off`/`web`) หรือไม่มี `FIREBASE_PROJECT_ID` — ได้จาก `VerifyFirebaseIDToken(FirebaseAPK)` อยู่แล้ว; **ตรวจ gate ก่อน validate body** เพื่อไม่ให้ route ที่ปิดอยู่เผยรูปแบบ body (เหมือน `GoogleSignIn` ที่เช็ค `s.google == nil` ก่อน)
- **R3 — Validation.** `idToken` required, ≤ 8 KB (`maxIDTokenBytes`); `platform` ∈ {`android`,`ios`} (`web` → `422 one_of` — web ใช้ exchange ไม่ได้); `installId` required, ≤ 200; `appVersion` ≤ 64 — ผิด → `422 invalid_argument` + field violations
- **R4 — Rate limit.** `ratelimit.ExchangeIP` (30/นาที/IP) ก่อน verify token → `429` + `Retry-After`
- **R5 — Verify + resolve.** `VerifyFirebaseIDToken(ctx, idToken, FirebaseAPK)` → ผลลัพธ์ตาม §C.6.2: `401 invalid_token` (token เสีย / `auth_time < password_changed_at`), `403 no_account` (ไม่มี user ที่ `legacy_auth_uid = sub`), `403 account_disabled`, `503 unavailable` (ดึง key ไม่ได้)
- **R6 — amr.** `firebase.sign_in_provider`: `password` → `pwd`, `google.com` → `google`; ค่าอื่น (`custom`, `anonymous`, `phone`, …) → `401 invalid_token` + log `reason=provider_unsupported` (fail loud — APK 3.x ใช้แค่สองแบบนี้)
- **R7 — Must change password.** user ที่ `must_change_password` → ไม่ออก token; ตอบ `403 password_change_required` + `passwordChangeTicket` (ใช้ `s.passwordChangeTicket` เดิม, R79)
- **R8 — Session.** `openSession(uid, grant{amr, firebaseUID: sub}, LoginInput{platform, installId, appVersion, IP, UA, requestID})` → body เหมือน login (`accessToken`, `refreshToken`, `expiresIn`, `tenants`, `defaultTenantId`); session ใหม่แทน session เดิมของ install เดียวกัน (R83, พฤติกรรมเดิมของ `createSession`); outbox `user.logged_in` (มีใน `openSession` แล้ว)
- **R9 — Race guard.** `grant` ได้ field ใหม่ `firebaseUID`; ใน transaction ของ `openSession` ถ้า `firebaseUID != ""` ต้องเช็คว่าแถว user ที่ lock ยังมี `legacy_auth_uid = firebaseUID` (ไม่ตรง → `403 no_account`) — แบบเดียวกับที่ `googleSub` เช็ค link ซ้ำใน tx
- **R10 — ไม่เก็บ credential ของ Firebase.** Go ไม่บันทึก ID token / uid ลง session หรือ log (log ได้แค่ `reason`, `platform`, `request_id`)

**Functional — Mobile**
- **R11 — เมื่อไรจะ exchange.** `GoAuthRepository.resume()`: มี Go refresh token → พฤติกรรมเดิม (R4 ของ core loop). **ไม่มี** → ถาม `LegacyFirebaseSession.read()`; ได้ `null` (ไม่มี Firebase user) → `null` = หน้า login ตามเดิม; ได้ session → exchange
- **R12 — อ่าน session เดิม.** `firebase_session_source.dart`: `Firebase.initializeApp(options: DefaultFirebaseOptions.currentPlatform)` (ชื่อ app `[DEFAULT]` — ต้องตรงกับ 3.x เพราะ FlutterFire เก็บ session ตามชื่อ app) → รอ user ที่ restore แล้วด้วย `authStateChanges().first` (timeout 5 s) → `getIdToken()` (ต่ออายุเองถ้าหมด — ต้องมีเน็ต) → `{idToken, email}`; เน็ตล้ม (`network-request-failed`) → `NetworkException`
- **R13 — ผลของ exchange.**
  | ผล | แอปทำ | Firebase session |
  |---|---|---|
  | 200 | เก็บ token (`_openSession`) → `_driverOrLogout()` → Home | `end()` (sign out + ลบ app) |
  | `403 password_change_required` | หน้าตั้งรหัส (email = อีเมลของ Firebase user) | `end()` |
  | `401 invalid_token`, `403 no_account`, `403 account_disabled` | หน้า login + ข้อความ `login_notice_exchange_relogin` (หรือ `login_error_account_disabled`) | `end()` — ไม่ลองซ้ำทุกครั้งที่เปิดแอป |
  | `404` (server ยังไม่เปิด mode `mobile`) | หน้า login ไม่มีข้อความ | **เก็บไว้** — rollout ทีหลังยังแลกได้ |
  | เน็ตล้ม / `503` / timeout | `SessionOffline` (ปุ่มลองใหม่) | **เก็บไว้** |
  | `403 driver_profile_required` (จาก `/v1/mobile/me` หลังแลก) | หน้า login + `login_error_driver_profile_required` (logout Go ตามเดิม) | `end()` |
- **R14 — exchange ครั้งเดียว.** หลัง `end()` ไม่มี Firebase user เหลือ → เปิดแอปครั้งต่อไปเข้า R11 ทาง "มี Go refresh token" หรือหน้า login; ไม่มี loop
- **R15 — Fence.** `firebase_core` + `firebase_auth` import ได้ **เฉพาะ** `lib/go/features/auth/data/firebase_session_source.dart`; `test/go_fence_test.dart` ได้ allowlist ไฟล์นี้ไฟล์เดียว และยังห้าม `cloud_firestore`, `firebase_storage`, `cloud_functions`, `firebase_app_check` ทุกที่ใน `lib/go`. หลัง `end()` เรียก `Firebase.app().delete()` เพื่อคืน runtime fence ของ D3 (การเรียก Firebase โดยไม่ตั้งใจหลังจากนั้นจะ throw `[core/no-app]` เหมือนเดิม)
- **R16 — Firebase init เฉพาะตอนจำเป็น.** ไม่ init Firebase ตอน cold start ที่มี Go refresh token; init เฉพาะใน R11 กรณีไม่มี Go token

**Functional — Install-over build**
- **R17 — `--upgrade-test`.** `pnpm build:mobile:gotest:upgrade` (= `--local --upgrade-test`): `--flavor dev` + `--dart-define=FLAVOR=gotest` + `API_BASE_URL` = `LOCAL_API_BASE_URL` + `GOOGLE_OIDC_CLIENT_ID`; versionName `4.0.0-gotest.<N>.upgrade`, versionCode `400000 + N` (สูงกว่า build dev ใดๆ จึงติดตั้งทับได้); `adb reverse` + `adb install -r`
- **R18 — Guard.** `src/dev/google-services.json` ต้องเป็นโปรเจกต์เดียวกับ `envs/.env.dev.mobile` และ **ไม่ใช่ prod**; ห้ามใช้ `--upgrade-test` กับ flavor อื่น; `.env.prod` สลับเป็นไฟล์ว่างระหว่าง build เหมือนเดิม
- **R19 — HTTP ไป loopback.** build ปกติของ flavor dev **ไม่เปลี่ยน**: config cleartext-loopback ใส่ผ่าน gradle property `logitrackUpgradeTest=true` (`--android-project-arg`) → manifest placeholder `networkSecurityConfig`; ไม่ส่ง property = manifest ของ dev เหมือนเดิมทุกไบต์ (ตรวจ AC)

**Non-functional**
- **N1.** i18n ครบ `en` + `th` (key ใหม่ §4.6), จำนวน key เท่ากันสองไฟล์
- **N2.** Security: ไม่ log/เก็บ ID token, uid ใน log ฝั่งแอป (`debugPrint` ห้ามพิมพ์ token); `rl:exchange_ip`; route ปิดโดย default (mode `off`)
- **N3.** dev/prod APK ไม่เปลี่ยนพฤติกรรม (AC12 ของ core loop ยังผ่าน: prod `libapp.so` ไม่มี `/v1/auth/exchange`)
- **N4.** `go test ./...` + integration เขียว, `make gen-check` (routes.txt) ผ่าน, `flutter analyze` ไม่มี issue ใหม่, `flutter test` เขียว

## 4. Design

### 4.1 การตัดสินใจหลัก

| # | ตัดสินใจ | เหตุผล |
|---|---|---|
| E1 | ทำทั้ง Go + แอปใน slice นี้ (ผู้ใช้เลือก 2026-11-20) | ปิด T55; โค้ดฝั่งแอปใน `lib/go/**` คือโค้ดที่ 4.0 จริงจะใช้ |
| E2 | ทดสอบด้วย install-over build บน package dev (ผู้ใช้เลือก) | เป็นสถานการณ์จริงเดียวที่พิสูจน์ "session รอดการติดตั้งทับ" ได้; package gotest ไม่เคยมี session 3.x |
| E3 | ยอมให้ 1 ไฟล์ใน `lib/go` import Firebase Auth (ข้อยกเว้น fence) + `app.delete()` หลังใช้ | exchange ต้องอ่าน session ของ FlutterFire ซึ่งมีทางเดียวคือ `firebase_auth`; จำกัดเหลือไฟล์เดียว + allowlist ใน test ทำให้ยังตรวจได้ |
| E4 | ใช้ `VerifyFirebaseIDToken` + `openSession` เดิม ไม่เขียน verify ใหม่ | ของ T08 ผ่าน test แล้ว ครอบ gate/mode/disabled/password-changed ครบ |
| E5 | provider อื่นนอก `password`/`google.com` → 401 | fail loud ตาม ADR 0003 แนวคิดเดียวกัน; ไม่เดา amr |
| E6 | 404/เน็ตล้ม → เก็บ Firebase session; 401/403 → ปิด Firebase session | 404 = server ยังไม่พร้อม (ควรได้แลกทีหลัง); 401/403 = แลกไม่ได้แน่นอน ลองซ้ำทุกครั้งที่เปิดแอปไม่มีประโยชน์ |
| E7 | local เปิด mode `mobile` กับ Firebase **dev** + dev SA (ผู้ใช้เลือก) | ทดสอบ end-to-end ได้จริง; mirror เขียนแค่ Firebase Auth ของ dev |

### 4.2 Go

`internal/auth/exchange.go` (ใหม่):
```go
type ExchangeInput struct {
    IDToken    string `json:"idToken"`
    Platform   string `json:"platform"`
    InstallID  string `json:"installId"`
    AppVersion string `json:"appVersion"`
    IP, UserAgent, RequestID string `json:"-"`
}

// Exchange is POST /v1/auth/exchange (R42, Appendix C §C.4.6).
func (s *Service) Exchange(ctx context.Context, in ExchangeInput) (*LoginResult, error)
// 1. gate: !s.fb.Mode.Mobile() || s.fb.Verifier == nil -> 404            (R2)
// 2. validateExchange(&in) -> 422                                         (R3)
// 3. s.limit(ctx, ratelimit.ExchangeIP, ipSubject(in.IP)) -> 429          (R4)
// 4. id, err := s.VerifyFirebaseIDToken(ctx, in.IDToken, FirebaseAPK)     (R5)
// 5. amr := amrForProvider(id.SignInProvider) ("" -> 401)                 (R6)
// 6. id.MustChangePassword -> s.passwordChangeTicket(ctx, uid, authVer)   (R7)
// 7. s.openSession(ctx, id.Principal.UserID, grant{amr, firebaseUID: id.UID}, LoginInput{...})  (R8)
```
- `handleExchange` ใน `internal/auth/http.go` (แบบ `handleGoogle`), `r.Post("/exchange", s.handleExchange)` ใน `mountAuth`
- `login.go`: `grant.firebaseUID string`; ใน tx ของ `openSession` ต่อจากเช็ค `googleSub`: `if g.firebaseUID != "" && (user.LegacyAuthUid == nil || *user.LegacyAuthUid != g.firebaseUID) { return errNoAccount() }` — **UNVERIFIED** ว่า `authdb.LockUserRow` มี `LegacyAuthUid` แล้ว ถ้าไม่มีให้เพิ่มใน `queries/*.sql` ของ `LockUser` แล้ว `make gen` (บน Windows เขียน sqlc output ด้วยมือได้แบบ `SetDriverActiveTask` แล้วให้ CI `gen-check` ยืนยัน)
- `passwordChangeTicket` ต้องได้ `AuthVersion` — ถ้า `FirebaseIdentity` ไม่มี ให้เพิ่ม field `AuthVersion` (มีใน `Principal.AuthVersion` แล้ว → ใช้ตัวนั้น)
- ไม่มี migration, ไม่มี route ใหม่ใน web BFF

**Config local (ไม่ commit — `logitrack-api/.env` เป็น gitignored):**
```
FIREBASE_PROJECT_ID=logi-track-wrt-dev
AUTH_FIREBASE_BRIDGE_MODE=mobile
GOOGLE_APPLICATION_CREDENTIALS=/run/secrets/logitrack/<dev-sa>.json   # ไฟล์ SA ของ dev วางใน logitrack-api/deploy/dev-secrets/ (mount ro ใน compose:312)
```
- ⚠️ mode `mobile` เปิด **account mirror**: แก้ user ใน Go local (รหัสผ่าน, อีเมล, disable) จะเขียนไป Firebase Auth ของ **dev** ตาม uid ที่ผูก — ยอมรับ (dev เท่านั้น); ห้ามใช้ SA ของ prod (start-up validate ไม่ได้กันข้อนี้ → ตรวจด้วยตาตอนตั้งค่า + เช็ค `project_id` ในไฟล์ SA ก่อน)
- ผูก user: local Postgres `UPDATE users SET legacy_auth_uid = '<uid Firebase dev ของบัญชีที่ล็อกอินในแอป dev 3.x>' WHERE id = '<user คนขับ local>'` (local เท่านั้น; uid ดูจาก Firebase Console dev → Authentication); user นั้นต้องผูกกับ driver อยู่แล้ว (เช่น d1 สมชาย)

### 4.3 Mobile

`lib/go/features/auth/data/firebase_session_source.dart` (ใหม่ — ไฟล์เดียวที่ import Firebase):
```dart
class LegacySession { final String idToken; final String email; }

abstract class LegacyFirebaseSession {
  /// The still-signed-in Firebase user of APK 3.x, or null. Throws NetworkException when the token needs a refresh
  /// and the phone is offline.
  Future<LegacySession?> read();
  /// Signs out of Firebase and deletes the default app (restores the D3 runtime fence). Never throws.
  Future<void> end();
}

class FlutterFireLegacySession implements LegacyFirebaseSession { ... }   // R12, R15, R16
```

`GoAuthRepository` (`go_auth_repository.dart`):
- ctor รับ `LegacyFirebaseSession? legacy` (null ใน test ที่ไม่สนใจ / build ที่ไม่ต้องการ)
- `resume()` → ถ้าไม่มี refresh token → `_exchangeLegacy()` (R11, R13):
  - `legacy.read()` → null → return null
  - `_openSession('/v1/auth/exchange', {'idToken': s.idToken}, email: s.email)` (reuse ทั้ง body platform/installId/appVersion และ `PasswordChangeRequired`)
  - จัดการผลตามตาราง R13: ความผิดพลาด "แลกไม่ได้แน่นอน" โยน `ExchangeRefused(messageKey)` ให้ `SessionBloc` แสดงข้อความ
- `SessionBloc._onStarted`: จับ `PasswordChangeRequired` → `SessionNeedsPassword(email, ticket)`; `ExchangeRefused` → `SessionUnauthenticated(messageKey: ...)`
- `GoServices.create`: ส่ง `FlutterFireLegacySession()` เข้า repository

**Fence (`test/go_fence_test.dart`):** allowlist `lib/go/features/auth/data/firebase_session_source.dart` สำหรับ `package:firebase_core/` + `package:firebase_auth/` เท่านั้น; เพิ่ม `package:firebase_core/` ลงรายการห้ามของไฟล์อื่น (ตอนนี้ไม่มีในรายการ)

**pubspec:** ไม่เพิ่ม dependency (`firebase_core`, `firebase_auth` มีอยู่แล้ว); ไม่ bump version (gotest ใช้ `--build-name`)

### 4.4 Install-over build

`scripts/build-mobile-gotest.mjs`:
- flag `--upgrade-test` (ต้องคู่กับ `--local`): `--flavor dev`, guard R18 (`googleServices("dev")` project == `.env.dev.mobile` project, ≠ prod), version R17, `--android-project-arg=logitrackUpgradeTest=true`, ชื่อ APK ใน `build/app/outputs/flutter-apk/` ของ flavor dev
- package.json root: `"build:mobile:gotest:upgrade": "node scripts/build-mobile-gotest.mjs --local --upgrade-test"`

`android/app/build.gradle.kts` + `src/main/AndroidManifest.xml`:
- `manifestPlaceholders["networkSecurityConfig"]` = `"@xml/network_security_config_loopback"` เมื่อ `project.findProperty("logitrackUpgradeTest") == "true"` มิฉะนั้นไม่ใส่ attribute (**UNVERIFIED**: manifest placeholder ใส่ attribute แบบมีเงื่อนไขไม่ได้ตรงๆ → ทางเลือกที่ทดสอบใน T6: source set เพิ่ม `src/upgradeTest/` ผ่าน `sourceSets["dev"].manifest.srcFile` แบบมีเงื่อนไข หรือ `res` overlay; เลือกทางที่ build ปกติของ dev ได้ manifest เดิม — ตรวจด้วย `aapt dump xmltree` ใน AC)
- ย้าย/คัดลอก `network_security_config.xml` ของ gotest เป็นไฟล์ loopback-only ใช้ร่วม

**ขั้นตอนทดสอบ (T8):** ติดตั้ง dev 3.x (build จากเครื่องนี้ เพื่อ signature เดียวกัน — debug keystore) → ล็อกอินด้วยรหัส (และอีกรอบด้วย Google) → `pnpm build:mobile:gotest:upgrade` → เปิดแอป → ต้องเข้า Home โดยไม่เห็นหน้า login

### 4.5 Data model
- ไม่มี collection/table/column ใหม่; ใช้ `users.legacy_auth_uid` (มีแล้ว), `sessions` (แถวใหม่ amr `pwd`/`google`)

### 4.6 i18n (mobile, `assets/translations/{en,th}.json`)
| key | en | th |
|---|---|---|
| `login_notice_exchange_relogin` | Your previous sign-in could not be carried over. Please sign in again. | ไม่สามารถใช้การเข้าสู่ระบบเดิมต่อได้ กรุณาเข้าสู่ระบบอีกครั้ง |

(ตัด `session_exchanging` ออกตอน build: หน้าเริ่มแอปเป็น spinner ไม่มีข้อความ key นี้จะไม่ถูกใช้)

(ใช้ key เดิม: `login_error_account_disabled`, `login_error_driver_profile_required`, `api_error_network`)

**Web / Firestore rules / billing:** ไม่แตะ

## 5. Affected files

**Go (`logitrack-api/`)**
- `internal/auth/exchange.go` (ใหม่) + `exchange_test.go` (ใหม่, fake verifier `internal/auth/firebase/firebasetest`)
- `internal/auth/http.go` (`handleExchange`, mount)
- `internal/auth/login.go` (`grant.firebaseUID` + เช็คใน tx)
- `internal/auth/bridge_integration_test.go` (เคส exchange end-to-end กับ Postgres)
- `internal/auth/authdb/*` + `queries/*.sql` (ถ้า `LockUserRow` ไม่มี `legacy_auth_uid`)
- `api/routes.txt`, `api/support-*.json` (regen), `README.md` route list

**Mobile (`logitrack-mobile/`)**
- `lib/go/features/auth/data/firebase_session_source.dart` (ใหม่)
- `lib/go/features/auth/data/go_auth_repository.dart`
- `lib/go/core/session/session_bloc.dart`
- `lib/go/app/go_services.dart`
- `test/go/go_auth_exchange_test.dart` (ใหม่), `test/go_fence_test.dart`
- `assets/translations/en.json`, `th.json`
- `android/app/build.gradle.kts`, `android/app/src/main/AndroidManifest.xml` (หรือ source set ใหม่), `android/app/src/**/res/xml/network_security_config_loopback.xml`

**Scripts / docs**
- `scripts/build-mobile-gotest.mjs`, `package.json` (root)
- `envs/.env.gotest.mobile.example` (อธิบาย `--upgrade-test`)
- `shared-docs/specs/mobile-auth-exchange.md` (spec นี้), `shared-docs/specs/mobile-go-core-loop.md` (D3: ข้อยกเว้น), `shared-docs/specs/mv-go/B-api-catalog.md` ("As built"), `shared-docs/.vibe-rules.md` (Change Log + Confirmed Pattern fence), `shared-docs/runbook-run-deploy.md` (หัวข้อ install-over test)

## 6. Task breakdown
> Go ก่อน (test ได้เดี่ยว) → แอป → build → ทดสอบบนเครื่อง

- [x] **T1.** Go: `exchange.go` + `grant.firebaseUID` + `handleExchange` + mount; unit tests ครบเคส R2–R9 (mode off/web 404, ไม่มี project 404, gate ก่อน validate, platform web 422, provider อื่น 401, must-change 403 ticket, disabled 403, uid ไม่รู้จัก 403, auth_time ก่อนเปลี่ยนรหัส 401, keys ล่ม 503, 429, สำเร็จ amr pwd/google + install id + outbox `user.logged_in`)
- [x] **T2.** Go: integration test (Postgres) + race guard R9 (เปลี่ยน `legacy_auth_uid` ระหว่าง verify กับ tx → 403); ยืนยัน BFF proxy 404 `v1/auth/exchange` (อ่าน `logitrack-web/app/api/go/[...path]/route.ts`); `make gen`, `go test ./...`, `go vet`
- [x] **T3.** Mobile: `firebase_session_source.dart` + `GoAuthRepository` exchange + `SessionBloc` + `GoServices`
- [x] **T4.** Mobile: `test/go/go_auth_exchange_test.dart` (fake `LegacyFirebaseSession` + `FakeAdapter`: ทุกแถวของตาราง R13, มี Go token แล้วไม่แตะ legacy (R16), exchange ครั้งเดียว R14) + fence test allowlist (R15)
- [x] **T5.** i18n en + th (§4.6)
- [x] **T6.** Android: cleartext-loopback แบบมีเงื่อนไข (R19) — พิสูจน์ว่า dev build ปกติได้ manifest เดิม
- [x] **T7.** `build-mobile-gotest.mjs --upgrade-test` + script `build:mobile:gotest:upgrade` + guards R17–R18
- [ ] **T8.** Local e2e: ตั้งค่า §4.2 (dev SA, mode mobile, ผูก uid), ติดตั้ง dev 3.x + ล็อกอิน (รหัส 1 รอบ, Google 1 รอบ) → install-over → Home โดยไม่ถามรหัส; เคส 404 (mode off) แล้วเปิด mode → แลกได้รอบถัดไป
- [ ] **T9.** Docs: Appendix B "As built", `mobile-go-core-loop.md` D3, runbook, `.vibe-rules.md` Change Log + Confirmed Pattern

## 7. Acceptance criteria (ตรวจรับ)
- [ ] **AC1** (R2) mode `off` และ `web`: `POST /v1/auth/exchange` ด้วย body ใดๆ (รวม body ผิดรูป) → `404`
- [ ] **AC2** (R3, R4) mode `mobile`: `platform:"web"` → `422`; ยิงเกิน 30 ครั้ง/นาที/IP → `429` + `Retry-After`
- [ ] **AC3** (R5, R6, R8) token Firebase ที่ `sign_in_provider=password` → 200 token pair, แถว `sessions` มี `amr='pwd'`, `install_id` = ที่ส่ง; `google.com` → `amr='google'`; provider อื่น → 401; outbox `user.logged_in` 1 แถวต่อ exchange
- [ ] **AC4** (R5) user ที่เปลี่ยนรหัสหลัง `auth_time` ของ token → 401; user disabled → 403 `account_disabled`; uid ไม่มีใน `users` → 403 `no_account`
- [ ] **AC5** (R7) user `must_change_password` → 403 `password_change_required` + ticket; ไม่มีแถว `sessions` ใหม่
- [ ] **AC6** (R9) integration: `legacy_auth_uid` ถูกเปลี่ยนหลัง verify ก่อน tx → 403 `no_account`, ไม่มี session
- [ ] **AC7** (R10, N2) log ของ api ระหว่าง AC3–AC6 ไม่มี ID token หรือ uid (grep log)
- [ ] **AC8** (R11, R13, R14, R16) unit tests ฝั่งแอปครบทุกแถวของตาราง R13 + มี Go token แล้ว `read()` ไม่ถูกเรียก + หลัง `end()` เปิดครั้งถัดไปไม่ exchange ซ้ำ
- [ ] **AC9** (R15) `test/go_fence_test.dart` ผ่าน และล้มจริงเมื่อเติม `import 'package:firebase_auth/firebase_auth.dart';` ในไฟล์อื่นใต้ `lib/go` (ทดลองแล้ว revert)
- [ ] **AC10** (R17, R18, R19, N3) `build:mobile:gotest:upgrade` ได้ APK package `com.example.logi_track_driver_app` เวอร์ชัน `4.0.0-gotest.N.upgrade` versionCode `400000+N`; ใช้ `src/prod/google-services.json` → build ล้ม; `build:mobile:dev` ปกติ manifest ไม่มี `networkSecurityConfig` (เทียบ `aapt dump xmltree` ก่อน/หลัง); APK prod `libapp.so` ไม่มี `/v1/auth/exchange`
- [ ] **AC11** (เป้าหมายหลัก, R11–R13) **บน emulator/เครื่องจริง:** dev 3.x ล็อกอินด้วยรหัสผ่าน → ติดตั้ง upgrade build ทับ → เปิดแอป → เข้า Home ของคนขับโดยไม่เห็นหน้า login; ทำซ้ำกับ 3.x ที่ล็อกอินด้วย Google → ได้ `amr='google'`; ผลบันทึกใน §9 (session รอดการติดตั้งทับหรือไม่ = ปิด UNVERIFIED ของ developer-spec §11.4)
- [ ] **AC12** (R13 แถว 404) mode `off` → เปิด upgrade build → หน้า login ไม่มีข้อความ, Firebase user ยังอยู่; เปลี่ยน mode เป็น `mobile` + restart api → เปิดแอปใหม่ → แลกสำเร็จ
- [ ] **AC13** (N1) en/th มี key ใหม่ครบ จำนวนเท่ากัน; ไม่มี string hardcode ใน `lib/go/**`
- [ ] **AC14** (N4) `go test ./...` + integration เขียว, `make gen-check` ผ่าน, `flutter analyze` ไม่มี issue ใหม่, `flutter test` เขียว

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Firebase session **ไม่รอด** การติดตั้งทับบางเครื่อง (UNVERIFIED §11.4) | AC11 พิสูจน์บน emulator; ถ้าไม่รอด → fallback เดิม: หน้า login + รหัสชั่วคราวจากแอดมิน (R29) และบันทึกผลกลับเข้า developer-spec §11.4 |
| ข้อยกเว้น fence ทำให้โค้ด Firebase อื่นแอบเข้า `lib/go` | allowlist **ไฟล์เดียว** ใน test (AC9); `app.delete()` หลังใช้คืน runtime fence |
| mirror ของ mode `mobile` เขียน Firebase Auth ผิดโปรเจกต์ | local ใช้ SA ของ **dev** เท่านั้น — เช็ค `project_id` ในไฟล์ SA ก่อนตั้งค่า (T8); prod/staging ไม่เปิด mode นี้ใน slice นี้ |
| upgrade build ติดตั้งทับไม่ได้ (signature ต่าง: dev 3.x ที่ติดตั้งอยู่ build จากเครื่องอื่น) | build dev 3.x จากเครื่องนี้ก่อน (T8); เอกสาร `INSTALL_FAILED_UPDATE_INCOMPATIBLE` ใน runbook |
| ID token หมดอายุ (> 1 ชม.) + เครื่องออฟไลน์ตอนเปิด 4.x ครั้งแรก | R13: เน็ตล้ม → Offline + เก็บ Firebase session → ลองใหม่เมื่อมีเน็ต (`getIdToken()` ต่ออายุเอง) |
| เปลี่ยน `openSession` (race guard) กระทบ login/Google | เช็คทำงานเฉพาะ `firebaseUID != ""`; test login/Google เดิมต้องผ่านโดยไม่แก้ |
| Rollback | Go: ถอด mount `/exchange` (หรือแค่ mode `off` = 404); แอป: ถ้า `legacy` เป็น null พฤติกรรมเท่า core loop เดิม; ไม่มี migration |

## 9. Open questions / follow-ups
- **Q1 (UNVERIFIED, ตอบใน T1):** `authdb.LockUserRow` มี `LegacyAuthUid` หรือยัง — ถ้าไม่มีต้องแก้ query + regen
- **Q2 (UNVERIFIED, ตอบใน T6):** วิธีใส่ `networkSecurityConfig` แบบมีเงื่อนไขที่ทำให้ dev build ปกติได้ manifest เดิมทุกไบต์
- **Q3:** ผล AC11 (session รอดการติดตั้งทับ) → อัปเดต developer-spec §11.4 จาก UNVERIFIED เป็นผลจริง
- **Follow-up (P7a release):** เปิด mode `mobile` บน staging → prod ตาม §11.12; ตัวแอป 4.0 จริง (flavor dev/prod รันโค้ด Go) ใช้ `FlutterFireLegacySession` ตัวเดียวกันนี้
- **Follow-up:** iOS — FlutterFire เก็บ session ใน Keychain ซึ่งอาจรอดแม้ลบแอป; ทดสอบเมื่อมี iOS build
