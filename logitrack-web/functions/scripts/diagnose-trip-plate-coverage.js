/**
 * DIAGNOSTIC (read-only): how many trip_records have NO truck plate provenance?
 *
 * Sizes the display regression introduced by the truck-plate-filter spec (ADR 0005 §6):
 * `getLicensePlate` stops falling back to `drivers.activeTruck.truckPlate`, so any trip with
 *   - no `trip_records.truckLicensePlate` (loading-phase snapshot), AND
 *   - no `truckId`, AND
 *   - no `licensePlate` on its linked task
 * will render "-" where it may show a (guessed, possibly wrong) plate today.
 *
 * If the count is material, run `backfillTripTruckData` (Utilities → Backfill) BEFORE shipping
 * the UI change, then re-run this script.
 *
 * Reads (never writes) Firestore.
 *
 * Auth: set GOOGLE_APPLICATION_CREDENTIALS, or run on a machine with ADC
 *       (gcloud auth application-default login).
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/diagnose-trip-plate-coverage.js [--project logitrack-prod] [--months 6]
 */

const admin = require("firebase-admin");

const args = process.argv.slice(2);
const projectIdx = args.indexOf("--project");
const PROJECT_ID = projectIdx >= 0 ? args[projectIdx + 1] : "logitrack-prod";
const monthsIdx = args.indexOf("--months");
const MONTHS_BACK = monthsIdx >= 0 ? Number(args[monthsIdx + 1]) : 0; // 0 = all time

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

function toDate(v) {
    if (!v) return null;
    if (v instanceof Date) return v;
    if (typeof v.toDate === "function") return v.toDate();
    if (typeof v === "number" && Number.isFinite(v)) return new Date(v);
    if (typeof v === "string") {
        const d = new Date(v);
        return Number.isNaN(d.getTime()) ? null : d;
    }
    return null;
}

function monthKey(d) {
    return d ? `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}` : "(no date)";
}

function str(v) {
    return String(v ?? "").trim();
}

function pct(n, total) {
    return total > 0 ? `${((n / total) * 100).toFixed(1)}%` : "0.0%";
}

