// Exports the evidence gallery golden pages from the legacy function (main spec §9.6, T49).
//
//   node testdata/golden/evidence/export.mjs        (from logitrack-api/, Node >= 22.18)
//
// fn:tripEvidence.ts imports firebase-admin, so the exporter takes its pure part (the photo labels, esc, page,
// collectPhotos, collectIncidentPhotos, renderGallery: everything above `export const tripEvidence`) into a temporary
// module and runs it; the three status pages are the page(title, body) calls of the handler, read from the source.
// internal/storage/gallery_test.go checks the Go pages against pages.json byte for byte.
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const src = readFileSync(new URL("../../../../logitrack-web/functions/src/tripEvidence.ts", import.meta.url), "utf8");
const start = src.indexOf("interface GalleryPhoto");
const end = src.indexOf("export const tripEvidence");
assert.ok(start > 0 && end > start, "tripEvidence.ts: the pure part was not found");

const dir = mkdtempSync(path.join(tmpdir(), "evidence-golden-"));
const mod = path.join(dir, "gallery.ts");
writeFileSync(mod, src.slice(start, end) + "\nexport { PHOTO_LABEL, labelFor, esc, page, collectPhotos, collectIncidentPhotos, renderGallery };\n");
const g = await import(pathToFileURL(mod).href);
rmSync(dir, { recursive: true, force: true });

// The status pages: every page("title", `body`) call of the handler.
const status = {};
for (const m of src.slice(end).matchAll(/res\.status\((\d{3})\)\.type\("html"\)\.send\(page\("([^"]+)",\s*`([^`]+)`\)\)/g)) {
  status[m[1]] = g.page(m[2], m[3]);
}
assert.deepEqual(Object.keys(status).sort(), ["400", "404", "500"]);

// The handler's subtitles (fn:tripEvidence.ts:143, :151): the values escaped, `?? "-"` for absent ones.
const tripSubtitle = (origin, destination, tripNo) => `${g.esc(origin ?? "-")} → ${g.esc(destination ?? "-")} · เลขทริป ${g.esc(tripNo)}`;
const standbySubtitle = (start, end) => `${g.esc(start ?? "-")} → ${g.esc(end ?? "-")} · งานหมด / Standby`;

const trip = {
  photos: [
    { type: "pre_close", url: "https://media.example.test/a.jpg?X-Sig=1&x=2" },
    { type: "stop_2_arrived", url: "https://media.example.test/b.jpg" },
    { type: "", url: "https://media.example.test/c.jpg" },
  ],
  deliveryStopsProgress: [{ photos: [{ type: "arrived", url: "https://media.example.test/d.jpg" }] }],
};
const incidents = [{ mapPhotoUrl: "https://media.example.test/m.jpg", situation1PhotoUrl: "https://media.example.test/s1.jpg" }];
const tripPhotos = [...g.collectPhotos(trip), ...g.collectIncidentPhotos(incidents)];
const standbyPhotos = g.collectPhotos({ photos: [{ type: "customer_worksheet", url: "https://media.example.test/w.jpg" },
  { type: "site_photo", url: "https://media.example.test/s.jpg" }] });

const cases = [
  { name: "trip", title: "รูปหลักฐานการจัดส่ง", subtitle: { origin: "ALANG-A <x>", destination: null, tripNo: "SPX\"1'&" }, photos: tripPhotos },
  { name: "trip-empty", title: "รูปหลักฐานการจัดส่ง", subtitle: { origin: "", destination: "SOCE", tripNo: "T-2" }, photos: [] },
  { name: "standby", title: "รูปหลักฐาน Standby", subtitle: { start: "ลาดพร้าว08", end: null }, photos: standbyPhotos },
].map((c) => {
  const sub = "tripNo" in c.subtitle ? tripSubtitle(c.subtitle.origin, c.subtitle.destination, c.subtitle.tripNo)
    : standbySubtitle(c.subtitle.start, c.subtitle.end);
  return { ...c, html: g.page(c.title, g.renderGallery(c.title, sub, c.photos)) };
});

const labels = Object.fromEntries([...Object.keys(g.PHOTO_LABEL), "stop_1_arrived", ""].map((k) => [k, g.labelFor(k)]));

const out = {
  source: "logitrack-web/functions/src/tripEvidence.ts",
  note: "The legacy gallery's pages: the 400 / 404 / 500 pages, three galleries (photos as collectPhotos and collectIncidentPhotos list them, subtitle parts as the handler reads them: null = absent) and the photo labels. Regenerate with export.mjs; never edit by hand.",
  status,
  cases,
  labels,
};
writeFileSync(new URL("pages.json", import.meta.url), JSON.stringify(out, null, 2) + "\n");
console.log(`pages.json: ${Object.keys(status).length} status pages, ${cases.length} galleries, ${Object.keys(labels).length} labels`);
