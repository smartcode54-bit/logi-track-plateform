# แผนใช้ Google Maps หาระยะทาง Hub → SOC และเก็บข้อมูล

## วัตถุประสงค์
- หาระยะทางและเวลาเดินทาง (driving) จากแต่ละ **Hub** ไปแต่ละ **SOC** ด้วย Google Maps
- เก็บผลลัพธ์ใน Firestore เพื่อใช้ในระบบ

**เป้าหมายการนำข้อมูลระยะทางไปใช้**
- **ประมาณการค่าใช้จ่าย** (เช่น ค่าน้ำมัน/เที่ยว จากระยะทาง)
- **ประมาณการค่าซ่อมบำรุง** (ใช้ระยะทางรวมช่วยประเมินรอบบำรุง)
- **การจัดการยางในอนาคต** (ใช้ระยะทาง/กิโลเมตรรวมสำหรับติดตามอายุและเปลี่ยนยาง)
- ใช้ประกอบ First Mile, รายงาน และการจัดเส้นทางได้ตามต้องการ

---

## 1. เลือก Google API ที่เหมาะสม

| API | ใช้เมื่อไหร่ | ข้อดี | ข้อจำกัด |
|-----|----------------|-------|----------|
| **Distance Matrix API** | ต้องการระยะทาง/เวลา หลายคู่ (Hub×SOC) ในครั้งเดียว | 1 request ได้หลาย origin×destination (สูงสุด 25×25 หรือ 100 elements ต่อ request) | ไม่ได้เส้นทางละเอียด |
| **Directions API** | ต้องการเส้นทางละเอียด (polyline, steps) ต่อ 1 คู่ | ได้ระยะทาง + เวลา + พิกัดเส้นทาง | 1 request ต่อ 1 คู่ A→B |

**แนะนำ:** ใช้ **Distance Matrix API** สำหรับคำนวณระยะทาง/เวลา Hub→SOC ทุกคู่ แล้วเก็บลง Firestore ถ้าอนาคตต้องการเส้นทางบนแผนที่ค่อยใช้ Directions API เพิ่มเฉพาะคู่ที่เลือก

---

## 2. โครงสร้างข้อมูลที่เก็บ (Firestore)

### ตัวเลือก A: Collection แยก `hub_soc_distances`

```
hub_soc_distances (collection)
├── {hubId}_{socId} (document ID สำหรับ query ง่าย)
│   ├── hubId: string          // หรือ source_id ของ Hub
│   ├── socId: string           // หรือ source_id ของ SOC (SOCE, SOCN, SOCW)
│   ├── distanceMeters: number
│   ├── distanceKm: number
│   ├── durationSeconds: number
│   ├── durationMinutes: number
│   ├── hubLat: number
│   ├── hubLng: number
│   ├── socLat: number
│   ├── socLng: number
│   ├── updatedAt: Timestamp   // ใช้ตัดว่าเมื่อไหร่ควรคำนวณใหม่
│   └── (optional) rawResponse  // เก็บ response จาก API ถ้าต้องการ debug
```

### ตัวเลือก B: เก็บใน subcollection ใต้ Hub

```
hubs (collection)
└── {hubDocId}
    └── distances_to_soc (subcollection)
        └── {socId}
            ├── distanceMeters, durationSeconds, ...
```

**แนะนำ:** ใช้ตัวเลือก A — query ง่าย (เช่น "ระยะทางจาก Hub นี้ไปทุก SOC", "ระยะทางจากทุก Hub ไป SOC นี้") และอัปเดตเป็นชุดได้สะดวก

---

## 3. ขั้นตอนการทำงาน (Flow)

1. **ดึงรายการ Hub และ SOC**
   - อ่านจาก collection `hubs` กรอง `station_type === "HUB"` และ `station_type === "SOC"`
   - ใช้เฉพาะจุดที่มี `latitude` และ `longitude`

2. **จับคู่ Hub–SOC และเรียก Google API**
   - สร้างคู่ (origin = Hub, destination = SOC) ทุกคู่
   - ใช้ **Distance Matrix API**: ส่ง origins (lat,lng ของทุก Hub) และ destinations (lat,lng ของทุก SOC) เป็นชุด (แบ่ง batch ตาม limit 25×25 หรือ 100 elements)
   - ได้ค่า `distance.value` (เมตร), `duration.value` (วินาที) ต่อคู่

