/**
 * DIAGNOSTIC (read-only): find CJSF trips where FUEL was applied to what should be a
 * fixed-rate SUPPLEMENTARY (เสริม) trip.
 *
 * WHY THIS EXISTS
 *   Supplementary rate cards are fixed, separately-agreed prices — they must NOT move with
 *   the primary fuel-rate adjustment (ADR-0005). The per-trip math already excludes fuel
 *   when jobCategory === "SUPPLEMENTARY". The leak is UPSTREAM, on the TASK:
 *     - A task created without picking หลัก/เสริม is stamped PRIMARY (schema/dialog default).
 *     - billing then bills it PRIMARY → the fuel multiplier is applied.
 *   The category's source of truth is the assign-time choice on the task; when it was never
 *   set to เสริม, the trip silently carries fuel.
 *
 * This script only READS Firestore and reports the affected trips, bucketed by how they
 * should be fixed. It writes nothing.
 *
 * BUCKETS
 *   A) SUPP-labeled but fuel applied  — effective category is SUPPLEMENTARY yet the snapshot
 *      carries fuel (a pre-fix / stale snapshot). A plain force-recompute is BLOCKED by the
 *      frozen guard (tripBillingOnDelivered.ts) because jobCategory is already SUPPLEMENTARY.
 *      Fix path: Edit Trip → หลัก/เสริม (setTripJobCategory) re-derives + re-freezes; or a data-fix.
 *   B) PRIMARY/unset + fuel + a SUPP rate card exists for the route — a candidate เสริม trip that
 *      was never marked. Only ops can confirm (route also has a PRIMARY card, so code can't tell).
 *      Fix path: mark the TASK เสริม (Edit Trip), which recomputes with no fuel.
 *   C) PRIMARY + fuel + NO supp card for the route — genuine หลัก. Ignored (correct).
 *   D) no fuel — correct.
 *
 * Auth: GOOGLE_APPLICATION_CREDENTIALS, or ADC (gcloud auth application-default login) for the
 *       project that owns the data. Prod project is `logitrack-prod`.
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/diagnose-cjsf-supplementary-fuel.js --project logitrack-prod
 *   node functions/scripts/diagnose-cjsf-supplementary-fuel.js --customer CJSF --csv out.csv
 */

const fs = require("fs");
const admin = require("firebase-admin");

const args = process.argv.slice(2);
function flag(name, def) {
    const i = args.indexOf(`--${name}`);
    return i >= 0 ? args[i + 1] : def;
}
const PROJECT_ID = flag("project", "logitrack-prod");
const CUSTOMER_QUERY = (flag("customer", "CJSF") || "CJSF").trim().toUpperCase();
const CSV_PATH = flag("csv"); // optional: dump A+B rows to CSV for review
const MULT_EPS = 1e-6;
const THB_EPS = 0.01;

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

// ── helpers (mirror lib/billingCompute.ts, kept inline so the script is self-contained) ──
function toMs(v) {
    if (!v) return 0;
    if (typeof v === "number") return v;
    if (v instanceof Date) return v.getTime();
    if (typeof v.toMillis === "function") return v.toMillis();
    if (typeof v.toDate === "function") return v.toDate().getTime();
    return 0;
}
function normCode(v) {
    return (v ?? "").trim().toUpperCase();
}
function normDest(d) {
    const u = normCode(d);
    if (!u) return "";
    if (u.startsWith("SOCE")) return "SOCE";
    if (u.startsWith("SOCN")) return "SOCN";
    if (u.startsWith("SOCW")) return "SOCW";
    const i = u.indexOf("-");
    return i > 0 ? u.slice(0, i).trim() : u;
}
function extractHub(s) {
    const raw = (s ?? "").trim();
    if (!raw) return "";
    return normCode((raw.split(" - ")[0] ?? raw).trim());
}
function normVeh(v) {
    const u = normCode(v || "4WJ") || "4WJ";
    const m = {
        PICKUP: "4W", "4WH": "4W",
        "4 WHEELS": "4WJ", "4 WHEELS JUMBO": "4WJ",
        "6 WHEELS": "6WH", "6W": "6WH",
        "10 WHEELS": "10WH", "10W": "10WH",
        "18 WHEELS": "18WH", "18W": "18WH",
        "2 WHEELS": "2W",
    };
    return m[u] ?? u;
}
function normCat(v) {
    return v === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : v === "PRIMARY" ? "PRIMARY" : undefined;
}
function bkkDate(ms) {
    return new Date(ms + 7 * 3600 * 1000).toISOString().slice(0, 10);
}
function bkkMonth(ms) {
    return bkkDate(ms).slice(0, 7);
}
// selectBillingRateEntry: newest effective on/before date; fallback to oldest.
function selectRate(entries, dateMs) {
    if (!entries || entries.length === 0) return null;
    const eff = entries.filter((e) => e.effectiveFromMs <= dateMs).sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (eff.length) return eff[0];
    return [...entries].sort((a, b) => a.effectiveFromMs - b.effectiveFromMs)[0];
}
function fmt(n) {
    return typeof n === "number" && Number.isFinite(n) ? n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : "-";
}

