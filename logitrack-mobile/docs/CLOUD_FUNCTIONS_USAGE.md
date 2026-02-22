# การเรียก Firebase Cloud Functions จากแอป Flutter (Dart)

## Cloud Functions ไม่ได้ติดตั้งบน Device

- **Cloud Functions รันบนเซิร์ฟเวอร์ของ Google (Firebase/Google Cloud)** ไม่ได้ถูก deploy หรือ "ติดตั้ง" ลงในอุปกรณ์
- แอป Flutter แค่ **ส่ง request ผ่านอินเทอร์เน็ต** ไปที่ URL ของ function แล้วรับ response กลับมา (เหมือนเรียก API)
- การ "deploy" ทำที่เครื่อง dev/server ครั้งเดียวด้วย `firebase deploy --only functions` จากโปรเจกต์ `logitrack-web/functions` — หลังจากนั้นทุก device ที่มีแอปจะเรียก function ตัวเดียวกันบน cloud ได้เลย

## การทำงานแบบย่อ

```
[Device / แอป Flutter]  --(อินเทอร์เน็ต)-->  [Firebase → Cloud Function รันบน Google]
                                                    |
[Device ได้รับผลลัพธ์]  <--(response JSON)--       [return result]
```

- แอปใช้ package `cloud_functions` เรียก `FirebaseFunctions.instance.httpsCallable('ชื่อฟังก์ชัน')`
- SDK จะส่ง HTTP request (พร้อม auth token ถ้า user login แล้ว) ไปที่ Firebase
- Firebase ไปรัน function ที่ deploy ไว้ แล้วส่งผลกลับมาให้แอป

## วิธีใช้ในโค้ด Dart

### 1. กำหนด region ให้ตรงกับที่ deploy

Functions ของโปรเจกต์ deploy ที่ region **asia-southeast1** ดังนั้นต้องใช้ `instanceFor(region:)`:

```dart
import 'package:cloud_functions/cloud_functions.dart';

// ใช้ instance ของ region asia-southeast1 (เดียวกับที่ deploy ไว้)
final functions = FirebaseFunctions.instanceFor(region: 'asia-southeast1');
```

### 2. เรียก Callable Function (ไม่ส่งพารามิเตอร์)

```dart
final callable = functions.httpsCallable('ชื่อฟังก์ชัน');
final result = await callable.call();
print(result.data); // ข้อมูลที่ function return
```

### 3. เรียก Callable Function (ส่งพารามิเตอร์)

```dart
final callable = functions.httpsCallable<Map<String, dynamic>>('notifyFirstMileTaskUpdate');
final result = await callable.call({
  'taskId': 'FM-20260222-SOCN-001',
  'newDriverId': 'driver_uid_xxx',
  'status': 'Assigned',
});
```

### 4. จัดการ error

```dart
try {
  final callable = functions.httpsCallable('setAdminClaims');
  final result = await callable.call();
  // success
} on FirebaseFunctionsException catch (e) {
  // e.code เช่น 'unauthenticated', 'permission-denied', 'internal'
  // e.message ข้อความจาก function
}
```

## Service สำหรับเรียก Functions (ตัวอย่าง)

ในโปรเจกต์มี `lib/core/services/cloud_functions_service.dart` เป็นจุดกลางสำหรับเรียก callable functions ที่ region `asia-southeast1` — ใช้ผ่าน service นี้แทนการสร้าง `FirebaseFunctions.instanceFor(...)` ซ้ำหลายที่

## สรุป

| คำถาม | คำตอบ |
|--------|--------|
| Function ติดตั้งบน device ไหม? | **ไม่** — รันบน Google Cloud เท่านั้น |
| แอปทำอะไร? | เรียกผ่าน SDK = ส่ง HTTP request ไปที่ Firebase แล้วรับ response |
| ต้อง deploy function ที่ device ไหม? | **ไม่** — deploy ครั้งเดียวจากเครื่อง dev ด้วย `firebase deploy --only functions` |
| หลาย device เรียก function เดียวกันได้ไหม? | **ได้** — ทุก device เรียก URL เดียวกันบน cloud |
