// Local-only production-build fixture for UI4K3. Never connects to a database.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4k3", alg: "EdDSA", use: "sig" };
const tenant = { id: "own-1", nameTh: "บริษัทขนส่งทดสอบ", nameEn: "Fleet fixture", kind: "own_fleet", role: "tenant_admin" };
const me = { id: "ui4k3", email: "fleet@fixture.test", displayName: "Fleet fixture", photoUrl: null, tenant, tenants: [tenant], platformRoles: [], dispatcher: false, steward: true, driver: null, customerScopes: [], capabilities: catalog.CAPABILITY_KEYS, mustChangePassword: false };
const truck = { id: "truck-1", licensePlate: "70-1234", brand: "Isuzu", model: "FRR", status: "active", currentMileage: 120000, pmIntervalKm: 20000 };
function initialRecords() {
    return [
        ["cost-only", "in_progress", "Total-only repair", "CM"],
        ["cancelled", "cancelled", "Cancelled repair", "CM"],
        ["booking", "pm_booking", "periodic_check", "PM"],
        ["scheduled", "scheduled", "tire_change", "PM"],
    ].map(([id, status, serviceType, type]) => ({
        id, tenantId: tenant.id, truckId: truck.id, licensePlate: truck.licensePlate, truckBrand: truck.brand, truckModel: truck.model, truckOwnership: "own", truckStatus: truck.status,
        type, serviceType, startDate: "2026-10-15", endDate: null, status, appointmentTime: status === "scheduled" ? "09:30" : null, pickupAppointmentAt: null,
        costLaborThb: null, costPartsThb: null, totalCostThb: id === "cost-only" ? 3500 : null, invoiceAmountThb: null, displayCostThb: id === "cost-only" ? 3500 : 0,
        provider: "อู่ทดสอบ", providerLat: 13.7, providerLng: 100.5, paymentMethod: "cash", currentMileage: 120000, nextServiceMileage: null,
        driverSubmitted: false, checkInAt: null, checkOutAt: null, notes: "", files: { images: [], receipts: [], invoices: id === "cost-only" ? Array.from({ length: 20 }, (_, i) => `maintenance/invoice-${i}.pdf`) : [] }, createdAt: "2026-10-15T00:00:00Z", updatedAt: "2026-10-15T00:00:00Z",
    }));
}
const token = () => new SignJWT({ sid: "ui4k3-session", ver: 1, tid: tenant.id, rol: tenant.role }).setProtectedHeader({ alg: "EdDSA", kid: "ui4k3" }).setSubject(me.id).setIssuer("ui4k3").setAudience("ui4k3-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
let records = initialRecords();
let failures = false;
let calls = [];
const transitions = { pm_booking: ["scheduled", "in_progress", "completed", "cancelled"], scheduled: ["pm_booking", "in_progress", "completed", "cancelled"], in_progress: ["completed", "cancelled"] };
const bodyOf = async (req) => { let body = ""; for await (const chunk of req) body += chunk; return body ? JSON.parse(body) : {}; };
http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3942");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui4k3/session") {
        return send(200, { token: await token() });
    }
    if (url.pathname === "/ui4k3/state") {
        if (req.method === "POST") { const body = await bodyOf(req); failures = body.failures ?? false; if (body.reset) { records = initialRecords(); calls = []; } }
        return send(200, { calls, records, failures });
    }
    if (url.pathname === "/media/maintenance.pdf") {
        res.writeHead(200, { "Content-Type": "application/pdf" });
        return res.end("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF");
    }
    if (url.pathname === "/v1/auth/login") return send(200, { data: { accessToken: await token(), refreshToken: "ui4k3-fixture-refresh", expiresIn: 7200 } });
    if (!req.headers.authorization) return send(401, { error: { code: "unauthenticated" } });
    if (url.pathname === "/v1/me") return send(200, { data: me });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: [tenant] });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/trucks/truck-1") return send(200, { data: truck });
    if (url.pathname === "/v1/trucks") return send(200, { data: [truck] });
    if (url.pathname.startsWith("/v1/maintenance")) {
        calls.push({ method: req.method, path: req.url });
        if (req.method === "GET") {
            if (failures) return send(503, { error: { code: "unavailable", requestId: "fixture-error" } });
            const status = url.searchParams.get("status");
            const type = url.searchParams.get("type");
            return send(200, { data: records.filter((r) => (!status || r.status === status) && (!type || r.type === type)), meta: { statusCounts: Object.fromEntries(["pm_booking", "scheduled", "in_progress", "completed", "cancelled"].map((s) => [s, records.filter((r) => r.status === s).length])) } });
        }
        const input = await bodyOf(req);
        calls[calls.length - 1].body = input;
        if ((input.startDate && !/^\d{4}-\d{2}-\d{2}$/.test(input.startDate)) || (input.endDate && !/^\d{4}-\d{2}-\d{2}$/.test(input.endDate))) return send(422, { error: { code: "invalid_argument" } });
        if (req.method === "POST") {
            const created = { ...initialRecords()[0], ...input, id: `new-${records.length}`, files: { images: [], receipts: [], invoices: [] } };
            records.unshift(created);
            return send(201, { data: created });
        }
        const record = records.find((r) => r.id === url.pathname.split("/").pop());
        if (!record) return send(404, { error: { code: "not_found" } });
        if (input.status && record.status !== input.status && !transitions[record.status]?.includes(input.status)) return send(409, { error: { code: "failed_precondition" } });
        Object.assign(record, input);
        record.displayCostThb = record.totalCostThb ?? (record.costLaborThb ?? 0) + (record.costPartsThb ?? 0);
        return send(200, { data: record });
    }
    if (url.pathname === "/v1/files") { res.writeHead(302, { Location: "http://127.0.0.1:3942/media/maintenance.pdf" }); return res.end(); }
    if (url.pathname === "/v1/events") { res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }); return res.write(": fixture\n\n"); }
    return send(200, { data: [] });
}).listen(3942, "127.0.0.1", () => console.log("UI4K3 stub API ready on 3942"));
