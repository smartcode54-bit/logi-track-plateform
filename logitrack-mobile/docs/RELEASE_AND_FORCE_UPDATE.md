# ปล่อยเวอร์ชันแอปคนขับ + บังคับอัปเดต (Build → Storage → Firestore)

คู่มือปฏิบัติสำหรับการ build APK, ประกาศเวอร์ชันขึ้น Firestore, และบังคับให้คนขับอัปเดต

สเปกและเหตุผลเบื้องหลังอยู่ที่ **`shared-docs/adr/0007-mobile-forced-update-pipeline.md`** — ไฟล์นี้เน้น "ทำยังไง"

---

## ⚠️ อ่านก่อน: เรื่อง keystore (สำคัญที่สุด)

`logitrack-mobile/android/key.properties` **ไม่มีในเครื่อง** → `android/app/build.gradle.kts` fallback ไปเซ็น release ด้วย **debug keystore ของเครื่องที่ build**

APK ที่ปล่อยอยู่ตอนนี้ (`logitrack-prodRelease-v2.9.2.apk`) เซ็นด้วย:

```
Signer #1 certificate DN : C=US, O=Android, CN=Android Debug
SHA-256                  : f444175d...be022
= C:\Users\acer\.android\debug.keystore  (สร้าง 6 ก.พ. 2026, หมดอายุ 2056)
```

**ทำไมสำคัญกับการบังคับอัปเดตเป็นพิเศษ:** Android ปฏิเสธการติดตั้งทับเมื่อ signature ไม่ตรง คนขับที่โดนบล็อกจะโหลด APK มาแล้ว **ติดตั้งไม่ได้ → ติดค้างถาวร ไม่มีทางออก**

| สถานการณ์ | ผล |
|-----------|-----|
| build จากเครื่องเดิม | ✅ อัปทับได้ปกติ |
| build จากเครื่องอื่น | ❌ ติดตั้งทับไม่ได้ |
| `debug.keystore` หาย / ถูกสร้างใหม่ | ❌ อัปเดตพังถาวร ต้องให้ทุกคน uninstall |
| เพิ่ม `key.properties` ตอนนี้ | ❌ ทุกเครื่องที่ติดตั้งอยู่อัปทับไม่ได้ |

**ต้องทำ:**

