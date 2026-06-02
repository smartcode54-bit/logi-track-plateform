/**
 * Fix missing deliveredTimestamp for ZXZB26011192271
 * Run: node functions/scripts/fix-delivered-timestamp.js
 */

const admin = require("firebase-admin");
const serviceAccount = require("D:/Secret/logitrack-prod-firebase-adminsdk-fbsvc-da7398c08a.json");

admin.initializeApp({
  credential: admin.credential.cert(serviceAccount),
  projectId: "logitrack-prod",
});

const db = admin.firestore();

async function fix() {
  const DOC_ID = "ZXZB26011192271";
  const ref = db.collection("trip_records").doc(DOC_ID);
  const snap = await ref.get();

  if (!snap.exists) {
    console.error("❌ ไม่พบ doc:", DOC_ID);
    process.exit(1);
  }

  const data = snap.data();
  console.log("📄 Doc:", DOC_ID);
  console.log("   status:             ", data.status);
  console.log("   deliveredTimestamp: ", data.deliveredTimestamp ?? "— (ไม่มี)");
  console.log("   updatedAt:          ", data.updatedAt?.toDate?.() ?? data.updatedAt);
  console.log("   std:                ", data.std?.toDate?.() ?? data.std);
  console.log("   sta:                ", data.sta?.toDate?.() ?? data.sta);

  // 29/05/2026 03:00 Bangkok = UTC+7 → UTC 2026-05-28T20:00:00Z
  const deliveredTimestamp = admin.firestore.Timestamp.fromDate(new Date("2026-05-28T20:00:00.000Z"));

  await ref.update({
    deliveredTimestamp,
    updatedAt: admin.firestore.FieldValue.serverTimestamp(),
  });

  console.log("\n✅ ตั้ง deliveredTimestamp เป็น 29/05/2026 03:00 Bangkok");
}

fix().catch((err) => {
  console.error("❌ Error:", err);
  process.exit(1);
});
