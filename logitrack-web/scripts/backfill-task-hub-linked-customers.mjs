/**
 * One-time / maintenance: sync denormalized Customer/Partner fields on `tasks`
 * from current `hubs` + `customers` (same as useFirstMileTask / useLineHaulTask buildHubLinkFields).
 *
 * Use after changing task sourceHub/destination, or after setting linkedCustomerId on hubs.
 *
 * Dev dry-run — ต้องใช้ service account ที่อ่าน/เขียน Firestore Dev ได้ (User login ธรรมดาไม่พอสำหรับ Admin SDK)
 *   (PowerShell)
 *   $env:GOOGLE_APPLICATION_CREDENTIALS="D:\real\path\dev-sa.json"   # พาธจริงถึงไฟล์ .json ห้ามใส่ ... เป็นตัวอย่าง
 *   $env:FIREBASE_PROJECT_ID="logi-track-wrt-dev"
 *   node scripts/backfill-task-hub-linked-customers.mjs --dry-run
 *   หรือจากโฟลเดอร์ logitrack-web: pnpm run backfill:task-hub-customers:dry-run
 *   หรือจาก root โมโนรีโป: pnpm run backfill:task-hub-customers:dry-run
 *
 * Dev apply (หลัง dry-run ผ่าน):
 *   node scripts/backfill-task-hub-linked-customers.mjs
 *   (หรือ pnpm คำสั่งเดียวกันข้างบน แต่ ...:apply)
 *
 * Optional:
 *   --dry-run              no writes
 *   --verbose              log updates, skips (missing hub), unchanged
 *   --limit=N              stop after N task updates (or would-updates)
 *   --task-id=<id>         only tasks/{id}
 *   --only-if-missing      only fill sides where *_LinkedCustomerId is empty (no reconcile)
 *
 * SPX suffix: ถ้า source_id / doc id ลงท้ายด้วย -SPX / _SPX หรือเป็น SPX ใช้รหัสลูกค้า "SPX" ใน task
 * (และถ้า hub ยังไม่มี linkedCustomerId จะจับคู่ customer ที่ code == "SPX" ถ้ามีใน Firestore)
 */

import { initializeApp, cert, applicationDefault } from "firebase-admin/app";
import { getFirestore, FieldValue, FieldPath } from "firebase-admin/firestore";
import { existsSync, readFileSync } from "fs";

const SOC_KEYS = ["SOCE", "SOCN", "SOCW"];

const dryRun = process.argv.includes("--dry-run");
const verbose = process.argv.includes("--verbose");
const onlyIfMissing = process.argv.includes("--only-if-missing");
const limitArg = process.argv.find((a) => a.startsWith("--limit="));
const updateLimit = limitArg ? Number.parseInt(limitArg.split("=")[1], 10) : null;
const taskIdArg = process.argv.find((a) => a.startsWith("--task-id="));
const singleTaskId = taskIdArg ? taskIdArg.slice("--task-id=".length) : null;

function initAdmin() {
    const keyPathRaw = process.env.GOOGLE_APPLICATION_CREDENTIALS;
    const keyPath = keyPathRaw
        ? String(keyPathRaw)
              .trim()
              .replace(/^["']|["']$/g, "")
        : "";
    let projectId = process.env.FIREBASE_PROJECT_ID || process.env.GCLOUD_PROJECT;
    if (keyPath) {
        const badPlaceholder = keyPath === "..." || keyPath === "." || keyPath === "..";
        if (badPlaceholder || !existsSync(keyPath)) {
            console.error(
                "GOOGLE_APPLICATION_CREDENTIALS ไม่ชี้ไฟล์ Service Account ที่มีจริง:\n" +
                    `  ได้รับ: ${keyPath}\n` +
                    "แก้ไข: ใส่พาธเต็มไปยังไฟล์ .json (ดาวน์โหลดจาก Firebase Console → Project settings → Service accounts)\n" +
                    "ห้ามใส่ข้อความตัวอย่างเป็นแค่จุด (…) — ถ้าไม่ใช้ไฟล์ JSON ให้ลบ env นี้แล้วใช้ gcloud auth application-default login + FIREBASE_PROJECT_ID",
            );
            process.exit(1);
        }
        const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
        projectId = projectId || serviceAccount.project_id;
        initializeApp({ credential: cert(serviceAccount), projectId });
    } else {
        if (!projectId) {
            console.error(
                "Missing project: set FIREBASE_PROJECT_ID or GOOGLE_APPLICATION_CREDENTIALS with a service account JSON.",
            );
            process.exit(1);
        }
        initializeApp({ credential: applicationDefault(), projectId });
    }
    console.log(
        `Firestore project: ${projectId}${dryRun ? " (dry-run — no writes)" : " (APPLY — will write to tasks)"}`,
    );
}

/** @param {string} sourceId */
function normalizeSocIdToKey(sourceId) {
    const u = String(sourceId ?? "").trim().toUpperCase();
    for (const key of SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "(")) return key;
    }
    return String(sourceId ?? "").trim();
}