3. **บันทึกลง Firestore**
   - เขียน/อัปเดต documents ใน `hub_soc_distances` ตามโครงสร้างในข้อ 2
   - ใส่ `updatedAt` เพื่อใช้ตัดว่าคำนวณเมื่อไหร่

4. **เมื่อไหร่ควรคำนวณ**
   - **เป้าหมายการรัน:** ไม่เกิน **4 ครั้ง/เดือน** เพื่อประหยัดค่า API — เพราะ Hub ไม่ได้เปลี่ยนหรือเพิ่มบ่อย
   - **On-demand:** ปุ่มใน Admin (หน้า Sources หรือ First Mile) "คำนวณระยะทาง Hub–SOC" ให้ผู้ดูแลกดเมื่อต้องการอัปเดต
   - (ถ้าต้องการ) แจ้งเตือนหรือล็อกไม่ให้กดบ่อยเกิน 4 ครั้ง/เดือน

---

## 4. สิ่งที่ต้องเตรียมในโปรเจกต์

### 4.1 Google Cloud
- สร้างโปรเจกต์หรือใช้โปรเจกต์เดิม
- เปิด **Distance Matrix API** (และ Directions API ถ้าจะใช้เส้นทางด้วย)
- สร้าง API Key และจำกัดการใช้งาน (เช่น จำกัด IP หรือ HTTP referrer) เพื่อความปลอดภัย
- เก็บ API Key ใน environment (ไม่ commit ลง git)

### 4.2 Environment variables
- **ฝั่ง Server (แนะนำ):** เรียก Google API จาก API Route หรือ Cloud Function เพื่อไม่ให้ API Key ไปอยู่ที่ client  
  - ตัวอย่าง: `GOOGLE_MAPS_API_KEY` ใน `.env.local` (Next.js) หรือใน Firebase Functions config

### 4.3 โค้ดที่ต้องเขียน
- **Schema/Type:** กำหนด type หรือ Zod schema สำหรับ document ใน `hub_soc_distances`
- **ฟังก์ชันเรียก Distance Matrix API:** รับ list ของ (lat,lng) origins และ destinations → เรียก API → แปลงเป็นระยะทาง/เวลาต่อคู่
- **ฟังก์ชันบันทึก Firestore:** รับผลจาก API แล้วเขียน/merge ลง `hub_soc_distances`
- **API Route (Next.js):** เช่น `POST /api/admin/distances/hub-soc` รับคำสั่ง "คำนวณใหม่" แล้วเรียกฟังก์ชันด้านบน (ใช้ API Key แค่ฝั่ง server)
- **UI:** ปุ่ม "คำนวณระยะทาง Hub–SOC" ในหน้า Admin (Sources หรือ First Mile) เรียก API Route ข้างต้น และแสดงสถานะ (กำลังคำนวณ / สำเร็จ / error)

---

## 5. ค่าใช้จ่าย (Google Maps Platform)

การคิดเงินใช้แบบ **ต่อ element** = จำนวน origins × จำนวน destinations ต่อ request  
(ไม่ใช่ต่อ request ทั้งก้อน)

### Distance Matrix API (Legacy – แบบพื้นฐาน)

| รายการ | ค่า |
|--------|-----|
| **ฟรีต่อเดือน** | 10,000 elements แรก |
| **10,001 – 100,000** | $5.00 ต่อ 1,000 elements |
| **100,001 ขึ้นไป** | $4.00 ต่อ 1,000 elements |

