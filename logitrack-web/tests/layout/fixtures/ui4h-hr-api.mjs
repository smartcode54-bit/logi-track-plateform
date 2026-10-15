// Deterministic, read-only browser fixtures for UI4H, bound only to the task's stub port.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4h", alg: "EdDSA", use: "sig" };
const tenant = { id: "ui4h", nameTh: "บริษัทขนส่งทดสอบ", nameEn: "Test transport", kind: "own_fleet", role: "tenant_admin" };
const token = await new SignJWT({ sid: "ui4h", ver: 1, tid: tenant.id, rol: tenant.role, plt: [] })
    .setProtectedHeader({ alg: "EdDSA", kid: "ui4h" }).setSubject("ui4h")
    .setIssuer("ui4h").setAudience("ui4h-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
const now = new Date();
const year = now.getFullYear();
const month = `${year}-${String(now.getMonth() + 1).padStart(2, "0")}`;
const day = `${month}-15`;
const at = `${day}T02:00:00Z`;
const common = { id: "fixture-1", tenantId: tenant.id, createdAt: at, updatedAt: at };
const maintenance = ["pm_booking", "scheduled", "in_progress", "completed"].map((status, i) => ({
    ...common, id: `maintenance-${i}`, truckId: "truck-1", licensePlate: "กข-1234", truckBrand: "Isuzu", truckModel: null,
    truckOwnership: "own", truckStatus: "maintenance", type: i % 2 ? "CM" : "PM", serviceType: "periodic_check",
    startDate: day, endDate: null, status, appointmentTime: "10:00", pickupAppointmentAt: null,
    costLaborThb: 1000, costPartsThb: 2000, totalCostThb: 3000, invoiceAmountThb: null, displayCostThb: 3000,
    provider: "อู่ทดสอบ", providerLat: null, providerLng: null, paymentMethod: "cash", currentMileage: 100000,
    nextServiceMileage: 110000, driverSubmitted: false, checkInAt: null, checkOutAt: null, notes: null,
    files: { images: [], receipts: [], invoices: [] }, createdBy: null, updatedBy: null,
}));
const payroll = ["draft", "pending_approval", "approved", "paid", "cancelled"].map((status, i) => ({
    ...common, id: `payroll-${i}`, driverId: "driver-1", driverName: "สมหญิง คนขับทดสอบ", payPeriod: month, round: "R2",
    periodStart: at, periodEnd: at, status, totalEarningsThb: 20000, totalDeductionsThb: 750, netPayThb: 19250,
    currency: "THB", paymentDate: null, paymentMethod: null, remarks: null, approvedBy: null, approvedAt: null, ledgerTransactionId: null,
}));
const leave = ["pending", "approved", "rejected", "cancelled"].map((status, i) => ({
    ...common, id: `leave-${i}`, driverId: "driver-1", driverName: "สมหญิง คนขับทดสอบ", type: "sick", status,
    startDate: day, endDate: day, days: 1, reason: "พักรักษาตัว", approverUserId: null, decidedAt: null,
    rejectionReason: status === "rejected" ? "ตรวจสอบเอกสาร" : null, attachments: [],
}));
const standby = [{ ...common, driverId: "driver-1", driverName: "สมหญิง คนขับทดสอบ", taskId: null, tripId: null,
    jobCategory: "PRIMARY", startLocation: "จุดรับสินค้า", endLocation: "ศูนย์กระจายสินค้า", startedAt: at, endedAt: at,
    durationMinutes: 60, note: "รอรับงาน", status: "completed", lat: null, lng: null, truckId: "truck-1", truckLicensePlate: "กข-1234", photos: [] }];
const holidays = ["public", "company", "other"].map((type, i) => ({
    ...common, id: `holiday-${i}`, tenantId: type === "public" ? null : tenant.id,
    date: `${month}-${String(15 + i).padStart(2, "0")}`, type, status: "published", name: "Holiday (วันหยุด)",
    nameEn: ["Public holiday", "Company holiday", "Other holiday"][i], nameTh: ["วันหยุดราชการ", "วันหยุดบริษัท", "วันหยุดอื่น"][i],
    description: null, descriptionEn: null, descriptionTh: null, isRecurring: false, createdBy: null,
}));
const counts = (rows) => Object.fromEntries(rows.map((r) => [r.status, 1]));

http.createServer((req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3922");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui4h/session") return send(200, { token });
    if (url.pathname === "/ui4h/browser-session") {
        res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://127.0.0.1:3921/app/maintenance" });
        return res.end();
    }
    if (req.headers.authorization !== `Bearer ${token}`) return send(401, { error: { code: "unauthenticated" } });
    if (req.method !== "GET") return send(405, { error: { code: "method_not_allowed" } });
    if (url.pathname === "/v1/me") return send(200, { data: { id: "ui4h", email: "hr@fixture.test", displayName: "HR fixture", photoUrl: null,
        tenant, tenants: [tenant], platformRoles: [], customerScopes: [], capabilities: catalog.CAPABILITY_KEYS,
        dispatcher: false, steward: true, driver: null, mustChangePassword: false } });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: [tenant] });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        return res.write(": ui4h fixture\n\n");
    }
    if (url.pathname === "/v1/maintenance") {
        const status = url.searchParams.get("status");
        const type = url.searchParams.get("type");
        return send(200, { data: maintenance.filter((r) => (!status || r.status === status) && (!type || r.type === type)), meta: { statusCounts: counts(maintenance) } });
    }
    if (url.pathname === "/v1/payroll") return send(200, { data: payroll, meta: { statusCounts: counts(payroll) } });
    if (url.pathname === "/v1/leave-requests") return send(200, { data: leave, meta: { statusCounts: counts(leave) } });
    if (url.pathname === "/v1/standby") return send(200, { data: standby });
    if (url.pathname === "/v1/holidays") return send(200, { data: holidays.filter((r) => r.date.startsWith(url.searchParams.get("year") ?? "")) });
    return send(200, { data: [] });
}).listen(3922, "127.0.0.1", () => console.log("UI4H stub API ready on 3922"));