/** @param {string} socId @param {string} key */
function socIdMatchesKey(socId, key) {
    const u = String(socId ?? "").trim().toUpperCase();
    const k = String(key ?? "").trim().toUpperCase();
    if (!k) return false;
    return u === k || u.startsWith(k + " ") || u.startsWith(k + "(");
}

/**
 * @param {string} taskCode
 * @param {Map<string, Record<string, unknown> & { _docId: string }>} hubByKey
 * @param {Array<Record<string, unknown> & { _docId: string }>} hubList
 */
function findHubForTaskCode(taskCode, hubByKey, hubList) {
    const t = String(taskCode ?? "").trim();
    if (!t) return null;
    if (hubByKey.has(t)) return hubByKey.get(t);
    const norm = normalizeSocIdToKey(t);
    if (norm && norm !== t && hubByKey.has(norm)) return hubByKey.get(norm);
    for (const hub of hubList) {
        const sid = String(hub.source_id ?? hub._docId ?? "").trim();
        if (t === sid || socIdMatchesKey(t, sid)) return hub;
    }
    return null;
}

/** @param {unknown} v */
function strOrEmpty(v) {
    if (v == null) return "";
    return String(v).trim();
}

/** รหัสสถานีลงท้าย SPX (เช่น ALANG-SPX) — ใช้รหัสลูกค้า SPX โดยต้อง */
function hubSourceIdHasSpxSuffix(hub) {
    if (!hub) return false;
    const s = String(hub.source_id ?? hub._docId ?? "").trim().toUpperCase();
    if (!s) return false;
    if (s === "SPX") return true;
    return s.endsWith("-SPX") || s.endsWith("_SPX") || s.endsWith(".SPX");
}

/**
 * @typedef {{ id: string; name: string; code: string; kind: string } | null} SideSnap
 */

/**
 * @param {Record<string, unknown> | null | undefined} hub
 * @param {Record<string, unknown> | null | undefined} customer
 * @param {{ id: string; name?: string } | null} spxCustomer resolved customers where code == "SPX" (optional)
 * @returns {SideSnap}
 */
function expectedSideSnapshot(hub, customer, spxCustomer) {
    const spx = hubSourceIdHasSpxSuffix(hub);
    const linkedId = strOrEmpty(hub?.linkedCustomerId);

    if (linkedId) {
        const code = spx ? "SPX" : strOrEmpty(customer?.code);
        return {
            id: linkedId,
            name: strOrEmpty(customer?.name),
            code,
            kind: strOrEmpty(hub?.customerLinkKind) || "customer",
        };
    }

    if (spx && spxCustomer) {
        return {
            id: spxCustomer.id,
            name: strOrEmpty(spxCustomer.name),
            code: "SPX",
            kind: "customer",
        };
    }

    return null;
}

/**
 * @param {Record<string, unknown>} taskData
 * @param {"source" | "dest"} side
 * @returns {{ id: string; name: string; code: string; kind: string }}
 */
function currentSideSnapshot(taskData, side) {
    if (side === "source") {
        return {
            id: strOrEmpty(taskData.sourceHubLinkedCustomerId),
            name: strOrEmpty(taskData.sourceHubLinkedCustomerName),
            code: strOrEmpty(taskData.sourceHubLinkedCustomerCode),
            kind: strOrEmpty(taskData.sourceHubCustomerLinkKind),
        };
    }
    return {
        id: strOrEmpty(taskData.destinationLinkedCustomerId),
        name: strOrEmpty(taskData.destinationLinkedCustomerName),
        code: strOrEmpty(taskData.destinationLinkedCustomerCode),
        kind: strOrEmpty(taskData.destinationCustomerLinkKind),
    };
}

/**
 * @param {SideSnap} exp
 * @param {{ id: string; name: string; code: string; kind: string }} cur
 */
function sideMismatch(exp, cur) {
    if (exp === null) {
        return !!(cur.id || cur.name || cur.code || cur.kind);
    }
    return cur.id !== exp.id || cur.name !== exp.name || cur.code !== exp.code || cur.kind !== exp.kind;
}

/**
 * @param {{ hubFound: boolean; snapshot: SideSnap }} src
 * @param {{ hubFound: boolean; snapshot: SideSnap }} dst
 */
