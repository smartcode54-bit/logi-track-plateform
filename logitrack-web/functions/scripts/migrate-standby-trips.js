/**
 * ONE-TIME MIGRATION: trip_records (status=standby) → standby_records
 *
 * Migrates the 2 historical trips that were recorded as trip_records with
 * status="standby" before the standby_records collection existed.
 *
 * After migration each new standby_record has status="completed" so the
 * existing autoComputeStandbyBilling / backfillStandbyBillingSnapshots
 * functions can compute billing normally.
 *
 * Auth: set GOOGLE_APPLICATION_CREDENTIALS before running, OR run from a
 *       machine with application-default credentials (gcloud auth adc).
 *
 * Usage:
 *   node scripts/migrate-standby-trips.js [--project logitrack-prod] [--dry-run]
 *
 * Flags:
 *   --project <id>   Firebase project ID (default: logitrack-prod)
 *   --dry-run        Print what would be written without touching Firestore
 */

const admin = require("firebase-admin");

// ── Config ────────────────────────────────────────────────────────────────────
const SPX_TRIP_IDS_TO_MIGRATE = [
    "ZXZB26010240771",
    "ZXJB26010099540",
];

const args = process.argv.slice(2);
const projectIdx = args.indexOf("--project");
const PROJECT_ID = projectIdx >= 0 ? args[projectIdx + 1] : "logitrack-prod";
const DRY_RUN = args.includes("--dry-run");

// ─────────────────────────────────────────────────────────────────────────────

if (!admin.apps.length) {
    admin.initializeApp({ projectId: PROJECT_ID });
}
const db = admin.firestore();

function toDate(val) {
    if (!val) return null;
    if (val && typeof val.toDate === "function") return val.toDate();
    if (val instanceof Date) return val;
    if (typeof val === "string" || typeof val === "number") return new Date(val);
    return null;
}

function diffMinutes(start, end) {
    if (!start || !end) return null;
    const ms = end.getTime() - start.getTime();
    if (ms < 0) return null;
    return Math.round(ms / 60000);
}

async function resolveCustomerId(task) {
    if (!task) return null;
    return task.sourceHubLinkedCustomerId?.trim() ||
           task.destinationLinkedCustomerId?.trim() ||
           null;
}

