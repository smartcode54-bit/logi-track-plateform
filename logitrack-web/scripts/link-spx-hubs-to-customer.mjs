/**
 * ผูกจุดรับส่ง (hubs + SOC) ของเครือข่ายหนึ่ง (SPX หรือ SPK/J&T) เข้ากับลูกค้ารายเดียว
 * เช่น TTP ← จุดของ SPX, JWT ← จุดของ J&T (SPK)
 *
 * ทำไม: ฟอร์มงาน FM/LH ให้เลือกลูกค้าก่อน แล้วแสดงเฉพาะจุดที่ `hubs.linkedCustomerId` = ลูกค้านั้น
 * → จุดที่ยังไม่ผูกจะไม่ขึ้นในรายการ. สคริปต์นี้เขียนฟิลด์เดียวกับ HubDialog
 * (`linkedCustomerId`, `linkedCustomerName` = "CODE — name", `customerLinkKind`, `updatedAt`)
 *
 * กลุ่มเครือข่าย (กฎเดียวกับ hubDistanceNetworkGroup ใน validate/hubSchema.ts):
 *   - source_id ขึ้นต้น SPK → SPK
 *   - ผูกกับลูกค้ารหัส SPK / J&T / JT → SPK
 *   - ที่เหลือ → SPX
 * กลุ่มนี้ใช้คำนวณระยะ Hub↔SOC ด้วย — จุดที่ "กลุ่มจะเปลี่ยน" ถ้าผูกกับลูกค้าเป้าหมาย (เช่น จุด J&T ที่
 * source_id ไม่ขึ้นต้น SPK และเป็น SPK เพราะผูกกับลูกค้า J&T อยู่) จะถูกข้ามและแสดงรายการให้ดูแทน
 *
 * ค่าเริ่มต้นปลอดภัย:
 *   - dry-run เสมอ ถ้าไม่ใส่ --apply
 *   - ผูกเฉพาะจุดที่ "ยังไม่ผูก" (หรือผูกกับ customer doc ที่ถูกลบไปแล้ว)
 *   - จุดที่ผูกกับลูกค้าอื่นอยู่ → แค่แสดงรายการ ไม่เปลี่ยน (ใช้ --relink-from=CODE ถ้าจะย้าย)
 *   - งาน (tasks) เดิมไม่ถูกแก้ — สคริปต์แตะเฉพาะ collection `hubs`
 *
 * ต้องใช้ service account ของโปรเจกต์ปลายทาง (PowerShell):
 *   $env:GOOGLE_APPLICATION_CREDENTIALS="D:\Secret\LOGI-TRACK\<project>-firebase-adminsdk-....json"
 *   node scripts/link-spx-hubs-to-customer.mjs --project=dev --customer=TTP                  # dry-run
 *   node scripts/link-spx-hubs-to-customer.mjs --project=dev --customer=TTP --apply          # เขียนจริง
 *   node scripts/link-spx-hubs-to-customer.mjs --project=dev --customer=JWT --network=SPK    # จุดของ J&T
 *
 * Options:
 *   --project=dev|prod        REQUIRED — ต้องตรงกับ project_id ของ service account
 *   --customer=CODE           ลูกค้าเป้าหมายตามรหัส (เช่น TTP) — หรือใช้ --customer-id=<docId>
 *   --network=SPX|SPK         กลุ่มจุดที่จะผูก (default: SPX)
 *   --apply                   เขียนจริง (ไม่ใส่ = dry-run)
 *   --relink-from=CODE,CODE   ย้ายจุดที่ผูกกับลูกค้ารหัสเหล่านี้มาเป็นลูกค้าเป้าหมายด้วย
 *   --exclude=ID,ID           ข้ามจุดตาม doc id หรือ source_id
 *   --type=HUB|SOC            ทำเฉพาะประเภทสถานี
 *   --kind=customer|partner   customerLinkKind ที่จะเขียน (default: customer)
 *   --out=FILE                บันทึกรายงานลงไฟล์ (UTF-8) ด้วย
 */

import path from "path";
import { existsSync, readFileSync, writeFileSync } from "fs";
import { fileURLToPath } from "url";

const PROJECT_IDS = { dev: "logi-track-wrt-dev", prod: "logitrack-prod" };
const SPK_CUSTOMER_CODES = new Set(["SPK", "J&T", "JT"]);

const str = (v) => (v == null ? "" : String(v).trim());
const upper = (v) => str(v).toUpperCase();
const csv = (v) =>
    str(v)
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);

/** Mirror of hubDistanceNetworkGroup (validate/hubSchema.ts). */
export function hubNetworkGroup(sourceId, linkedCustomerCode) {
    if (upper(sourceId).startsWith("SPK")) return "SPK";
    if (SPK_CUSTOMER_CODES.has(upper(linkedCustomerCode))) return "SPK";
    return "SPX";
}

