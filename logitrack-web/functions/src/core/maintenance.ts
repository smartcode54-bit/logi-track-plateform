/**
 * 🛠️ Core Maintenance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับรันตรรกะ Odometer ทริกเกอร์ระบบแจ้งเตือน Smart PM
 */

/** เกณฑ์เตือน PM (กม.) — ต้อง sync กับ logitrack-web/lib/maintenancePm.ts */
export const PM_ALERT_THRESHOLD_KM = 2000;

/**
 * ตรวจสอบว่าควรสร้างงาน PM Booking หรือไม่: เหลือไม่เกิน threshold กม. ถึงรอบ หรือเลยกำหนดแล้ว
 */
export function isMaintenanceDue(mileage: number, nextServiceMileage: number): boolean {
    const m = Number(mileage);
    const next = Number(nextServiceMileage);
    if (!Number.isFinite(m) || !Number.isFinite(next)) return false;
    const distanceLeft = next - m;
    return distanceLeft <= PM_ALERT_THRESHOLD_KM;
}