function buildUpdatePayload(src, dst) {
    /** @type {Record<string, unknown>} */
    const u = {
        updatedAt: FieldValue.serverTimestamp(),
    };
    if (src.hubFound) {
        if (src.snapshot === null) {
            u.sourceHubLinkedCustomerId = FieldValue.delete();
            u.sourceHubLinkedCustomerName = FieldValue.delete();
            u.sourceHubLinkedCustomerCode = FieldValue.delete();
            u.sourceHubCustomerLinkKind = FieldValue.delete();
        } else {
            u.sourceHubLinkedCustomerId = src.snapshot.id;
            u.sourceHubLinkedCustomerName = src.snapshot.name;
            u.sourceHubLinkedCustomerCode = src.snapshot.code;
            u.sourceHubCustomerLinkKind = src.snapshot.kind;
        }
    }
    if (dst.hubFound) {
        if (dst.snapshot === null) {
            u.destinationLinkedCustomerId = FieldValue.delete();
            u.destinationLinkedCustomerName = FieldValue.delete();
            u.destinationLinkedCustomerCode = FieldValue.delete();
            u.destinationCustomerLinkKind = FieldValue.delete();
        } else {
            u.destinationLinkedCustomerId = dst.snapshot.id;
            u.destinationLinkedCustomerName = dst.snapshot.name;
            u.destinationLinkedCustomerCode = dst.snapshot.code;
            u.destinationCustomerLinkKind = dst.snapshot.kind;
        }
    }
    return u;
}

/**
 * @param {Record<string, unknown>} taskData
 * @param {{ hubFound: boolean; snapshot: SideSnap }} src
 * @param {{ hubFound: boolean; snapshot: SideSnap }} dst
 */
function shouldUpdateTask(taskData, src, dst) {
    const curSrc = currentSideSnapshot(taskData, "source");
    const curDst = currentSideSnapshot(taskData, "dest");

    if (!src.hubFound && !dst.hubFound) return false;

    if (onlyIfMissing) {
        let need = false;
        if (src.hubFound && !curSrc.id && src.snapshot !== null) need = true;
        if (dst.hubFound && !curDst.id && dst.snapshot !== null) need = true;
        // suffix SPX: ยังเติมถ้า code ยังไม่ใช่ SPX
        if (src.hubFound && src.snapshot && curSrc.id && curSrc.code !== "SPX" && src.snapshot.code === "SPX") {
            need = true;
        }
        if (dst.hubFound && dst.snapshot && curDst.id && curDst.code !== "SPX" && dst.snapshot.code === "SPX") {
            need = true;
        }
        return need;
    }

    let mismatch = false;
    if (src.hubFound) mismatch = mismatch || sideMismatch(src.snapshot, curSrc);
    if (dst.hubFound) mismatch = mismatch || sideMismatch(dst.snapshot, curDst);
    return mismatch;
}