async function main() {
    console.log(`\n=== CJSF SUPPLEMENTARY-FUEL DIAGNOSTIC (project: ${PROJECT_ID}) ===\n`);

    // ── 1. resolve customer ────────────────────────────────────────────────
    const custSnap = await db.collection("customers").get();
    const matches = [];
    custSnap.docs.forEach((d) => {
        const c = d.data();
        const name = String(c.name ?? c.customerName ?? "").trim();
        const code = String(c.code ?? c.customerCode ?? "").trim();
        const hay = `${code} ${name}`.toUpperCase();
        if (hay.includes(CUSTOMER_QUERY)) matches.push({ id: d.id, name, code });
    });
    if (matches.length === 0) {
        console.error(`❌  no customer matched "${CUSTOMER_QUERY}". Pass --customer <code|name>.`);
        process.exit(1);
    }
    if (matches.length > 1) {
        console.log(`⚠️  ${matches.length} customers matched "${CUSTOMER_QUERY}":`);
        matches.forEach((m) => console.log(`     ${m.id}  code=${m.code || "-"}  name=${m.name || "-"}`));
        console.log(`   Using the first. Narrow with --customer if wrong.\n`);
    }
    const cust = matches[0];
    console.log(`[customer] ${cust.id}  code=${cust.code || "-"}  name=${cust.name || "-"}`);

    // ── 2. rate cards → route→entries maps, split by category ───────────────
    const rateSnap = await db.collection("customer_rate_entries").where("customerId", "==", cust.id).get();
    const suppByRoute = new Map(); // "HUB::DEST::VEH" -> [entries]
    const primaryRoutes = new Set();
    const suppRoutes = new Set();
    rateSnap.docs.forEach((d) => {
        const e = d.data();
        const key = `${normCode(e.hubId)}::${normDest(e.destinationCode)}::${normVeh(e.vehicleClass)}`;
        const cat = normCat(e.jobCategory) ?? "PRIMARY";
        if (cat === "SUPPLEMENTARY") {
            suppRoutes.add(key);
            const arr = suppByRoute.get(key) ?? [];
            arr.push({ rateThb: Number(e.rateThb ?? 0), effectiveFromMs: toMs(e.effectiveFrom) });
            suppByRoute.set(key, arr);
        } else {
            primaryRoutes.add(key);
        }
    });
    const bothRoutes = [...suppRoutes].filter((k) => primaryRoutes.has(k));
    console.log(`[rate cards] total ${rateSnap.size} | PRIMARY routes ${primaryRoutes.size} | SUPPLEMENTARY routes ${suppRoutes.size} | routes with BOTH ${bothRoutes.length}`);

    // ── 3. delivered + billed trips for this customer ──────────────────────
    const tripSnap = await db.collection("trip_records").where("billingCustomerId", "==", cust.id).get();
    console.log(`[trip_records billingCustomerId==CJSF] total ${tripSnap.size}\n`);

    // batch-load tasks
    const trips = tripSnap.docs.map((d) => ({ id: d.id, data: d.data() }));
    const taskIds = [...new Set(trips.map((t) => (typeof t.data.taskId === "string" ? t.data.taskId.trim() : "")).filter(Boolean))];
    const taskCat = new Map(); // taskId -> { jobCategory, sourceHub, destination, truckType }
    for (let i = 0; i < taskIds.length; i += 30) {
        const chunk = taskIds.slice(i, i + 30);
        const snap = await db.collection("tasks").where(admin.firestore.FieldPath.documentId(), "in", chunk).get();
        snap.forEach((d) => {
            const t = d.data();
            taskCat.set(d.id, {
                jobCategory: normCat(t.jobCategory),
                sourceHub: t.sourceHub,
                destination: t.destination,
                truckType: t.truckType,
            });
        });
    }

    const bucketA = []; // SUPP-labeled + fuel  (frozen; fix via Edit Trip / data-fix)
    const bucketB = []; // PRIMARY/unset + fuel + supp card exists (candidate เสริม; ops confirm)
    let cCount = 0, dCount = 0, notDelivered = 0;

    for (const { id, data } of trips) {
        if (data.status !== "delivered") { notDelivered++; continue; }
        if (typeof data.billingEstimateThb !== "number") continue;

        const task = (typeof data.taskId === "string" && taskCat.get(data.taskId.trim())) || {};
        const effCat = normCat(data.jobCategory) ?? task.jobCategory ?? undefined;

        const mult = Number(data.billingRateMultiplier ?? 1);
        const addPer = Number(data.billingAddThbPerTrip ?? 0);
        const est = Number(data.billingEstimateThb);
        const base = Number(data.billingBaseRateThb ?? 0);
        const hasFuelId = !!data.billingFuelAdjustmentId;
        const isMulti = data.isMultiDelivery === true;
        // "fuel applied": multiplier ≠ 1, a per-trip add, a fuel-adjustment id, or (single-leg) est ≠ base.
        const fuelApplied =
            Math.abs(mult - 1) > MULT_EPS ||
            Math.abs(addPer) > THB_EPS ||
            hasFuelId ||
            (!isMulti && Math.abs(est - base) > THB_EPS);
        if (!fuelApplied) { dCount++; continue; }

        const hubId = normCode(data.billingLookupHubId || extractHub(task.sourceHub));
        const dest = normDest(data.billingLookupDestination || task.destination);
        const veh = normVeh(task.truckType);
        const routeKey = `${hubId}::${dest}::${veh}`;
        const suppExists = suppRoutes.has(routeKey);
        const deliveredMs = toMs(data.deliveredTimestamp) || toMs(data.createdAt);
        const correctSupp = suppExists ? selectRate(suppByRoute.get(routeKey), deliveredMs)?.rateThb ?? null : null;

        const row = {
            id, spx: data.spxTripId || "", month: deliveredMs ? bkkMonth(deliveredMs) : "?",
            date: deliveredMs ? bkkDate(deliveredMs) : "?",
            route: `${hubId}→${dest}`, veh, effCat: effCat ?? "(unset)",
            base, est, mult, addPer, fuelId: data.billingFuelAdjustmentId || "",
            frozen: data.billingManualOverride === true || normCat(data.jobCategory) === "SUPPLEMENTARY",
            isMulti, suppExists, correctSupp,
        };

        if (effCat === "SUPPLEMENTARY") bucketA.push(row);
        else if (suppExists) bucketB.push(row); // PRIMARY/unset + fuel + a supp card exists
        else cCount++;
    }

    // ── 4. report ──────────────────────────────────────────────────────────
    const byMonth = (rows) => {
        const m = new Map();
        rows.forEach((r) => m.set(r.month, (m.get(r.month) ?? 0) + 1));
        return [...m.entries()].sort().map(([k, v]) => `${k}:${v}`).join("  ") || "(none)";
    };
    console.log(`──────────── SUMMARY ────────────`);
    console.log(`  delivered+billed scanned : ${trips.length - notDelivered}`);
    console.log(`  D) no fuel (correct)      : ${dCount}`);
    console.log(`  C) หลัก + fuel, no supp   : ${cCount}   (genuine PRIMARY — ignored)`);
    console.log(`  A) เสริม-labeled + fuel   : ${bucketA.length}   ← wrong; frozen guard blocks recompute`);
    console.log(`  B) หลัก/unset + fuel +    : ${bucketB.length}   ← candidate เสริม; ops must confirm`);
    console.log(`     supp card exists`);
    console.log(`  by month  A: ${byMonth(bucketA)}`);
    console.log(`  by month  B: ${byMonth(bucketB)}`);

    const printRows = (label, rows) => {
        if (rows.length === 0) return;
        console.log(`\n──────────── ${label} (${rows.length}) ────────────`);
        console.log(`  # | doc/spx | date | route (veh) | cat | base → est (mult, add) | correct เสริม | Δfuel | frozen`);
        rows.sort((a, b) => (a.date < b.date ? -1 : 1));
        rows.forEach((r, i) => {
            const suppPart = r.correctSupp != null ? `฿${fmt(r.correctSupp)}` : (r.suppExists ? "(supp card, no eff rate)" : "(no supp card)");
            const deltaFuel = r.isMulti ? "(multi)" : `฿${fmt(r.est - r.base)}`;
            console.log(
                `  ${String(i + 1).padStart(3)} | ${(r.spx || r.id).padEnd(16)} | ${r.date} | ${r.route} (${r.veh}) | ${String(r.effCat).padEnd(13)} | ฿${fmt(r.base)} → ฿${fmt(r.est)} (x${r.mult}${r.addPer ? `, +${r.addPer}` : ""}) | ${suppPart} | ${deltaFuel} | ${r.frozen ? "FROZEN" : ""}`
            );
        });
    };
    printRows("BUCKET A — เสริม-labeled but fuel applied", bucketA);
    printRows("BUCKET B — หลัก/unset + fuel + a เสริม rate card exists (candidate เสริม)", bucketB);

    // ── 5. optional CSV of A+B ─────────────────────────────────────────────
    if (CSV_PATH) {
        const esc = (v) => `"${String(v ?? "").replace(/"/g, '""')}"`;
        const header = "bucket,docId,spxTripId,month,date,route,veh,effectiveCategory,baseRateThb,billingEstimateThb,rateMultiplier,addThbPerTrip,fuelAdjustmentId,frozen,isMultiDelivery,suppCardExists,correctSuppRateThb,fuelDeltaThb";
        const line = (b, r) =>
            [b, r.id, r.spx, r.month, r.date, r.route, r.veh, r.effCat, r.base, r.est, r.mult, r.addPer, r.fuelId, r.frozen, r.isMulti, r.suppExists, r.correctSupp ?? "", r.isMulti ? "" : (r.est - r.base)].map(esc).join(",");
        const out = [header, ...bucketA.map((r) => line("A", r)), ...bucketB.map((r) => line("B", r))].join("\n");
        fs.writeFileSync(CSV_PATH, out, "utf8");
        console.log(`\n📄  wrote ${bucketA.length + bucketB.length} rows → ${CSV_PATH}`);
    }

    console.log(`\nFIX (nothing written by this script):`);
    console.log(`  • Bucket A/B → open the trip in Driver Monitor → Edit Trip → set หลัก/เสริม to "เสริม".`);
    console.log(`    That calls setTripJobCategory: writes tasks.jobCategory (source of truth) AND re-derives`);
    console.log(`    the snapshot for SUPPLEMENTARY (fuel removed, price re-frozen) atomically.`);
    console.log(`  • A plain "force recompute" will NOT fix Bucket A — the frozen guard skips SUPP trips.`);
    console.log(`\n=== DONE ===\n`);
    process.exit(0);
}

main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
