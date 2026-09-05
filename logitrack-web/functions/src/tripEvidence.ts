/**
 * Public read-only evidence gallery for a delivered trip.
 *
 * The customer opens this from the LINE job-complete message's "ดูรูปหลักฐาน" button. Access is gated
 * by an unguessable per-trip token (trip_records.evidenceToken) — no login needed, which suits a
 * multi-person customer group. Photos are read server-side via the Admin SDK, so Firestore/Storage are
 * never opened for broad public reads; the page just embeds the existing Storage download URLs.
 *
 * Region asia-southeast1. Public HTTP function (App Check does not apply to onRequest); the token is
 * the access control.
 */

import * as admin from "firebase-admin";
import { onRequest } from "firebase-functions/v2/https";

const COL_TRIP_RECORDS = "trip_records";
const COL_STANDBY_RECORDS = "standby_records";
const COL_INCIDENT_REPORTS = "incidentReport";

interface GalleryPhoto {
    url: string;
    label: string;
}

const PHOTO_LABEL: Record<string, string> = {
    // loading phase
    pre_close: "ก่อนปิดตู้",
    closing: "ปิดตู้",
    seal: "ซีล",
    runsheet: "ใบคุมรถ",
    runsheet_extra_1: "ใบคุมรถ (เพิ่ม)",
    runsheet_extra_2: "ใบคุมรถ (เพิ่ม)",
    runsheet_extra_3: "ใบคุมรถ (เพิ่ม)",
    truck_release: "ปล่อยรถ",
    checkin_app: "เช็คอิน (แอปลูกค้า)",
    // delivery phase
    pre_open: "ก่อนเปิดตู้",
    opening: "เปิดตู้",
    empty_container: "ตู้เปล่า",
    runsheet_received: "ใบคุมรถ (รับ)",
    arrived: "ถึงปลายทาง",
    // standby
    customer_worksheet: "ใบงานลูกค้า",
    site_photo: "รูปหน้างาน",
};

function labelFor(type: string): string {
    return PHOTO_LABEL[type] || type || "รูป";
}

function collectPhotos(trip: Record<string, unknown>): GalleryPhoto[] {
    const out: GalleryPhoto[] = [];
    const push = (arr: unknown) => {
        if (!Array.isArray(arr)) return;
        for (const p of arr) {
            const rec = p as Record<string, unknown>;
            const url = typeof rec?.url === "string" ? rec.url : "";
            const type = typeof rec?.type === "string" ? rec.type : "";
            if (url) out.push({ url, label: labelFor(type) });
        }
    };
    push(trip.photos);
    const stops = trip.deliveryStopsProgress;
    if (Array.isArray(stops)) for (const s of stops) push((s as Record<string, unknown>)?.photos);
    return out;
}

function esc(s: unknown): string {
    return String(s ?? "").replace(/[&<>"']/g, (c) =>
        ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c] as string)
    );
}

function page(title: string, bodyHtml: string): string {
    return `<!doctype html><html lang="th"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${esc(title)}</title>
<style>
:root{color-scheme:light dark}
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Noto Sans Thai",sans-serif;background:#f2f2f2;color:#111}
@media(prefers-color-scheme:dark){body{background:#111;color:#eee}.card{background:#1c1c1c;border-color:#333}.sub{color:#aaa}.cap{color:#aaa}}
.wrap{max-width:720px;margin:0 auto;padding:16px}
.card{background:#fff;border:1px solid #e5e5e5;border-radius:12px;padding:14px 16px;margin-bottom:14px}
h1{font-size:18px;margin:0 0 4px}
.sub{color:#666;font-size:13px;margin:0}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:8px}
.ph{display:block;border-radius:8px;overflow:hidden;background:#ddd;border:1px solid #e0e0e0;text-decoration:none}
.ph img{width:100%;height:150px;object-fit:cover;display:block}
.cap{font-size:11px;color:#666;padding:5px 8px}
.empty{color:#888;font-size:14px;padding:24px;text-align:center}
</style></head><body><div class="wrap">${bodyHtml}</div></body></html>`;
}

