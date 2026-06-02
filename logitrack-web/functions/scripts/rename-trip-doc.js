/**
 * Rename trip_records doc ID: 37719536 → ZXZB26011192271
 * Run: node functions/scripts/rename-trip-doc.js
 */

const admin = require("firebase-admin");
const serviceAccount = require("D:/Secret/logitrack-prod-firebase-adminsdk-fbsvc-da7398c08a.json");

admin.initializeApp({
  credential: admin.credential.cert(serviceAccount),
  projectId: "logitrack-prod",
});

const db = admin.firestore();

async function renameDoc() {
  const OLD_ID = "36601950";
  const NEW_ID = "ZXZB26011048091";
  const COLLECTION = "trip_records";

  const oldRef = db.collection(COLLECTION).doc(OLD_ID);
  const newRef = db.collection(COLLECTION).doc(NEW_ID);

  // ตรวจสอบก่อน
  const [oldSnap, newSnap] = await Promise.all([oldRef.get(), newRef.get()]);

  if (!oldSnap.exists) {
    console.error(`❌ ไม่พบ doc ID: ${OLD_ID}`);
    process.exit(1);
  }
  if (newSnap.exists) {
    console.error(`❌ มี doc ID: ${NEW_ID} อยู่แล้ว — ยกเลิกเพื่อความปลอดภัย`);
    process.exit(1);
  }

  const data = oldSnap.data();
  console.log(`✅ พบ doc เก่า: ${OLD_ID}`);
  console.log(`   spxTripId: ${data.spxTripId}`);
  console.log(`   status:    ${data.status}`);
  console.log(`   driverId:  ${data.driverId}`);

  // Copy → Delete ใน batch
  const batch = db.batch();
  batch.set(newRef, data);
  batch.delete(oldRef);
  await batch.commit();

  console.log(`\n✅ เสร็จแล้ว: ${OLD_ID} → ${NEW_ID}`);
}

renameDoc().catch((err) => {
  console.error("❌ Error:", err);
  process.exit(1);
});