async function main() {
    initAdmin();
    const db = getFirestore();

    /** @type {Map<string, Record<string, unknown> & { _docId: string }>} */
    const hubByKey = new Map();
    /** @type {Array<Record<string, unknown> & { _docId: string }>} */
    const hubList = [];
    const hubsSnap = await db.collection("hubs").get();
    for (const d of hubsSnap.docs) {
        const data = d.data();
        const row = { ...data, _docId: d.id };
        hubList.push(row);
        const sid = String(data.source_id ?? d.id ?? "").trim();
        if (sid) hubByKey.set(sid, row);
        hubByKey.set(d.id, row);
    }

    /** @type {Map<string, Record<string, unknown> | null>} */
    const customersById = new Map();

    /** @type {{ id: string; name?: string } | null} */
    let spxCustomerDoc = null;
    const spxQ = await db.collection("customers").where("code", "==", "SPX").limit(1).get();
    if (!spxQ.empty) {
        const d = spxQ.docs[0];
        const dat = d.data() ?? {};
        spxCustomerDoc = { id: d.id, name: strOrEmpty(dat.name) };
        console.log(`Resolved customers code=SPX -> id=${d.id}`);
    } else {
        console.log("No customer with code=SPX; hubs with -SPX suffix will only get SPX code when linkedCustomerId is set.");
    }

    /**
     * @param {string} customerId
     */
    async function getCustomer(customerId) {
        const id = strOrEmpty(customerId);
        if (!id) return null;
        if (customersById.has(id)) return customersById.get(id);
        const snap = await db.collection("customers").doc(id).get();
        if (!snap.exists) {
            customersById.set(id, null);
            return null;
        }
        const data = snap.data() ?? {};
        customersById.set(id, data);
        return data;
    }

    let scanned = 0;
    let updated = 0;
    /** @type {import("firebase-admin/firestore").DocumentSnapshot | null} */
    let lastDoc = null;
    let stoppedByLimit = false;

    /**
     * @param {import("firebase-admin/firestore").QueryDocumentSnapshot} doc
     */
    async function processTaskDoc(doc) {
        scanned++;
        const data = doc.data() ?? {};
        const sourceHub = strOrEmpty(data.sourceHub);
        const destination = strOrEmpty(data.destination);
        if (!sourceHub || !destination) {
            if (verbose) console.log(`[skip] ${doc.id}: missing sourceHub or destination`);
            return;
        }

        const srcHubRow = findHubForTaskCode(sourceHub, hubByKey, hubList);
        const dstHubRow = findHubForTaskCode(destination, hubByKey, hubList);
        if (!srcHubRow && verbose) {
            console.warn(`[warn] ${doc.id}: no hub match for sourceHub=${JSON.stringify(sourceHub)}`);
        }
        if (!dstHubRow && verbose) {
            console.warn(`[warn] ${doc.id}: no hub match for destination=${JSON.stringify(destination)}`);
        }

        const srcLinked = strOrEmpty(srcHubRow?.linkedCustomerId);
        const dstLinked = strOrEmpty(dstHubRow?.linkedCustomerId);

        const srcCustomer = srcLinked ? await getCustomer(srcLinked) : null;
        const dstCustomer = dstLinked ? await getCustomer(dstLinked) : null;

        const srcSide = {
            hubFound: !!srcHubRow,
            snapshot: srcHubRow ? expectedSideSnapshot(srcHubRow, srcCustomer ?? undefined, spxCustomerDoc) : null,
        };
        const dstSide = {
            hubFound: !!dstHubRow,
            snapshot: dstHubRow ? expectedSideSnapshot(dstHubRow, dstCustomer ?? undefined, spxCustomerDoc) : null,
        };

        if (!shouldUpdateTask(data, srcSide, dstSide)) {
            if (verbose) console.log(`[unchanged] ${doc.id}`);
            return;
        }

        const payload = buildUpdatePayload(srcSide, dstSide);
        if (dryRun) {
            console.log(`[dry-run] would update ${doc.id} sourceHub=${sourceHub} destination=${destination}`);
            if (verbose) {
                console.log(
                    `         source: hubFound=${srcSide.hubFound} ${srcSide.snapshot ? JSON.stringify(srcSide.snapshot) : srcSide.hubFound ? "(clear)" : "(skip)"}`,
                );
                console.log(
                    `         dest:   hubFound=${dstSide.hubFound} ${dstSide.snapshot ? JSON.stringify(dstSide.snapshot) : dstSide.hubFound ? "(clear)" : "(skip)"}`,
                );
            }
            updated++;
        } else {
            await doc.ref.update(payload);
            updated++;
            if (verbose) console.log(`[updated] ${doc.id}`);
        }

        if (updateLimit != null && !Number.isNaN(updateLimit) && updated >= updateLimit) {
            stoppedByLimit = true;
        }
    }

    if (singleTaskId) {
        const ref = db.collection("tasks").doc(singleTaskId);
        const snap = await ref.get();
        if (!snap.exists) {
            console.error(`No document: tasks/${singleTaskId}`);
            process.exit(1);
        }
        await processTaskDoc(/** @type {import("firebase-admin/firestore").QueryDocumentSnapshot} */ (snap));
        console.log(
            dryRun
                ? `Dry-run complete. scanned=${scanned} would_update=${updated}`
                : `Done. scanned=${scanned} updated=${updated}`,
        );
        return;
    }

     
    outer: while (true) {
        if (stoppedByLimit) break;
        let q = db.collection("tasks").orderBy(FieldPath.documentId()).limit(300);
        if (lastDoc) q = q.startAfter(lastDoc);
        const snap = await q.get();
        if (snap.empty) break;

        for (const doc of snap.docs) {
            await processTaskDoc(doc);
            lastDoc = doc;
            if (stoppedByLimit) break outer;
        }

        if (snap.size < 300) break;
    }

    if (stoppedByLimit && updateLimit != null) {
        console.log(`Stopped at --limit=${updateLimit}.`);
    }
    console.log(
        dryRun
            ? `Dry-run complete. scanned=${scanned} would_update=${updated}`
            : `Done. scanned=${scanned} updated=${updated}`,
    );
}

main().catch((e) => {
    console.error(e);
    process.exit(1);
});