/** Incident/delay photos on a trip's incident reports, grouped under "เหตุล่าช้า". */
function collectIncidentPhotos(incidents: Array<Record<string, unknown>>): GalleryPhoto[] {
    const out: GalleryPhoto[] = [];
    for (const r of incidents) {
        const add = (field: string, label: string) => {
            const url = typeof r[field] === "string" ? (r[field] as string) : "";
            if (url) out.push({ url, label });
        };
        add("mapPhotoUrl", "เหตุล่าช้า — แผนที่/พิกัด");
        add("situation1PhotoUrl", "เหตุล่าช้า — สถานการณ์");
        add("situation2PhotoUrl", "เหตุล่าช้า — สถานการณ์");
    }
    return out;
}

/** header card + responsive photo grid. `subtitleHtml` is already escaped by the caller. */
function renderGallery(title: string, subtitleHtml: string, photos: GalleryPhoto[]): string {
    const header = `<div class="card"><h1>${esc(title)}</h1><p class="sub">${subtitleHtml}</p></div>`;
    const grid = photos.length === 0
        ? `<div class="card"><div class="empty">ยังไม่มีรูป</div></div>`
        : `<div class="card"><div class="grid">${photos
            .map((p) => `<a class="ph" href="${esc(p.url)}" target="_blank" rel="noopener"><img src="${esc(p.url)}" loading="lazy" alt="${esc(p.label)}"><div class="cap">${esc(p.label)}</div></a>`)
            .join("")}</div></div>`;
    return header + grid;
}

export const tripEvidence = onRequest(
    { region: "asia-southeast1", memory: "256MiB" },
    async (req, res) => {
        try {
            const fromQuery = typeof req.query.k === "string" ? req.query.k.trim() : "";
            const fromPath = String(req.path ?? "").split("/").filter(Boolean).pop() ?? "";
            const token = fromQuery || fromPath;
            if (!token || token === "tripEvidence") {
                res.status(400).type("html").send(page("ลิงก์ไม่ถูกต้อง",
                    `<div class="card"><div class="empty">ลิงก์ไม่ถูกต้อง</div></div>`));
                return;
            }

            const db = admin.firestore();
            res.set("Cache-Control", "private, max-age=300");

            // Trip token first (delivery + incident photos), then standby token (worksheet + site).
            const tripSnap = await db.collection(COL_TRIP_RECORDS).where("evidenceToken", "==", token).limit(1).get();
            if (!tripSnap.empty) {
                const doc = tripSnap.docs[0];
                const trip = doc.data();
                const incidentSnap = await db.collection(COL_INCIDENT_REPORTS).where("tripId", "==", doc.id).get();
                const incidents = incidentSnap.docs.map((d) => d.data());
                const photos = [...collectPhotos(trip), ...collectIncidentPhotos(incidents)];
                const ocr = trip.ocrData as Record<string, unknown> | undefined;
                const subtitle = `${esc(trip.origin ?? "-")} → ${esc(trip.destination ?? "-")} · เลขทริป ${esc(ocr?.tripId ?? trip.spxTripId ?? doc.id)}`;
                res.status(200).type("html").send(page("รูปหลักฐานการจัดส่ง", renderGallery("รูปหลักฐานการจัดส่ง", subtitle, photos)));
                return;
            }

            const standbySnap = await db.collection(COL_STANDBY_RECORDS).where("evidenceToken", "==", token).limit(1).get();
            if (!standbySnap.empty) {
                const standby = standbySnap.docs[0].data();
                const subtitle = `${esc(standby.startLocation ?? "-")} → ${esc(standby.endLocation ?? "-")} · งานหมด / Standby`;
                res.status(200).type("html").send(page("รูปหลักฐาน Standby", renderGallery("รูปหลักฐาน Standby", subtitle, collectPhotos(standby))));
                return;
            }

            res.status(404).type("html").send(page("ไม่พบข้อมูล",
                `<div class="card"><div class="empty">ไม่พบรูปสำหรับลิงก์นี้ (อาจถูกเพิกถอนแล้ว)</div></div>`));
        } catch {
            res.status(500).type("html").send(page("เกิดข้อผิดพลาด",
                `<div class="card"><div class="empty">เกิดข้อผิดพลาด กรุณาลองใหม่</div></div>`));
        }
    }
);