async function migrate() {
    console.log(`\n🚀  Migrating ${SPX_TRIP_IDS_TO_MIGRATE.length} standby trips`);
    console.log(`    Project : ${PROJECT_ID}`);
    console.log(`    Dry-run : ${DRY_RUN}\n`);

    // 1. Find trip_records by spxTripId
    let found = [];
    for (const spxId of SPX_TRIP_IDS_TO_MIGRATE) {
        const snap = await db
            .collection("trip_records")
            .where("spxTripId", "==", spxId)
            .limit(1)
            .get();

        if (snap.empty) {
            // Fallback: maybe the ID is the doc ID
            const docSnap = await db.collection("trip_records").doc(spxId).get();
            if (docSnap.exists) {
                found.push({ id: docSnap.id, data: docSnap.data() });
                console.log(`✅  Found by doc ID  : ${spxId}`);
            } else {
                console.warn(`⚠️   Not found in trip_records: ${spxId}`);
            }
        } else {
            const doc = snap.docs[0];
            found.push({ id: doc.id, data: doc.data() });
            console.log(`✅  Found by spxTripId: ${spxId} → tripId=${doc.id}`);
        }
    }

    if (found.length === 0) {
        console.error("❌  No trips found. Exiting.");
        process.exit(1);
    }

    // 2. For each trip, look up its task and build the standby_record payload
    let migrated = 0;
    let skipped = 0;

    for (const { id: tripId, data: trip } of found) {
        const spxTripId = trip.spxTripId ?? tripId;
        console.log(`\n── Processing trip ${spxTripId} (docId: ${tripId})`);

        // Check if already migrated (avoid duplicates)
        const existing = await db
            .collection("standby_records")
            .where("migratedFromTripId", "==", tripId)
            .limit(1)
            .get();

        if (!existing.empty) {
            console.log(`   ↩️  Already migrated → standbyId=${existing.docs[0].id}. Skipping.`);
            skipped++;
            continue;
        }

        // Look up task for location/customer info
        const taskId = typeof trip.taskId === "string" ? trip.taskId.trim() : null;
        let task = null;
        if (taskId) {
            try {
                const taskSnap = await db.collection("tasks").doc(taskId).get();
                if (taskSnap.exists) task = taskSnap.data();
            } catch (e) {
                console.warn(`   ⚠️  Could not load task ${taskId}:`, e.message);
            }
        }

        const customerId = await resolveCustomerId(task);
        const startedAt = toDate(trip.createdAt);
        const endedAt = toDate(trip.updatedAt);
        const durationMinutes = diffMinutes(startedAt, endedAt);

        const startLocation = task?.sourceHub ?? trip.origin ?? null;
        const endLocation = task?.destination ?? trip.destination ?? null;

        const payload = {
            // Core fields
            status: "completed",
            driverId: trip.driverId ?? null,
            taskId: taskId,
            startLocation: startLocation ?? null,
            endLocation: endLocation ?? null,
            startedAt: startedAt ? admin.firestore.Timestamp.fromDate(startedAt) : null,
            endedAt: endedAt ? admin.firestore.Timestamp.fromDate(endedAt) : null,
            durationMinutes: durationMinutes,
            photos: [],
            note: `Migrated from trip_records/${tripId} (spxTripId: ${spxTripId})`,
            lat: trip.lat ?? null,
            lng: trip.lng ?? null,
            // Billing hint (billing function will compute from rate entries)
            customerId: customerId,
            // Migration provenance
            migratedFromTripId: tripId,
            migratedFromSpxTripId: spxTripId,
            migratedAt: admin.firestore.FieldValue.serverTimestamp(),
            createdAt: startedAt
                ? admin.firestore.Timestamp.fromDate(startedAt)
                : admin.firestore.FieldValue.serverTimestamp(),
        };

        console.log(`   📋  Payload:`);
        console.log(`       driverId      : ${payload.driverId ?? "(none)"}`);
        console.log(`       taskId        : ${payload.taskId ?? "(none)"}`);
        console.log(`       customerId    : ${payload.customerId ?? "(none — billing will fail)"}`);
        console.log(`       startLocation : ${payload.startLocation ?? "(none)"}`);
        console.log(`       endLocation   : ${payload.endLocation ?? "(none)"}`);
        console.log(`       startedAt     : ${startedAt?.toISOString() ?? "(none)"}`);
        console.log(`       endedAt       : ${endedAt?.toISOString() ?? "(none)"}`);
        console.log(`       durationMins  : ${payload.durationMinutes ?? "(none)"}`);

        if (DRY_RUN) {
            console.log(`   🔵  DRY-RUN — would write to standby_records (auto-id)`);
        } else {
            const newRef = await db.collection("standby_records").add(payload);
            console.log(`   ✅  Created standby_records/${newRef.id}`);
        }

        migrated++;
    }

    console.log(`\n${"─".repeat(60)}`);
    console.log(`Done.  Migrated: ${migrated}  Skipped (already done): ${skipped}`);
    if (!DRY_RUN && migrated > 0) {
        console.log(`\n👉  Next step:`);
        console.log(`    1. Make sure standby_rate_entries has a rate for the customer(s) above.`);
        console.log(`    2. Go to Utilities → Backfill Standby Billing and run for the date range.`);
        console.log(`       OR call computeStandbyBillingSnapshot from Firebase Console for each ID.`);
    }
    process.exit(0);
}

migrate().catch((err) => {
    console.error("Fatal error:", err);
    process.exit(1);
});
