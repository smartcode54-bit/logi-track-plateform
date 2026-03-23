"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
const admin = __importStar(require("firebase-admin"));
if (admin.apps.length === 0) {
    admin.initializeApp();
}
const db = admin.firestore();
async function check() {
    try {
        console.log("=== Checking Truck 5ขด7371 ===");
        const snapshot = await db.collection("trucks").where("licensePlate", "==", "5ขด7371").get();
        if (snapshot.empty) {
            console.log("❌ ไม่พบข้อมูลรถทะเบียน 5ขด7371 ในฐานข้อมูลครับ");
            return;
        }
        const doc = snapshot.docs[0];
        const data = doc.data();
        console.log("🆔 ID:", doc.id);
        console.log("🚚 ทะเบียน:", data.licensePlate);
        console.log("📊 กิโลเมตรปัจจุบัน (currentMileage):", data.currentMileage);
        console.log("📅 ระยะเช็คถัดไป (nextServiceMileage):", data.nextServiceMileage);
        console.log("🚨 ครั้งแจ้งเตือนล่าสุด (lastAlertMileage):", data.lastAlertMileage);
        console.log("\n=== Checking Maintenance Tasks for this truck ===");
        const maintenanceSnap = await db.collection("maintenance")
            .where("truckId", "==", doc.id)
            .get();
        console.log(`พบใบงานทั้งหมด: ${maintenanceSnap.size} รายการ`);
        maintenanceSnap.docs.forEach((d) => {
            const mData = d.data();
            console.log(` - [${mData.status}] Type: ${mData.type} ${mData.serviceType || ""} (Created: ${mData.createdAt?.toDate?.()?.toLocaleString() || ""})`);
        });
    }
    catch (err) {
        console.error("Error checking database:", err);
    }
}
check();
//# sourceMappingURL=check_truck_status.js.map