/** Same label HubDialog writes to `hubs.linkedCustomerName`. */
export function customerLinkLabel(customer) {
    return `${str(customer?.code)} — ${str(customer?.name)}`.trim();
}

/**
 * Decide what happens to each hub. Pure — no Firestore.
 * @param {Array<{ id: string, data: Record<string, unknown> }>} hubs
 * @param {Map<string, Record<string, unknown>>} customersById
 * @param {{ id: string, code?: unknown }} target
 * @param {{ network?: "SPX" | "SPK", relinkFrom?: string[], exclude?: string[], type?: string }} opts
 */
export function planHubLinks(hubs, customersById, target, opts = {}) {
    const network = upper(opts.network) || "SPX";
    const relinkFrom = new Set((opts.relinkFrom ?? []).map(upper));
    const exclude = new Set((opts.exclude ?? []).map(upper));
    const onlyType = upper(opts.type);
    const plan = {
        toLink: [],
        toRelink: [],
        already: [],
        linkedElsewhere: [],
        groupWouldChange: [],
        excluded: [],
        otherNetwork: [],
    };

    for (const { id, data } of hubs) {
        const sourceId = str(data.source_id) || id;
        const type = upper(data.station_type) || "HUB";
        if (onlyType && type !== onlyType) continue;

        const linkedId = str(data.linkedCustomerId);
        const linkedCustomer = linkedId ? customersById.get(linkedId) : undefined;
        const row = {
            id,
            sourceId,
            type,
            nameTh: str(data.source_name_th) || str(data.source_name_en),
            current: linkedId
                ? linkedCustomer
                    ? customerLinkLabel(linkedCustomer)
                    : `(ลูกค้า ${linkedId} ไม่มีในระบบแล้ว)`
                : "",
        };

        if (exclude.has(upper(id)) || exclude.has(upper(sourceId))) plan.excluded.push(row);
        else if (hubNetworkGroup(sourceId, linkedCustomer?.code) !== network) plan.otherNetwork.push(row);
        else if (linkedId && linkedId === target.id) plan.already.push(row);
        else {
            const bucket =
                !linkedId || !linkedCustomer
                    ? plan.toLink
                    : relinkFrom.has(upper(linkedCustomer.code))
                      ? plan.toRelink
                      : plan.linkedElsewhere;
            // Re-linking must not move the point to the other network (it drives Hub↔SOC distances).
            if (bucket !== plan.linkedElsewhere && hubNetworkGroup(sourceId, target.code) !== network) {
                plan.groupWouldChange.push(row);
            } else {
                bucket.push(row);
            }
        }
    }

    for (const list of Object.values(plan)) {
        list.sort((a, b) => (a.type + a.sourceId).localeCompare(b.type + b.sourceId));
    }
    return plan;
}

/** Report lines, mirrored to --out=FILE as UTF-8 (PowerShell `Out-File` mangles Thai). */
const report = [];
function out(line = "") {
    console.log(line);
    report.push(line);
}

function argValue(name) {
    const hit = process.argv.find((a) => a.startsWith(`--${name}=`));
    return hit ? hit.slice(name.length + 3) : undefined;
}

function printRows(title, rows, showCurrent) {
    out(`\n${title}: ${rows.length}`);
    for (const r of rows) {
        const current = showCurrent && r.current ? `   (ตอนนี้: ${r.current})` : "";
        out(`  [${r.type}] ${r.sourceId}  ${r.nameTh}${current}`);
    }
}

async function main() {
    try {
        await run();
    } finally {
        const outPath = argValue("out");
        if (outPath && report.length > 0) {
            writeFileSync(outPath, report.join("\n") + "\n", "utf8");
            console.log(`\nบันทึกรายงานที่ ${path.resolve(outPath)}`);
        }
    }
}

