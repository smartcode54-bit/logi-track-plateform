/**
 * 🛠️ Core Maintenance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับรันตรรกะ Odometer ทริกเกอร์ระบบแจ้งเตือน Smart PM
 */

/**
 * ตรวจสอบว่าครบรอบการแจ้งเตือนเช็คระยะ (Threshold <= 2,000 กม.) หรือไม่
 * @param mileage เลขไมล์ปัจจุบัน (จากฟอร์มเติมน้ำมัน)
 * @param nextServiceMileage เลขไมล์รอบเช็คระยะถัดไป
 */
export function isMaintenanceDue(mileage: number, nextServiceMileage: number): boolean {
    const distanceLeft = nextServiceMileage - mileage;
    
    // ตรวจสอบระยะห่าง <= 2,000 กม. และ ยังไม่เลยกำหนดขับขี่ ( distanceLeft >= 0 )
    return distanceLeft <= 2000 && distanceLeft >= 0;
}
