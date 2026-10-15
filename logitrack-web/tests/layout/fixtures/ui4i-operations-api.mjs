// Deterministic sessions for the production-build palette acceptance suite, only on the task's stub port.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
new Function("exports", compiled.outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4i", alg: "EdDSA", use: "sig" };
const tenant = { id: "ui4i-tenant", nameTh: "บริษัทขนส่งทดสอบ", nameEn: "Operations fixture", kind: "own_fleet", role: "tenant_admin" };
const me = { id: "ui4i-staff", email: "operations@fixture.test", displayName: "Operations fixture", photoUrl: null,
    tenant, tenants: [tenant], platformRoles: ["platform_admin"], customerScopes: [], capabilities: catalog.CAPABILITY_KEYS,
    dispatcher: false, steward: true, driver: null, mustChangePassword: false };
const now = "2026-10-15T03:00:00Z";
const company = { id: "company-1", tenantId: tenant.id, nameTh: "บริษัทขนส่งทดสอบ", nameEn: "Operations fixture", shortName: "OPS",
    taxId: "1234567890123", branchType: "headquarters", branchNumber: null, address: "Bangkok", phone: "021234567", email: "operations@fixture.test",
    logoKey: null, logoUrl: null, stampKey: null, stampUrl: null, signatureKey: null, signatureUrl: null, signatoryName: "Fixture operator",
    bankName: "Fixture bank", accountNumber: "1234567890", accountName: "Operations fixture", withholdingTaxRate: 3, companyType: "owner", isActive: true,
    maxTrucks: null, maxDrivers: null, createdAt: now, updatedAt: now };
const hub = (id, sourceId, name, stationType = "HUB") => ({ id, sourceId, sourceNameTh: name, sourceNameEn: name,
    latitude: 13.7, longitude: 100.5, stationType, network: "SPX", linkedPartyId: null, linkedCustomerId: null,
    linkedCustomerName: null, linkedCustomerKind: null, createdByDriver: false, updatedAt: now });
const hubs = [hub("h1", "SPX-A", "บางนา"), { ...hub("h2", "SPX-NEW", "จุดรับใหม่"), latitude: null, longitude: null, createdByDriver: true }, hub("h3", "SOCE", "ศูนย์คัดแยกตะวันออก", "SOC")];
const task = (status, i) => ({ id: `task-${i}`, tenantId: tenant.id, taskNo: `FM-15102026-00${i}`, taskType: "first_mile", jobCategory: "PRIMARY", status,
    planAt: now, planDate: "2026-10-15", planTime: "09:30", actualPickupAt: null, sourceHub: "SPX-A", sourceHubId: "h1",
    source: { raw: "SPX-A", key: "code:SPX-A", label: "บางนา", resolved: true }, destination: "SOCE", destinationHubId: null, destinationSocKey: "SOCE",
    destinationPlace: { raw: "SOCE", key: "code:SOCE", label: "SOCE", resolved: true }, truckId: "tr-1", truckType: i % 2 ? "6WH" : "4W",
    licensePlate: "70-1111", driverId: "driver-1", driverName: "สมชาย ใจดี", helperDriverId: null, helperName: null, runOrder: i,
    checkInAt: null, checkInLat: null, checkInLng: null, isMultiDelivery: false, stops: [], trip: null, createdAt: now, updatedAt: now });
const chat = { id: "chat-1", tenantId: tenant.id, legacyDocId: null, driverId: "driver-1", driverName: "สมชาย ใจดี", driverUserId: "driver-user-1",
    driverLegacyUid: null, status: "open", priority: "urgent", assignedAdminUserId: null, assignedAdminName: null, assignedAt: null, closedAt: null,
    lastMessagePreview: "ขอความช่วยเหลือเรื่องจุดรับสินค้า", lastMessageAt: now, lastMessageByUserId: "driver-user-1", lastMessageType: "normal",
    createdAt: now, updatedAt: now, unreadForMe: 2, lastReadAt: null, reads: [], driverLastReadAt: null };

http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3932");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui4i/session" || url.pathname === "/ui4i/browser-session") {
        const token = await new SignJWT({ sid: "ui4i-session", ver: 1, tid: tenant.id, rol: tenant.role, plt: me.platformRoles })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui4i" }).setSubject(me.id)
            .setIssuer("ui4i").setAudience("ui4i-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui4i/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://ui4i.localhost:3931/app/dashboard" });
            return res.end();
        }
        return send(200, { token });
    }
    let subject;
    try { subject = decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub; } catch { /* No fixture session. */ }
    if (subject !== me.id) return send(401, { error: { code: "unauthenticated" } });
    if (url.pathname === "/v1/me") return send(200, { data: me });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: [tenant] });
    if (url.pathname === "/v1/badges") return send(200, { data: { pendingExpenses: 7, unassignedChats: 4, urgentChats: 1 } });
    if (url.pathname === "/v1/dashboard/summary") return send(200, { data: {
        period: { from: url.searchParams.get("from"), to: url.searchParams.get("to"), days: 7 },
        trips: { delivered: 120, parcels: 3450, withIncident: 6, onTimePct: 95, delayPct: 5 },
        activity: { days: ["09", "10", "11", "12", "13", "14", "15"].map((d, i) => ({ date: `2026-10-${d}`, delivered: i * 3 })), total: 63, previousTotal: 50, trendPct: 26 },
        incidents: { inPeriod: 6 }, drivers: { active: 30, onDuty: 12, inactive: 3, total: 45 }, chats: { unassigned: 4, urgent: 1 },
        compliance: { taxDue: 1, insuranceDue: 0, serviceDateDue: 0, serviceKmDue: 1, itemsTotal: 2, items: [
            { truckId: "tr-1", licensePlate: "70-1111", kind: "tax", dueDate: "2026-10-18", daysRemaining: 3 },
            { truckId: "tr-2", licensePlate: "70-2222", kind: "service_km", kmRemaining: -120 } ] },
        recent: [{ type: "delivered", id: "r1", at: now, plate: "70-1111", place: "SOCE" }, { type: "incident", id: "r2", at: now, text: "ยางแตก" }] } });
    if (url.pathname === "/v1/stats/users-by-role") return send(200, { data: { scope: { all: false, tenantId: tenant.id }, total: 20, none: 0,
        tenantRoles: { tenant_admin: 1, manager: 2, operation_staff: 3, operator: 1, user: 1, driver: 12 }, scopes: { customer: 0, dispatcher: 0 }, platformRoles: { platform_admin: 0, support: 0 } } });
    if (url.pathname === "/v1/vehicle-locations") return send(200, { data: [
        { truckId: "tr-1", tenantId: tenant.id, licensePlate: "70-1111", lat: 13.7, lng: 100.5, speedKmh: 40, heading: 90, lastGpsAt: now, updatedAt: now,
            engineOn: true, positionDescription: "บางนา", odometerKm: 1234, fuelLevel: 60, fuelPercentage: 50 },
        { truckId: "tr-2", tenantId: tenant.id, licensePlate: "70-2222", lat: 13.75, lng: 100.55, speedKmh: 0, heading: null, lastGpsAt: now, updatedAt: now, engineOn: false } ] });
    if (url.pathname === "/v1/incidents") return send(200, { data: [{ id: "incident-1", truckId: "tr-1", truckLicensePlate: "70-1111", createdAt: now }] });
    if (url.pathname === "/v1/hubs") return send(200, { data: hubs });
    if (url.pathname === "/v1/distances/meta") return send(200, { data: { lastCalculatedAt: null } });
    if (url.pathname === "/v1/companies/owner") return send(200, { data: company });
    if (url.pathname === "/v1/companies") return send(200, { data: [company, { ...company, id: "company-2", nameTh: "บริษัทคู่ค้าทดสอบ", companyType: "subcontractor", isActive: false }] });
    if (url.pathname === "/v1/tasks") return send(200, { data: ["pending", "assigned", "checked_in", "in_transit", "completed", "cancelled"].map(task) });
    if (url.pathname === "/v1/chats") return send(200, { data: [chat] });
    if (url.pathname === "/v1/chats/chat-1") return send(200, { data: chat });
    if (url.pathname === "/v1/chats/chat-1/messages") return send(200, { data: [] });
    if (url.pathname === "/v1/drivers/driver-1") return send(200, { data: { id: "driver-1", fullNameTh: "สมชาย ใจดี", name: "Somchai", mobile: "0812345678", photoUrl: null, status: "active", assignment: { truckPlate: "70-1111", truckType: "6WH" } } });
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        return res.write(": ui4i fixture\n\n");
    }
    return send(200, { data: [] });
}).listen(3932, "127.0.0.1", () => console.log(`UI4I stub API ready on 3932; PID ${process.pid}`));
