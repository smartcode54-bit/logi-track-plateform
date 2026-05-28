// One-off: set { role: "driver", driverId } custom claim on a driver's auth user,
// then revoke tokens so the new claim takes effect on next login.
//
// Why: the `maintenance` Firestore rule reads request.auth.token.driverId. Drivers
// created before that claim was added can't read maintenance -> the vehicle-expense
// page shows [cloud_firestore/permission-denied]. This backfills the claim.
//
// Auth: needs credentials with Firebase Auth Admin + Firestore read on the project.
//   Either:  gcloud auth application-default login   (with an Owner/Editor account)
//   Or:      set GOOGLE_APPLICATION_CREDENTIALS=path\to\serviceAccountKey.json
//
// Usage:
//   node scripts/fix-driver-claim.js <uid> [projectId] [driverDocId]
//   - projectId defaults to logitrack-prod
//   - driverDocId is optional; if omitted, the script finds it via drivers.authId == uid

const admin = require("firebase-admin");

const uid = process.argv[2];
const projectId = process.argv[3] || "logitrack-prod";
let driverDocId = process.argv[4] || "";

if (!uid) {
  console.error("Usage: node scripts/fix-driver-claim.js <uid> [projectId] [driverDocId]");
  process.exit(1);
}

admin.initializeApp({ projectId });

(async () => {
  const user = await admin.auth().getUser(uid);
  console.log(`User: ${user.email || "(no email)"} | current claims:`, JSON.stringify(user.customClaims || {}));

  if (!driverDocId) {
    const snap = await admin.firestore().collection("drivers").where("authId", "==", uid).limit(1).get();
    if (snap.empty) {
      throw new Error(`No drivers doc found with authId == ${uid}. Pass driverDocId explicitly as the 3rd arg.`);
    }
    driverDocId = snap.docs[0].id;
    const d = snap.docs[0].data();
    console.log(`Resolved driverDocId=${driverDocId} | truckId=${d.truckId || d.currentAssignment?.truckId || "(none)"}`);
  }

  const nextClaims = { ...(user.customClaims || {}), role: "driver", driverId: driverDocId };
  await admin.auth().setCustomUserClaims(uid, nextClaims);
  await admin.auth().revokeRefreshTokens(uid);

  console.log("Done. New claims:", JSON.stringify(nextClaims));
  console.log("Driver must sign out and log in again to refresh their ID token.");
  process.exit(0);
})().catch((e) => {
  console.error("ERROR:", e.code || e.message);
  process.exit(1);
});
