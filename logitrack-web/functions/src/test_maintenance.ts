/**
 * 🛠️ Standalone Unit Test Script: ตรวจสอบตรรกะระยะ ODO Meter (Preventive Maintenance)
 */

// 1. ตรรกะแบบจำลอง (Mocking Logic เหมือนใน checkMaintenanceAlert)
function checkMaintenanceAlertLogic(mileage: number, nextServiceMileage: number, lastAlertMileage: number = 0): { 
    shouldAlert: boolean;
    reason: string;
} {
    if (mileage === lastAlertMileage) {
        return { shouldAlert: false, reason: "ระงับการยิงซ้ำ (Already Alerted)" };
    }

    const distanceLeft = nextServiceMileage - mileage;

    if (distanceLeft <= 2000) {
        const label = distanceLeft < 0 ? "เลยกำหนด — ยังต้องเปิด PM Booking" : `เข้าเงื่อนไขแจ้งเตือน (เหลืออีก ${distanceLeft} กม.)`;
        return { shouldAlert: true, reason: label };
    }

    return { shouldAlert: false, reason: `ปลอดภัย (เหลืออีก ${distanceLeft} กม.)` };
}

// 2. ชุดข้อสอบ (Test Cases Scenario)
function runTests() {
    console.log("=========================================");
    console.log("🚗 เริ่มการรัน Unit Test: ODO Maintenance");
    console.log("=========================================\n");

    const testCases = [
        {
            name: "กรณีระยะห่าง 2,000 กม. พอดี",
            mileage: 38000,
            nextServiceMileage: 40000,
            lastAlert: 0,
            expected: true
        },
        {
            name: "กรณีระยะห่างเกิน 2,000 กม. (2,500 กม.)",
            mileage: 37500,
            nextServiceMileage: 40000,
            lastAlert: 0,
            expected: false
        },
        {
            name: "กรณีคนขับกรอกเลขซ้ำที่จุดเคยยิงสำเร็จมาก่อน",
            mileage: 38000,
            nextServiceMileage: 40000,
            lastAlert: 38000,
            expected: false
        },
        {
            name: "กรณีใกล้ชนระยะจริง (เหลือ 100 กม.)",
            mileage: 39900,
            nextServiceMileage: 40000,
            lastAlert: 0,
            expected: true
        },
        {
            name: "กรณีเกินระยะเช็คไปแล้ว (คนขับมองข้าม)",
            mileage: 40500,
            nextServiceMileage: 40000,
            lastAlert: 0,
            expected: true // overdue → ต้องสร้าง PM Booking
        }
    ];

    let passedCount = 0;

    testCases.forEach((scenario, index) => {
        const result = checkMaintenanceAlertLogic(scenario.mileage, scenario.nextServiceMileage, scenario.lastAlert);
        const isPassed = result.shouldAlert === scenario.expected;

        console.log(`[Test #${index + 1}] -> ${scenario.name}`);
        console.log(`  🔍 ตรวจไมล์: ${scenario.mileage} / ${scenario.nextServiceMileage}`);
        console.log(`  💡 ผลลัพธ์: ${result.reason}`);
        
        if (isPassed) {
            console.log("  ✅ ผ่าน (PASSED)\n");
            passedCount++;
        } else {
            console.log("  ❌ ล้มเหลว (FAILED)\n");
        }
    });

    console.log("=========================================");
    if (passedCount === testCases.length) {
        console.log("🎉 สรุปภาพรวม: ✅ ผ่านทั้งหมด 100% ครบถ้วน!");
    } else {
        console.log(`🎉 สรุปภาพรวม: ผ่าน ${passedCount}/${testCases.length} รายการ`);
    }
    console.log("=========================================");
}

runTests();
