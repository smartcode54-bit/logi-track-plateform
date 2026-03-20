# Run LogiTrack Mobile on a Physical Device

## Prerequisites

- Flutter SDK installed
- Android device with **USB debugging** enabled
- USB cable to connect device to your computer

---

## Step 1: Enable USB debugging on your phone

1. Open **Settings** → **About phone**
2. Tap **Build number** 7 times until you see "You are now a developer"
3. Go back to **Settings** → **Developer options**
4. Turn on **USB debugging**
5. (Optional) Turn on **Install via USB** if you use some Xiaomi/OPPO devices

---

## Step 2: Connect the device

1. Connect your phone to the PC with a USB cable
2. On the phone, when prompted **"Allow USB debugging?"** → tap **Allow** (optionally check "Always allow from this computer")
3. Unlock the phone and leave the screen on if it asks for authorization

---

## Step 3: Check that Flutter sees the device

Open a terminal in the project and run:

```bash
cd logitrack-mobile
flutter devices
```

You should see your phone listed, for example:

```
25078PC3EG (mobile) • RG79MFIJZLY9MFTS • android-arm64 • Android 15 (API 35)
```

If the device does **not** appear:

- Try another USB cable (some are charge-only)
- Try another USB port
- Re-enable USB debugging and reconnect
- On Windows: ensure [USB drivers](https://developer.android.com/studio/run/oem-usb) for your phone are installed (often automatic for popular brands)

---

## Step 4: Run the app on the physical device

**Option A – Let Flutter pick the device (if only one is connected):**

```bash
cd logitrack-mobile
flutter run
```

**Option B – Specify the device by ID:**

```bash
cd logitrack-mobile
flutter run -d <device_id>
```

Use the **device ID** from `flutter devices` (e.g. `RG79MFIJZLY9MFTS`):

```bash
flutter run -d RG79MFIJZLY9MFTS
```

**Option C – Release build (smaller, faster on device):**

```bash
flutter run --release
```

---

## Step 5: While the app is running

- **r** – Hot reload  
- **R** – Hot restart  
- **q** – Quit and stop the app on the device  

---

## Troubleshooting

| Problem | What to try |
|--------|---------------------|
| Device not in `flutter devices` | Reconnect USB, allow debugging on phone, try another cable/port |
| "No devices found" | Install/update USB driver for your phone (Windows) |
| Build fails (Android) | Run `flutter doctor` and fix any Android toolchain / license issues |
| App installs but crashes | Check log with `flutter run` and read the stack trace in the terminal |
| **Google Sign-In: "Developer console is not set up" (28444)** | See [Google Sign-In setup](#google-sign-in-setup) below |
| **Overlay บนรูปมีแค่ Lat/Lng ไม่มีที่อยู่ (prod)** | ใส่ `GOOGLE_MAPS_API_KEY` ใน `.env.prod` และเปิด Geocoding API — ดู [Geocoding บนรูปถ่าย](#geocoding-บนรูปถ่าย-prod-ไม่แสดงที่อยู่รหัสไปรษณีย์) ด้านล่าง |

---

### Google Sign-In setup

ถ้าลงแอปแล้วกด "Sign in with Google" แล้วขึ้นข้อความ **"Developer console is not set up"** หรือ error 28444 ให้ทำตามขั้นตอนนี้ (ต้องทำครั้งเดียวใน Firebase/Google Cloud):

#### 1. เปิด Google Sign-In ใน Firebase

1. ไปที่ [Firebase Console](https://console.firebase.google.com/) → เลือกโปรเจกต์ (prod หรือ dev ตาม flavor ที่ใช้)
2. ไปที่ **Authentication** → **Sign-in method**
3. เลือก **Google** → เปิด **Enable** → ตั้ง Support email → **Save**

#### 2. เพิ่ม SHA-1 และ SHA-256 ให้แอป Android

แอปที่ลงบนเครื่องจริงต้องใช้ SHA ของ keystore ที่ใช้เซ็น APK นั้น:

- **ถ้าลงจาก `flutter run` (debug)** ใช้ **debug keystore**
- **ถ้าลงจาก APK ที่ build เอง (release)** ใช้ **release keystore** ที่ใช้เซ็น

**ดูค่า SHA-1 / SHA-256 จาก Gradle (แนะนำ):**

รันจากโฟลเดอร์ **logitrack-mobile** (ไม่ต้อง cd ซ้ำเข้า logitrack-mobile อีก):

```bash
cd android
.\gradlew signingReport
```

หรือถ้าอยู่ที่ root ของ repo:

```bash
cd logitrack-mobile\android
.\gradlew signingReport
```

จากผลลัพธ์ให้ copy ค่า **SHA-1** และ **SHA-256** ของ variant ที่ใช้ (เช่น `prodRelease` หรือ `prodDebug`)

**หรือใช้ keytool (debug):**

```bash
keytool -list -v -alias androiddebugkey -keystore %USERPROFILE%\.android\debug.keystore -storepass android
```

(Windows: ใช้ `%USERPROFILE%` ได้ใน CMD)

**นำ SHA ไปใส่ใน Firebase:**

1. Firebase Console → โปรเจกต์ → **Project settings** (ไอคอนเฟือง)
2. ลงมาที่ **Your apps** → เลือกแอป Android (package name `com.wrt.logitrack` สำหรับ prod)
3. กด **Add fingerprint** → วาง **SHA-1** แล้ว Save
4. กด **Add fingerprint** อีกครั้ง → วาง **SHA-256** แล้ว Save

ถ้ามีแอป Android หลายตัว (เช่น dev/prod) ให้เพิ่ม SHA ในแอปที่ตรงกับ flavor ที่ใช้ล็อกอิน

#### 3. ตรวจสอบ Web Client ID ใน .env

แอปใช้ **Web client ID** จาก env สำหรับ Google Sign-In (ตัวที่ Firebase สร้างให้เมื่อเปิด Google provider):

1. ใน Firebase → **Project settings** → **Your apps** → แอป Android
2. หรือไปที่ [Google Cloud Console](https://console.cloud.google.com/) → โปรเจกต์เดียวกัน → **APIs & Services** → **Credentials**
3. หา OAuth 2.0 **Web client** (ไม่ใช่ Android client) → copy **Client ID**

ในโปรเจกต์ต้องมีในไฟล์ env ตาม flavor ที่รัน:

- รัน **prod** → ใช้ `.env.prod` และต้องมี `FIREBASE_WEB_CLIENT_ID=...` (ค่า Web client ID ของโปรเจกต์ prod)
- รัน **dev** → ใช้ `.env.dev` และต้องมี `FIREBASE_WEB_CLIENT_ID=...` (ค่า Web client ID ของโปรเจกต์ dev)

ถ้าไม่มีหรือผิด แก้แล้ว build/รันแอปใหม่

#### 4. สรุปสั้น ๆ

| สาเหตุที่มักเจอ | วิธีแก้ |
|------------------|--------|
| ยังไม่ได้เปิด Google ใน Firebase Authentication | เปิดที่ Sign-in method → Google → Enable |
| ยังไม่ได้เพิ่ม SHA-1/SHA-256 ของ keystore ที่ใช้เซ็นแอป | เพิ่มใน Firebase → Project settings → Your apps → Android app → Add fingerprint |
| ใช้แอปที่เซ็นด้วย debug แต่เพิ่มแต่ SHA ของ release (หรือตรงกันข้าม) | ใช้ SHA ของ variant เดียวกับที่ลงอยู่ (ดูจาก `signingReport`) |
| ไม่มีหรือใส่ `FIREBASE_WEB_CLIENT_ID` ผิดใน .env | ใส่ Web client ID จาก Firebase/Google Cloud ใน `.env.prod` หรือ `.env.dev` แล้ว build ใหม่ |

หลังแก้แล้วลองล็อกอาออกแล้วกด "Sign in with Google" ใหม่ (ถ้าจำเป็นถอนแอปแล้วลงใหม่)

#### 5. ถ้ายังติด 28444 หลังเพิ่ม SHA แล้ว

ทำตามทีละข้อ:

**ก. ตรวจ OAuth consent screen (Google Cloud)**

1. ไปที่ [Google Cloud Console](https://console.cloud.google.com/) → เลือก**โปรเจกต์เดียวกับ Firebase**
2. **APIs & Services** → **OAuth consent screen**
3. ต้องมี App name, User support email, Developer contact ครบ
4. ถ้าเป็น "Testing" ต้องเพิ่มอีเมลที่ใช้ทดสอบใน **Test users**

**ข. สร้าง/ตรวจ Android OAuth 2.0 Client**

Firebase อาจสร้างให้อัตโนมัติ แต่ถ้าไม่มี แอปจะขึ้น 28444:

1. Google Cloud Console → โปรเจกต์เดียวกับ Firebase → **APIs & Services** → **Credentials**
2. ดูรายการ **OAuth 2.0 Client IDs** ว่ามีประเภท **Android** ที่ package name = `com.wrt.logitrack` หรือไม่
3. **ถ้าไม่มี** → กด **+ CREATE CREDENTIALS** → **OAuth client ID** → Application type เลือก **Android**
   - Name: เช่น `LogiTrack Android`
   - Package name: `com.wrt.logitrack`
   - SHA-1: ใส่ค่า SHA-1 ที่เพิ่มใน Firebase (เช่น `C7:A7:A4:ED:85:0B:A3:84:66:96:F5:4A:0A:32:D0:E6:10:1A:FC:3D`)
   - กด **Create**
4. รอสัก 1–2 นาที แล้วลอง Sign in with Google อีกครั้ง

**ค. ตรวจว่าแอปได้ Web Client ID จริง (ต้อง build จาก .env ที่ถูก)**

- แอปอ่าน `FIREBASE_WEB_CLIENT_ID` จากไฟล์ `.env.prod` (prod) หรือ `.env.dev` (dev) ตอน** build** เท่านั้น
- ถ้าเคย build ตอนที่ยังไม่มีหรือใส่ผิด แอปที่ลงอยู่จะไม่มีค่าใช้ → ต้อง** build ใหม่** แล้วลงแอปใหม่
- ตรวจใน `.env.prod` ว่ามีบรรทัด `FIREBASE_WEB_CLIENT_ID=...` และค่าตรงกับ Web client ID จาก Firebase/Google Cloud (Credentials → OAuth 2.0 Client IDs → ประเภท **Web application**)

**ง. ใช้แอปที่ build หลังแก้ .env**

ถ้าแก้ `.env.prod` หรือ `.env.dev` ต้อง build แล้วลงใหม่:

```bash
cd logitrack-mobile
flutter build apk --flavor prod --dart-define=FLAVOR=prod
```

แล้วลง APK จาก `build/app/outputs/flutter-apk/` แล้วลอง Sign in with Google อีกครั้ง

---

## Geocoding บนรูปถ่าย (Prod ไม่แสดงที่อยู่/รหัสไปรษณีย์)

ถ้าตอนถ่ายรูป overlay แสดง Lat/Lng ได้แต่**ไม่มีที่อยู่หรือรหัสไปรษณีย์** (dev แสดงครบ prod ไม่แสดง):

1. **แอปใช้ fallback Google Geocoding API** เมื่อ native geocoder ไม่ทำงาน — ต้องมี `GOOGLE_MAPS_API_KEY` ใน `.env.prod`
2. **เปิด Geocoding API** ใน Google Cloud Console:
   - โปรเจกต์เดียวกับ Firebase Prod → **APIs & Services** → **Library**
   - ค้นหา **Geocoding API** → **Enable**
3. ตรวจว่า API key ใน `.env.prod` ไม่ถูก restrict เฉพาะ API อื่น (หรือเพิ่ม Geocoding API ใน restriction)

---

## First-time Android licenses (if needed)

If `flutter doctor` reports missing Android licenses:

```bash
flutter doctor --android-licenses
```

Accept the licenses when prompted.

# build apk for physical device

ต้องใส่ `--dart-define=FLAVOR=...` ด้วย เพื่อให้แอปโหลด `.env.prod` หรือ `.env.dev` ถูกต้อง (รวมถึง FIREBASE_WEB_CLIENT_ID สำหรับ Google Sign-In)

```bash - prod
flutter build apk --flavor prod --dart-define=FLAVOR=prod
# หรือ split ตาม ABI:
flutter build apk --flavor prod --dart-define=FLAVOR=prod --split-per-abi
```

```bash - dev
flutter build apk --flavor dev --dart-define=FLAVOR=dev
# หรือ split ตาม ABI:
flutter build apk --flavor dev --dart-define=FLAVOR=dev --split-per-abi
```

### `flutter run` (debug)

```bash
# Dev → ใช้ Firebase Dev + Web Client ID ของ Dev
flutter run --flavor dev --dart-define=FLAVOR=dev
```
```bash
# Prod → ใช้ Firebase Prod + Web Client ID ของ Prod
flutter run --flavor prod --dart-define=FLAVOR=prod


flutter build apk --flavor prod --release

---

## App Check และ Cloud Functions

ถ้าเรียกฟังก์ชันผ่าน `httpsCallable` แล้วได้ **`firebase_functions/unauthenticated`** ให้ตั้งค่า App Check ตาม [APP_CHECK.md](./APP_CHECK.md) (สำคัญมากเมื่อรัน `flutter run` แบบ debug กับ flavor **prod** — ต้องลงทะเบียน debug token ใน **Firebase project ของ prod**)

รายการ flow ทั้งหมดของแอปคนขับกับ Firebase อยู่ที่ [DRIVER_APP_FIREBASE_AUDIT.md](./DRIVER_APP_FIREBASE_AUDIT.md)