async function run() {
    const apply = process.argv.includes("--apply");
    const projectKey = argValue("project");
    const projectId = PROJECT_IDS[projectKey ?? ""];
    if (!projectId) {
        console.error("ต้องระบุ --project=dev หรือ --project=prod");
        process.exit(1);
    }
    const customerCode = argValue("customer");
    const customerId = argValue("customer-id");
    if (!customerCode && !customerId) {
        console.error("ต้องระบุ --customer=CODE (เช่น TTP) หรือ --customer-id=<docId>");
        process.exit(1);
    }
    const kind = argValue("kind") ?? "customer";
    if (kind !== "customer" && kind !== "partner") {
        console.error("--kind ต้องเป็น customer หรือ partner");
        process.exit(1);
    }
    const type = argValue("type");
    if (type && !["HUB", "SOC"].includes(upper(type))) {
        console.error("--type ต้องเป็น HUB หรือ SOC");
        process.exit(1);
    }
    const network = upper(argValue("network") ?? "SPX");
    if (network !== "SPX" && network !== "SPK") {
        console.error("--network ต้องเป็น SPX หรือ SPK");
        process.exit(1);
    }
    const otherNetwork = network === "SPX" ? "SPK/J&T" : "SPX";

    const keyPath = str(process.env.GOOGLE_APPLICATION_CREDENTIALS).replace(/^["']|["']$/g, "");
    if (!keyPath || !existsSync(keyPath)) {
        console.error(
            "ตั้ง GOOGLE_APPLICATION_CREDENTIALS ให้ชี้ไฟล์ service account (.json) ของโปรเจกต์ " + projectId,
        );
        process.exit(1);
    }
    const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
    if (serviceAccount.project_id !== projectId) {
        console.error(
            `Service account เป็นของ "${serviceAccount.project_id}" แต่ --project=${projectKey} คือ "${projectId}"`,
        );
        process.exit(1);
    }

    const { initializeApp, cert } = await import("firebase-admin/app");
    const { getFirestore } = await import("firebase-admin/firestore");
    initializeApp({ credential: cert(serviceAccount), projectId });
    const db = getFirestore();

    out(`Firestore project: ${projectId}${apply ? " (APPLY — จะเขียน hubs)" : " (dry-run — ไม่เขียน)"}`);

    const customerSnap = await db.collection("customers").get();
    const customersById = new Map(customerSnap.docs.map((d) => [d.id, d.data()]));

    let target;
    if (customerId) {
        const data = customersById.get(customerId);
        if (!data) {
            console.error(`ไม่พบลูกค้า id ${customerId}`);
            process.exit(1);
        }
        target = { id: customerId, ...data };
    } else {
        const matches = customerSnap.docs.filter((d) => upper(d.data().code) === upper(customerCode));
        if (matches.length !== 1) {
            console.error(
                matches.length === 0
                    ? `ไม่พบลูกค้ารหัส ${customerCode}`
                    : `ลูกค้ารหัส ${customerCode} มีหลายรายการ — ใช้ --customer-id แทน:\n` +
                          matches.map((d) => `  ${d.id}  ${customerLinkLabel(d.data())}`).join("\n"),
            );
            process.exit(1);
        }
        target = { id: matches[0].id, ...matches[0].data() };
    }
    const label = customerLinkLabel(target);
    out(`ลูกค้าเป้าหมาย: ${label}  (id ${target.id}, kind ${kind})  กลุ่มจุด: ${network}`);

    const hubSnap = await db.collection("hubs").get();
    const plan = planHubLinks(
        hubSnap.docs.map((d) => ({ id: d.id, data: d.data() })),
        customersById,
        target,
        { network, relinkFrom: csv(argValue("relink-from")), exclude: csv(argValue("exclude")), type },
    );

    printRows("จะผูก (ยังไม่ผูกลูกค้า)", plan.toLink, true);
    printRows("จะย้ายมาผูก (--relink-from)", plan.toRelink, true);
    printRows("ผูกกับลูกค้านี้อยู่แล้ว", plan.already, false);
    printRows("ผูกกับลูกค้าอื่น — ไม่แตะ (ใส่รหัสใน --relink-from ถ้าจะย้าย)", plan.linkedElsewhere, true);
    printRows(
        `ข้าม — ถ้าผูกกับ ${str(target.code)} จุดจะย้ายไปกลุ่ม ${otherNetwork} (กระทบการคำนวณระยะ Hub↔SOC)`,
        plan.groupWouldChange,
        true,
    );
    printRows("ข้ามตาม --exclude", plan.excluded, true);
    out(`\nกลุ่ม ${otherNetwork} (ไม่แตะ): ${plan.otherNetwork.length}`);

    const writes = [...plan.toLink, ...plan.toRelink];
    if (!apply) {
        out(`\nDry-run: จะเขียน ${writes.length} จุด — ตรวจรายการแล้วรันซ้ำพร้อม --apply`);
        return;
    }
    if (writes.length === 0) {
        out("\nไม่มีจุดที่ต้องเขียน");
        return;
    }

    const payload = {
        linkedCustomerId: target.id,
        linkedCustomerName: label,
        customerLinkKind: kind,
        updatedAt: new Date(),
    };
    for (let i = 0; i < writes.length; i += 400) {
        const batch = db.batch();
        for (const row of writes.slice(i, i + 400)) batch.update(db.collection("hubs").doc(row.id), payload);
        await batch.commit();
    }
    out(`\n✔ ผูก ${writes.length} จุดกับ ${label} แล้ว`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    main().catch((err) => {
        console.error(err);
        process.exit(1);
    });
}
