# logi_track_driver_app

A new Flutter project.

## ทดสอบบน Browser (เมื่อไม่มีอุปกรณ์หรือ Android Studio)

รันแอปบน Chrome ได้เลย ไม่ต้องมีเครื่อง Android/iOS หรือ Android Studio สำหรับรันเครื่องจำลอง:

### วิธีที่ 1: รันด้วย Chrome (แนะนำ)

```bash
cd logitrack-mobile
flutter pub get
flutter run -d chrome
```

ถ้ามีหลาย device ให้เลือกหมายเลขของ **Chrome** เมื่อ Flutter ถาม

### วิธีที่ 2: รัน Web Server แล้วเปิดใน Browser เอง

```bash
cd logitrack-mobile
flutter run -d web-server --web-port=8080
```

จากนั้นเปิดเบราว์เซอร์ไปที่ **http://localhost:8080**

### สิ่งที่ทำงานบน Web ได้

- Login, หน้า Home, เมนู, โปรไฟล์
- เปลี่ยนภาษา (EN/TH), ธีม
- หน้า Loading Phase: เลือกประเภทงาน, กรอก Trip ID, Origin/Destination, **อัปโหลดรูปจากไฟล์** (แทนกล้อง)
- OCR บน Web จะไม่รัน (มีข้อความแจ้ง); กล้อง/สแกน QR ใช้ได้บน Android/iOS เท่านั้น

### ข้อจำกัดบน Web

- **ตำแหน่ง (GPS)**: บน Browser จะขอ Location จาก browser; ถ้าไม่ให้สิทธิ์ แอปจะแสดงข้อความเกี่ยวกับตำแหน่ง
- **กล้อง / สแกน QR**: ไม่รองรับบน Web — ใช้ปุ่ม "From image" หรืออัปโหลดรูปแทน
- **การแจ้งเตือน (Push)**: อาจทำงานต่างจากบนมือถือ

### ตรวจสอบว่า Flutter รองรับ Web

```bash
flutter devices
```

ควรเห็นรายการที่มี **Chrome** หรือ **Web server** อยู่

ถ้าไม่มี ให้เปิดใช้ Web support:

```bash
flutter config --enable-web
```

---

## รันแบบเครื่องจำลอง Android (เหมือนมือถือ)

ติดตั้ง **Android Emulator** แล้วรันแอปได้เหมือนบนมือถือ: กล้อง (ใช้เว็บแคม), GPS จำลอง, สแกน QR, OCR ฯลฯ

### ขั้นตอนที่ 1: ติดตั้ง Android Studio

1. ดาวน์โหลด: [developer.android.com/studio](https://developer.android.com/studio)
2. ติดตั้ง แล้วเปิด Android Studio
3. ไปที่ **More Actions** → **SDK Manager** (หรือ **Tools** → **SDK Manager**)
   - แท็บ **SDK Platforms**: เลือก **Android 14** (หรือ 13) แล้วติ๊ก **Show Package Details** แล้วเลือก **Google APIs** / **Google Play** image
   - แท็บ **SDK Tools**: ให้ติ๊ก **Android SDK Build-Tools**, **Android Emulator**, **Android SDK Platform-Tools**
4. กด **Apply** / **OK** ให้ติดตั้งครบ

### ขั้นตอนที่ 2: สร้างเครื่องจำลอง (AVD)

1. ใน Android Studio: **More Actions** → **Virtual Device Manager** (หรือ **Tools** → **Device Manager**)
2. กด **Create Device**
3. เลือกมือถือ เช่น **Pixel 6** หรือ **Pixel 7** → **Next**
4. เลือก System Image: **API 34** (Android 14) หรือ **API 33** — ถ้ายังไม่มีให้กด **Download** ข้างๆ แล้วรอโหลดเสร็จ → **Next**
5. ตั้งชื่อ AVD (เช่น `Pixel_6_API_34`) → **Finish**

### ขั้นตอนที่ 3: เปิด Emulator แล้วรัน Flutter

**วิธีที่ 1:** เปิดเครื่องจำลองก่อน แล้วค่อยรันแอป

1. ใน **Device Manager** กดปุ่ม **Play (▶)** ข้างเครื่องที่สร้างไว้
2. รอจนเครื่อง Android เปิดขึ้นมาบนหน้าจอ
3. ในเทอร์มินัลที่โฟลเดอร์ `logitrack-mobile`:

```bash
flutter devices
flutter run
```

ถ้ามีหลาย device (เช่น Chrome + Emulator) Flutter จะถามให้เลือก — เลือกตัวที่ชื่อ **android** / **emulator**

**วิธีที่ 2:** ให้ Flutter เปิด emulator ให้ (ถ้ามี AVD เดียว)

```bash
flutter emulators
flutter run -d <id ของ emulator>
```

เช่น ถ้า `flutter emulators` แสดง `Pixel_6_API_34`:

```bash
flutter run -d Pixel_6_API_34
```

### ใช้กล้อง / GPS บน Emulator

- **กล้อง**: ในเมนู emulator (⋮) → **Camera** → ตั้งค่า Front/Back เป็น **Webcam** หรือ **Virtual scene** จะได้ถ่ายรูปในแอปได้
- **ตำแหน่ง (GPS)**: เมนู emulator → **Location** → ใส่ latitude/longitude (เช่น 13.7563, 100.5018 สำหรับกรุงเทพ) แล้วกด **Send** แอปจะได้ตำแหน่งจำลอง

### ตรวจสอบว่า Flutter เห็น Android

```bash
flutter doctor
```

ต้องมีเครื่องหมายถูกที่ **Android toolchain** และ **Android Studio** (หรือ Android command-line tools) ถ้ายังไม่ติดตั้งครบ `flutter doctor` จะบอกขั้นตอนเพิ่ม

---

## Getting Started

This project is a starting point for a Flutter application.

A few resources to get you started if this is your first Flutter project:

- [Lab: Write your first Flutter app](https://docs.flutter.dev/get-started/codelab)
- [Cookbook: Useful Flutter samples](https://docs.flutter.dev/cookbook)

For help getting started with Flutter development, view the
[online documentation](https://docs.flutter.dev/), which offers tutorials,
samples, guidance on mobile development, and a full API reference.
