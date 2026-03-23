"use strict";
/**
 * 🛠️ Core Maintenance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับรันตรรกะ Odometer ทริกเกอร์ระบบแจ้งเตือน Smart PM
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.isMaintenanceDue = isMaintenanceDue;
/**
 * ตรวจสอบว่าครบรอบการแจ้งเตือนเช็คระยะ (Threshold <= 2,000 กม.) หรือไม่
 * @param mileage เลขไมล์ปัจจุบัน (จากฟอร์มเติมน้ำมัน)
 * @param nextServiceMileage เลขไมล์รอบเช็คระยะถัดไป
 */
function isMaintenanceDue(mileage, nextServiceMileage) {
    const distanceLeft = nextServiceMileage - mileage;
    // ตรวจสอบระยะห่าง <= 2,000 กม. และ ยังไม่เลยกำหนดขับขี่ ( distanceLeft >= 0 )
    return distanceLeft <= 2000 && distanceLeft >= 0;
}
//# sourceMappingURL=maintenance.js.map