1. **Backup `C:\Users\acer\.android\debug.keystore`** ไว้ที่ปลอดภัย — ไฟล์นี้หาย = อัปเดตแอปทั้งฟลีตพังถาวร
2. **build จากเครื่องเดิมเสมอ** ถ้าจำเป็นต้องย้ายเครื่อง ให้ copy `debug.keystore` ไปวางที่ `%USERPROFILE%\.android\` ของเครื่องใหม่ก่อน
3. **อย่าเพิ่ม `key.properties`** จนกว่าจะพร้อมให้ทุกคน uninstall + install ใหม่ ถ้าจะย้ายไป release key ต้องทำ **ก่อน** บังคับอัปเดต ไม่ใช่พร้อมกัน

ตรวจ signature ของ APK:

```powershell
$as = "$env:LOCALAPPDATA\Android\Sdk\build-tools\36.1.0\apksigner.bat"
& $as verify --print-certs <path-to.apk>
```

---

## หลักการที่ต้องเข้าใจก่อนใช้

`settings/mobile_app` ใน Firestore มี **2 ผู้เขียนที่เขียนคนละฟิลด์ ไม่ทับกัน**

| ฟิลด์ | เขียนโดย | ความหมาย |
|-------|---------|----------|
| `latestVersion`, `apkDownloadUrl`, `apkSha256`, `apkSizeBytes`, `flavor`, `releasedAt`, `releasedBy` | **script** | ประกาศว่าปล่อยอะไรไป — **ไม่บล็อกใคร** |
| `minAllowedVersion`, `minAllowedVersionSetAt`, `minAllowedVersionSetBy` | **หน้า admin เท่านั้น** | เวอร์ชันขั้นต่ำ — **นี่คือตัวที่บล็อกจริง** |

> `publish` = ประกาศ · `บังคับ` = คนกดปุ่ม  
> script มี assert ห้ามเขียน `minAllowedVersion` เด็ดขาด การปล่อยบิลด์จึงล็อกใครไม่ได้โดยไม่ตั้งใจ

---

## เตรียมครั้งเดียว

### 1. Service account

มีอยู่แล้วในเครื่อง:

```
dev  : D:\Secret\LOGI-TRACK\logi-track-wrt-dev-firebase-adminsdk-fbsvc-9bc95442d2.json
prod : D:\Secret\LOGI-TRACK\logitrack-prod-firebase-adminsdk-fbsvc-13423c6233.json
```

script จะ **หยุดทันที** ถ้า SA ไม่ตรงกับ `--project` ที่ระบุ — ยิงผิดโปรเจกต์ไม่ได้

### 2. Deploy Storage rules

```powershell
cd d:\Secret\LOGI-TRACK\logitrack-platform\logitrack-web
firebase deploy --only storage --project logi-track-wrt-dev
firebase deploy --only storage --config firebase.prod.json --project logitrack-prod
```

เพิ่ม block `app_releases/**` แบบ `read: true` / `write: false` — ปิดไม่ให้คนขับทับ APK ที่ทั้งฟลีตกำลังจะติดตั้ง (ตัว script ใช้ Admin SDK ซึ่ง bypass rules อยู่แล้ว)

### 3. Deploy web (หน้า admin)

```powershell
# dev — มาเองเมื่อ push ขึ้น main แล้ว CI ผ่าน
git push origin main

# prod — manual
cd logitrack-web; pnpm deploy:prod
```

หน้าที่ได้: `/app/security-center/mobile-release` (เห็นเฉพาะ role **admin**)

---

## ปล่อยเวอร์ชันใหม่ (ทำทุกครั้ง)

### 1. Bump version

`logitrack-mobile/pubspec.yaml`

```yaml
# 2.9.4: อธิบายสั้นๆ ว่าเปลี่ยนอะไร
version: 2.9.4+1
```

### 2. Build

**รันจาก root ของ repo** (ไม่ใช่ใน `logitrack-mobile/`)

```powershell
cd d:\Secret\LOGI-TRACK\logitrack-platform
pnpm build:mobile:prod      # หรือ build:mobile:dev
```

script ทำ 2 ขั้น: copy `envs/.env.prod.mobile` → `logitrack-mobile/.env.prod` แล้วสั่ง
`flutter build apk --release --flavor prod --dart-define=FLAVOR=prod`

> **สองแฟล็กนี้ห้ามขาด** และไม่มีอันไหน derive อีกอันได้  
> `--flavor` คุม Gradle (applicationId, google-services.json) · `--dart-define` คุมฝั่ง Dart (เลือกไฟล์ `.env`, ป้าย Release/Dev, ค่า `flavor` ที่ส่งขึ้น heartbeat)  
> เดิม script ขาดทั้งคู่ → APK "prod" โหลด `.env.dev` และรายงานตัวเองเป็น dev

ผลลัพธ์:

```
logitrack-mobile\build\app\outputs\apk\prod\release\
    logitrack-prodRelease-v2.9.4.apk     ← ตัวที่ script จะหยิบ
    output-metadata.json                  ← script อ่านไฟล์นี้เพื่อ verify
```

### 3. ตรวจ build (จาก root)

```powershell
node -e "const m=require('./logitrack-mobile/build/app/outputs/apk/prod/release/output-metadata.json');console.log(m.applicationId, m.variantName, m.elements[0].versionName, JSON.stringify(m.elements[0].filters))"
```

| flavor | ต้องได้ |
|--------|---------|
| prod | `com.wrt.logitrack prodRelease 2.9.4 []` |
| dev | `com.example.logi_track_driver_app devRelease 2.9.4 []` |

`filters` ต้องเป็น `[]` และมี element เดียว — ถ้าแตกเป็นหลาย ABI script จะไม่ยอมปล่อย

เช็ค `--dart-define` อีกชั้น: เปิดแอปแล้วดู footer หน้า login ต้องขึ้น **"LOGI-TRACK Release v2.9.4"** (dev จะขึ้น "Dev")

### 4. Dry-run

```powershell
$env:GOOGLE_APPLICATION_CREDENTIALS="D:\Secret\LOGI-TRACK\logitrack-prod-firebase-adminsdk-fbsvc-13423c6233.json"
pnpm release:mobile:prod:dry-run
```

ไม่อัปโหลด ไม่เขียนอะไร — แค่ parse pubspec, หา APK, คำนวณ sha256, แล้วพิมพ์ payload ที่จะเขียนให้ดู

### 5. Apply

```powershell
pnpm release:mobile:prod:apply
```

ทำตามลำดับ: อัปขึ้น Storage → **HEAD ลิงก์เช็คว่าโหลดได้จริงและขนาดตรง** → ค่อยเขียน Firestore  
(อัปสำเร็จแต่ลิงก์พังจะไม่กลายเป็น release)

**ตอนนี้ยังไม่มีใครโดนบล็อก** — ตรวจได้ที่ Firestore: `latestVersion` ต้องเปลี่ยน แต่ `minAllowedVersion` ต้องเท่าเดิม

---

## 🔴 ก่อนกดบังคับ "ครั้งแรก" — ต้องแจก APK ด้วยมือก่อน

APK ที่คนขับใช้อยู่ ณ 28 ก.ค. 2026 **มีบั๊กปุ่มดาวน์โหลดตาย** (`canLaunchUrl` คืน false เพราะ manifest ขาด `<queries>` — แก้แล้วแต่ fix อยู่ในบิลด์ถัดไป) ถ้ากดบังคับตอนนี้ คนขับจะเจอ dialog ปิดไม่ได้ที่มีปุ่มกดแล้วไม่เกิดอะไร = **ติดตายทั้งฟลีต**

และมันเป็นวงกลม — จะผลักบิลด์ที่แก้บั๊กของระบบบังคับ ด้วยระบบบังคับที่พัง ไม่ได้

**ลำดับที่ปลอดภัยสำหรับรอบแรก:**

1. build + `release:mobile:prod:apply` ตามปกติ (ได้ลิงก์ที่โฮสต์ไว้ + เห็น badge ตกรุ่นในหน้า Mobile Clients)
2. **ไม่กดปุ่มบังคับ**
3. ส่งลิงก์ให้คนขับด้วยมือ (LINE / broadcast ในแอป) แล้วรอให้ทยอยอัปเอง
4. เฝ้าหน้า Mobile Clients จน badge "ตกรุ่น" เหลือน้อย
5. **ค่อยกดบังคับ** เพื่อไล่เก็บคนที่เหลือ — ตอนนั้นคนส่วนใหญ่อยู่บนบิลด์ที่ปุ่มใช้งานได้แล้ว คนที่ยังค้างเป็นส่วนน้อยที่ตามเก็บทีละคนได้

> เมื่อทั้งฟลีตอยู่บนบิลด์ที่มี fix แล้ว ขั้นตอนนี้ไม่จำเป็นอีก กดบังคับได้ตามปกติ

---

## บังคับอัปเดต

1. เปิด `/app/security-center/mobile-clients` → ดูว่ามีกี่เครื่องขึ้น badge **ตกรุ่น**
2. เปิด `/app/security-center/mobile-release`
   - ตรวจว่า "บิลด์ที่ปล่อยแล้ว" ขึ้นเวอร์ชันใหม่
   - **ตรวจว่า `apkDownloadUrl` ไม่ว่าง** และเปิดบนเบราว์เซอร์แล้ว **ดาวน์โหลด** (ไม่ใช่แสดงผลบนหน้าจอ)
3. กด **"บังคับคนขับทุกคนเป็น 2.9.4"**
   - ดูตัวเลข "จะบล็อก N จาก M เครื่องที่เห็นใน 7 วันล่าสุด" ว่าสมเหตุสมผล
   - ต้อง **พิมพ์เลขเวอร์ชัน** ถึงจะกด Confirm ได้

ปุ่มจะ **กดไม่ได้** เมื่อ:

| เงื่อนไข | เหตุผล |
|---------|--------|
| `apkDownloadUrl` ว่าง | แอปแสดงปุ่มดาวน์โหลดเฉพาะเมื่อมี URL — บังคับโดยไม่มีลิงก์ = คนขับติด dialog ที่ออกไม่ได้ |
| ยังไม่เคย publish | ไม่มีเวอร์ชันให้บังคับ |
| `minAllowedVersion` ≥ `latestVersion` แล้ว | ไม่มีอะไรให้ทำ |

### ผลที่เกิดกับคนขับ

เปิดแอปครั้งถัดไป (หรือกลับเข้าแอปจาก background) → dialog ปิดไม่ได้ + ปุ่มดาวน์โหลด → โหลด → ติดตั้งทับ → เปิดใหม่ → ใช้งานได้

---

## ย้อนกลับ (rollback)

`/app/security-center/mobile-release` → แก้ **เวอร์ชันขั้นต่ำที่อนุญาต** ให้ต่ำลง → Save

ปลดบล็อกคนที่ยังไม่อัปเดตได้ทันที แต่ **ย้อนการติดตั้งที่เกิดไปแล้วไม่ได้**

---

## ซ้อมบน dev ก่อนเสมอ

dev กับ prod เป็นคนละแอปคนละโปรเจกต์ ติดตั้งอยู่ข้างกันได้ ไม่กระทบคนขับจริงเลย

| | dev | prod |
|---|---|---|
| applicationId | `com.example.logi_track_driver_app` | `com.wrt.logitrack` |
| ชื่อบนเครื่อง | **LogiTrack DEV** | LogiTrack |
| Firebase project | `logi-track-wrt-dev` | `logitrack-prod` |

```powershell
# ลง APK เก่าเพื่อจำลอง "คนขับที่ยังไม่อัป"
adb install -r logitrack-mobile\build\app\outputs\flutter-apk\logitrack-devRelease-v2.7.2.apk

# build + publish ตัวใหม่
pnpm build:mobile:dev
$env:GOOGLE_APPLICATION_CREDENTIALS="D:\Secret\LOGI-TRACK\logi-track-wrt-dev-firebase-adminsdk-fbsvc-9bc95442d2.json"
pnpm release:mobile:dev:dry-run
pnpm release:mobile:dev:apply
```

**เช็คลิสต์ที่ต้องผ่านครบก่อนแตะ prod:**

- [ ] หลัง `:apply` แล้ว `minAllowedVersion` **ไม่เปลี่ยน** และเครื่อง Demo ยังใช้งานได้ปกติ
- [ ] เปิดลิงก์ `apkDownloadUrl` บนเบราว์เซอร์แล้ว **ดาวน์โหลด** ไม่ใช่ render
- [ ] หน้า Mobile Clients ขึ้น badge **ตกรุ่น** ให้เครื่อง Demo
- [ ] ลบ `apkDownloadUrl` ให้ว่าง → **ปุ่มบังคับต้อง disabled**
- [ ] กดบังคับ → เครื่อง Demo โดนบล็อก → โหลด → **ติดตั้งทับได้** ← ข้อสำคัญที่สุด (ประเด็น keystore)
- [ ] ลด `minAllowedVersion` → เครื่องที่ยังไม่อัปกลับมาใช้ได้

ถ้าข้อ "ติดตั้งทับได้" ไม่ผ่าน (ขึ้น *App not installed*) → **หยุด อย่าทำบน prod**

---

## Troubleshooting

### ตอน build

| อาการ | สาเหตุ |
|-------|--------|
| `ERR_PNPM_NO_SCRIPT` | รันผิดโฟลเดอร์ ต้องอยู่ root ของ repo |
| `ENOENT ... envs/.env.prod.mobile` | เหมือนกัน รันผิดโฟลเดอร์ |
| `flutter: not recognized` | flutter ไม่อยู่ใน PATH ของ terminal นั้น (เครื่องนี้อยู่ `D:\src\flutter\bin`) |
| `The system cannot find the file specified.` โผล่แล้ว build ต่อได้ | noise ของ `flutter.bat` บนเครื่องนี้ ไม่ใช่ error จริง |

### ตอน publish

script จะหยุดพร้อมข้อความเฉพาะของแต่ละกรณี:

| ข้อความ | ความหมาย / วิธีแก้ |
|---------|-------------------|
| `Missing --project=dev\|prod` | ใช้ `pnpm release:mobile:*` แทนการเรียก node ตรงๆ |
| `Service account belongs to "X" but --project resolves to "Y"` | ตั้ง `GOOGLE_APPLICATION_CREDENTIALS` ผิดโปรเจกต์ |
| `Built APK is version A but pubspec.yaml says B` | ลืม rebuild หลัง bump — รัน `pnpm build:mobile:*` ใหม่ |
| `Built APK has applicationId ... expected com.wrt.logitrack` | build ไม่ได้ใส่ `--flavor prod` — rebuild ด้วย script ที่ถูก |
| `No build metadata at ...` | ยังไม่ได้ build |
| `This build is split per ABI` | มี `--split-per-abi` หลุดเข้ามา — เอาออกแล้ว rebuild |
| `... already exists` | เวอร์ชันนี้ปล่อยไปแล้ว ให้ bump version ใหม่ |
| `the download URL returned 4xx` | อัปขึ้นแล้วแต่ลิงก์ไม่ทำงาน — **Firestore ยังไม่ถูกแก้** ปลอดภัย ลองใหม่ได้ |

> ⚠️ `--force` (ทับเวอร์ชันเดิม) จะ **สร้าง token ใหม่ = ลิงก์เดิมที่ประกาศไปแล้วใช้ไม่ได้**  
> ใช้ได้เฉพาะตอนที่ยังไม่บอกใครว่ามีเวอร์ชันใหม่

---

## ข้อจำกัดที่ต้องรู้

- **ไม่มี CI สำหรับ mobile** — build และ publish ต้องทำจากเครื่อง dev เอง
- **การบังคับรอบแรกยังหลบได้ด้วยการปิดเน็ต** — ตัวปิดช่องนี้ (cache floor ลง SharedPreferences) เป็นโค้ดฝั่งแอป จึงมีผลตั้งแต่ APK ตัวที่ *มี* โค้ดนี้เป็นต้นไป กล่าวคือป้องกัน**การบังคับครั้งถัดไป** ไม่ใช่ครั้งแรก
- **`versionCode` ค้างที่ 1 ตลอด** เพราะ pubspec ใช้ `+1` มาตลอด — ติดตั้งทับได้ปกติ แต่ Android เรียงลำดับเวอร์ชันไม่ได้
- **`settings/*` อ่านได้โดย user ที่ login ทุกคน** รวมคนขับ (จำเป็น เพราะแอปต้องอ่านเอง)

---

## ไฟล์ที่เกี่ยวข้อง

| ไฟล์ | หน้าที่ |
|------|---------|
| `package.json` (root) | `build:mobile:dev` / `build:mobile:prod` / `release:mobile:*` |
| `logitrack-web/scripts/publish-mobile-release.mjs` | อัป APK + เขียน Firestore |
| `logitrack-web/app/app/security-center/mobile-release/page.tsx` | หน้าคุมเวอร์ชัน + ปุ่มบังคับ |
| `logitrack-web/app/app/security-center/mobile-clients/page.tsx` | ดูว่าเครื่องไหนตกรุ่น |
| `logitrack-web/lib/mobileVersion.ts` | เทียบ semver ฝั่ง web |
| `logitrack-web/storage.rules` | สิทธิ์ `app_releases/**` |
| `lib/core/services/mobile_app_version_service.dart` | ตัวเช็คเวอร์ชัน + dialog บังคับ + cache |
| `shared-docs/adr/0007-mobile-forced-update-pipeline.md` | เหตุผลเบื้องหลังทุกการตัดสินใจ |

---

> **หมายเหตุ:** โฟลเดอร์ `**/docs/` ถูก gitignore ไฟล์นี้จะไม่เข้า repo จนกว่าจะ `git add -f logitrack-mobile/docs/RELEASE_AND_FORCE_UPDATE.md`
