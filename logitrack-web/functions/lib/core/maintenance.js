"use strict";
/**
 * 🛠️ Core Maintenance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับรันตรรกะ Odometer ทริกเกอร์ระบบแจ้งเตือน Smart PM
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.PM_ALERT_THRESHOLD_KM = void 0;
exports.isMaintenanceDue = isMaintenanceDue;
/** เกณฑ์เตือน PM (กม.) — ต้อง sync กับ logitrack-web/lib/maintenancePm.ts */
exports.PM_ALERT_THRESHOLD_KM = 2000;
/**
 * ตรวจสอบว่าควรสร้างงาน PM Booking หรือไม่: เหลือไม่เกิน threshold กม. ถึงรอบ หรือเลยกำหนดแล้ว
 */
function isMaintenanceDue(mileage, nextServiceMileage) {
    const m = Number(mileage);
    const next = Number(nextServiceMileage);
    if (!Number.isFinite(m) || !Number.isFinite(next))
        return false;
    const distanceLeft = next - m;
    return distanceLeft <= exports.PM_ALERT_THRESHOLD_KM;
}
//# sourceMappingURL=maintenance.js.map