async function main() {
    console.log(`\n=== TRIP PLATE COVERAGE DIAGNOSTIC (project: ${PROJECT_ID}) ===`);
    if (MONTHS_BACK > 0) console.log(`Window: last ${MONTHS_BACK} month(s) by createdAt`);
    else console.log(`Window: all time`);
    console.log("");

    // ── TASKS: taskId/docId → licensePlate, truckId ─────────────────────────
    // Keyed by BOTH doc id and the business `taskId`, mirroring how the web resolves a trip's
    // task (useDriverMonitor.ts taskById).
    const taskPlate = new Map();
    const taskTruckId = new Map();
    const taskSnap = await db.collection("tasks").get();
    taskSnap.forEach((d) => {
        const t = d.data();
        const plate = str(t.licensePlate);
        const truckId = str(t.truckId);
        taskPlate.set(d.id, plate);
        taskTruckId.set(d.id, truckId);
        const bizId = str(t.taskId);
        if (bizId) {
            taskPlate.set(bizId, plate);
            taskTruckId.set(bizId, truckId);
        }
    });
    console.log(`[tasks]  loaded: ${taskSnap.size} (${taskPlate.size} lookup keys)`);

    // ── TRUCKS: truckId → plate (to report orphan truckIds) ─────────────────
    const truckPlates = new Map();
    const trucksSnap = await db.collection("trucks").get();
    trucksSnap.forEach((d) => truckPlates.set(d.id, str(d.data().licensePlate)));
    console.log(`[trucks] loaded: ${trucksSnap.size}`);

    // ── TRIPS ───────────────────────────────────────────────────────────────
    let tripQuery = db.collection("trip_records");
    if (MONTHS_BACK > 0) {
        const cutoff = new Date();
        cutoff.setMonth(cutoff.getMonth() - MONTHS_BACK);
        tripQuery = tripQuery.where("createdAt", ">=", admin.firestore.Timestamp.fromDate(cutoff));
    }
    const tripSnap = await tripQuery.get();
    const total = tripSnap.size;
    console.log(`[trips]  loaded: ${total}\n`);

    const counts = {
        tripSnapshot: 0, // has trip_records.truckLicensePlate  → unaffected
        taskFallback: 0, // no snapshot, but task has a plate    → unaffected (new fallback)
        regress: 0, // no plate anywhere                    → REGRESSES to "-"
    };
    const withTruckId = { yes: 0, no: 0 };
    const orphanTruckIds = new Map(); // truckId not in trucks/
    const orphanPlates = new Map(); // plate string with no truckId anywhere
    const regressByMonth = new Map();
    const regressNoTask = { noTaskId: 0, taskMissing: 0, taskHasNoPlate: 0 };
    const regressSamples = [];

    tripSnap.forEach((d) => {
        const t = d.data();
        const tripPlate = str(t.truckLicensePlate);
        const tripTruckId = str(t.truckId);
        const taskKey = str(t.taskId);
        const tPlate = taskKey ? (taskPlate.get(taskKey) ?? null) : null;
        const tTruckId = taskKey ? (taskTruckId.get(taskKey) ?? null) : null;

        const effTruckId = tripTruckId || str(tTruckId);
        if (effTruckId) {
            withTruckId.yes++;
            if (!truckPlates.has(effTruckId)) {
                orphanTruckIds.set(effTruckId, (orphanTruckIds.get(effTruckId) ?? 0) + 1);
            }
        } else {
            withTruckId.no++;
        }

        if (tripPlate) {
            counts.tripSnapshot++;
        } else if (str(tPlate)) {
            counts.taskFallback++;
        } else {
            counts.regress++;
            const created = toDate(t.createdAt);
            const mk = monthKey(created);
            regressByMonth.set(mk, (regressByMonth.get(mk) ?? 0) + 1);
            if (!taskKey) regressNoTask.noTaskId++;
            else if (tPlate === null) regressNoTask.taskMissing++;
            else regressNoTask.taskHasNoPlate++;
            if (regressSamples.length < 15) {
                regressSamples.push({
                    id: d.id,
                    spx: str(t.spxTripId) || "-",
                    status: str(t.status) || "-",
                    driverId: str(t.driverId) || "-",
                    taskId: taskKey || "(none)",
                    created: created ? created.toISOString().slice(0, 10) : "(no date)",
                });
            }
        }

        // Orphan plate = a plate string that never resolves to a truck doc
        const anyPlate = tripPlate || str(tPlate);
        if (anyPlate && !effTruckId) {
            orphanPlates.set(anyPlate, (orphanPlates.get(anyPlate) ?? 0) + 1);
        }
    });

    // ── REPORT ──────────────────────────────────────────────────────────────
    console.log("─── PLATE PROVENANCE ───────────────────────────────────────");
    console.log(`  trip_records.truckLicensePlate  : ${counts.tripSnapshot}  (${pct(counts.tripSnapshot, total)})  unaffected`);
    console.log(`  task.licensePlate fallback      : ${counts.taskFallback}  (${pct(counts.taskFallback, total)})  unaffected (new chain)`);
    console.log(`  NO PLATE ANYWHERE  → "-"        : ${counts.regress}  (${pct(counts.regress, total)})  ⚠ REGRESSES`);
    console.log("");

    console.log("─── TRUCK IDENTITY ─────────────────────────────────────────");
    console.log(`  has truckId (trip or task)      : ${withTruckId.yes}  (${pct(withTruckId.yes, total)})  filterable by identity`);
    console.log(`  no truckId                      : ${withTruckId.no}  (${pct(withTruckId.no, total)})  plate-string bucket only`);
    console.log("");

    if (counts.regress > 0) {
        console.log("─── REGRESSING TRIPS: why no plate? ────────────────────────");
        console.log(`  trip has no taskId              : ${regressNoTask.noTaskId}`);
        console.log(`  taskId set but task not found   : ${regressNoTask.taskMissing}`);
        console.log(`  task found, but has no plate    : ${regressNoTask.taskHasNoPlate}`);
        console.log("");

        console.log("─── REGRESSING TRIPS BY MONTH ──────────────────────────────");
        [...regressByMonth.entries()]
            .sort((a, b) => a[0].localeCompare(b[0]))
            .forEach(([m, n]) => console.log(`  ${m}  ${String(n).padStart(6)}`));
        console.log("");

        console.log("─── SAMPLES (up to 15) ─────────────────────────────────────");
        regressSamples.forEach((s) =>
            console.log(`  ${s.created}  ${s.id}  spx=${s.spx}  status=${s.status}  task=${s.taskId}`)
        );
        console.log("");
    }

    if (orphanPlates.size > 0) {
        console.log("─── ORPHAN PLATES (plate string, no truckId) ───────────────");
        [...orphanPlates.entries()]
            .sort((a, b) => b[1] - a[1])
            .slice(0, 25)
            .forEach(([p, n]) => console.log(`  ${String(n).padStart(6)}  ${p}`));
        if (orphanPlates.size > 25) console.log(`  … and ${orphanPlates.size - 25} more`);
        console.log("");
    }

    if (orphanTruckIds.size > 0) {
        console.log("─── truckIds NOT IN trucks/ (deleted fleet docs) ───────────");
        [...orphanTruckIds.entries()]
            .sort((a, b) => b[1] - a[1])
            .slice(0, 25)
            .forEach(([id, n]) => console.log(`  ${String(n).padStart(6)}  ${id}`));
        console.log("");
    }

    console.log("─── VERDICT ────────────────────────────────────────────────");
    const ratio = total > 0 ? counts.regress / total : 0;
    if (counts.regress === 0) {
        console.log("  ✅ No trips regress. Ship the UI change as specced.");
    } else if (ratio < 0.01) {
        console.log(`  ✅ ${counts.regress} trips (${pct(counts.regress, total)}) regress — immaterial.`);
        console.log('     Ship as specced; these rows show "-" instead of a guessed plate.');
    } else {
        console.log(`  ⚠ ${counts.regress} trips (${pct(counts.regress, total)}) regress — MATERIAL.`);
        console.log("     Run backfillTripTruckData (Utilities → Backfill), then re-run this script");
        console.log("     before shipping the Driver Monitor change (spec T2 gate).");
    }
    console.log("");
}

main()
    .then(() => process.exit(0))
    .catch((e) => {
        console.error("FAILED:", e);
        process.exit(1);
    });