*อ้างอิง: [Google Maps Platform Pricing](https://developers.google.com/maps/billing-and-pricing/pricing#routes-legacy-pricing) (Routes APIs Legacy)### เคสจริง: 167 Hub × 3 SOC

- **Elements ต่อ 1 รันเต็ม:** 167 × 3 = **501 elements**
- **การแบ่ง batch (limit 25 origins/request, สูงสุด 100 elements/request):**  
  - 25 Hub × 3 SOC = 75 elements ต่อ request → ต้อง **7 requests** ต่อ 1 รัน (ceil(167/25) = 7)

| ความถี่รัน | Elements/เดือน | ค่าใช้จ่ายโดยประมาณ (USD) |
|------------|----------------|----------------------------|
| **1 ครั้ง (on-demand)** | 501 | **ฟรี** (ไม่เกิน 10,000) |
| **ไม่เกิน 4 ครั้ง/เดือน** (เป้าหมายของเรา) | 2,004 | **ฟรี** |
| **~20 ครั้ง/เดือน** | 10,020 | 20 elements เสีย ≈ **$0.10** |
| **วันละ 1 ครั้ง** (30 ครั้ง) | 15,030 | 5,030 billable ≈ **$25.15** |
| **50 ครั้ง/เดือน** | 25,050 | 15,050 billable ≈ **$75.25** |

สรุปสำหรับ **167 × 3** (และเป้าหมายรัน **ไม่เกิน 4 ครั้ง/เดือน**):
- **4 ครั้ง/เดือน** = 2,004 elements → อยู่ใน 10,000 ฟรี = **ไม่เสียเงิน**
- เก็บผลใน Firestore ใช้สำหรับประมาณการค่าใช้จ่าย ค่าซ่อมบำรุง และการจัดการยางในอนาคต โดยไม่ต้องรัน API บ่อย

### Distance Matrix Advanced (แบบมี traffic / โหมดพิเศษ)

- ฟรี 5,000 elements แรก แล้ว **$10 ต่อ 1,000** (จากนั้น $8 ต่อ 1,000)  
- ใช้เมื่อต้องการข้อมูลจราจรหรือโหมดพิเศษ ถ้าใช้แค่ driving ระยะทาง/เวลา แบบพื้นฐานใช้แบบ Legacy ก็พอ

### สรุปและข้อควรทำ

- เปิด Billing ใน Google Cloud (ถึงจะได้โควตาฟรี 10k) และตั้ง **quota / budget alert** ใน Console
- สำหรับ 501 elements/รัน: จำกัดความถี่รันหรือใช้แค่ on-demand จะช่วยควบคุมค่าใช้จ่าย

---

## 6. ข้อควรระวัง

- **Quota และค่าใช้จ่าย:** การคิดเงินเป็นต่อ **element** (origin×destination) ไม่ใช่ต่อ request ควรตรวจ quota และตั้ง budget alert ใน Google Cloud Console
- **Rate limit:** ถ้ามี Hub/SOC จำนวนมาก ควรแบ่ง batch และใส่ delay เล็กน้อยระหว่าง request เพื่อไม่ให้เกิน rate limit
- **การแมป SOC:** ปัจจุบัน First Mile ใช้ `SOC_KEYS = ["SOCE", "SOCN", "SOCW"]` — ต้องแมปว่า SOC ใน collection `hubs` ใช้ `source_id` เป็นอะไร (เช่น SOCE, SOC-E, SOCN) ให้ตรงกับคีย์นี้เมื่อเก็บ `socId`

---

## 7. ลำดับการ implement (สรุป)

1. สร้าง API Key และเปิด Distance Matrix API ใน Google Cloud
2. เพิ่ม env เช่น `GOOGLE_MAPS_API_KEY` (ฝั่ง server เท่านั้น)
3. สร้าง collection/structure ใน Firestore สำหรับ `hub_soc_distances` (หรือใช้ได้เลยโดยไม่ต้องสร้าง collection ล่วงหน้า)
4. เขียนฟังก์ชันเรียก Distance Matrix API + เขียนผลลง Firestore
5. สร้าง API Route (เช่น POST `/api/admin/distances/hub-soc`) ที่ดึง Hub/SOC จาก Firestore → เรียกฟังก์ชันคำนวณ → return สถานะ
6. เพิ่มปุ่มและ UI ใน Admin เพื่อ trigger การคำนวณและแสดงผล
7. (ถ้าต้องการ) นำข้อมูลจาก `hub_soc_distances` ไปใช้ในหน้า First Mile / รายงาน (เช่น แสดงระยะทางหรือเวลาโดยประมาณในตารางงาน)

ถ้าต้องการให้ช่วยลงมือเขียนโค้ด (เช่น API Route + ฟังก์ชัน Distance Matrix + ปุ่มในหน้า Sources) บอกได้ว่าจะเริ่มจากส่วนไหนก่อน (backend ก่อน หรือ UI ก่อน)
