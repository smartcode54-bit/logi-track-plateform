// Local production-build fleet fixtures. Start on 3882 and request /ui4b/session for a test token.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4b", alg: "EdDSA", use: "sig" };
const tenant = { id: "own-1", nameTh: "บริษัทขนส่งทดสอบ", nameEn: "Fleet fixture", kind: "own_fleet", role: "tenant_admin" };
const me = { id: "ui4b", email: "fleet@fixture.test", displayName: "Fleet fixture", photoUrl: null, tenant, tenants: [tenant], platformRoles: [],
    dispatcher: false, steward: true, driver: null, customerScopes: [], capabilities: catalog.CAPABILITY_KEYS, mustChangePassword: false };
const trucks = ["active", "maintenance", "insurance_claim", "inactive", "sold"].map((status, i) => ({
    id: `truck-${i + 1}`, tenantId: i % 2 ? "partner-1" : tenant.id, ownershipType: i % 2 ? "subcontractor" : "own",
    licensePlate: `70-${1234 + i}`, province: "กรุงเทพมหานคร", brand: "Isuzu", model: "FRR", year: 2026, color: "White", type: "6 Wheels", status,
    vin: "TEST123456789", engineNumber: "TEST123", fuelType: "Diesel", seats: 2, engineCapacity: 3000, fuelCapacity: 100, maxLoadWeightKg: 5000,
    taxResponsible: "Fleet fixture", maintenanceResponsible: "Fleet fixture", currentMileage: 10000, nextServiceMileage: i ? 9500 : 11000,
    files: {}, currentAssignments: [], statusHistory: [], createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
}));
const drivers = ["active", "on_duty", "inactive"].map((status, i) => ({
    id: `driver-${i + 1}`, tenantId: tenant.id, firstName: "Somchai", lastName: `Test ${i + 1}`, displayName: `Somchai Test ${i + 1}`, fullNameTh: `สมชาย ทดสอบ ${i + 1}`,
    mobile: "0812345678", email: "driver@fixture.test", status, employmentType: "full_time", truckLicenseId: "TEST123", licenseType: "ท.2",
    birthDate: "1990-01-01", idCard: "1234567890123", customerDriverIds: [], statusHistory: [], currentAssignment: null, login: null,
    profileUrl: null, profileKey: null, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
}));
const state = (i) => ({ expiryDate: "2026-10-01", state: i % 2 ? "expiring_soon" : "overdue", renewalStatus: "pending", daysRemaining: -1, amountThb: 2000, responsible: "Fleet fixture" });
const renewals = trucks.map((truck, i) => ({ ...truck, truckId: truck.id, tax: state(i), insurance: { ...state(i + 1), company: "Fixture insurer" } }));

http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3882");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (["/ui4b/session", "/ui4b/browser-session"].includes(url.pathname)) {
        const token = await new SignJWT({ sid: "ui4b-session", ver: 1, tid: tenant.id, rol: tenant.role })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui4b" }).setSubject(me.id).setIssuer("ui4b").setAudience("ui4b-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui4b/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://127.0.0.1:3881/app/trucks" });
            return res.end();
        }
        return send(200, { token });
    }
    if (!req.headers.authorization) return send(401, { error: { code: "unauthenticated" } });
    if (url.pathname === "/v1/me") return send(200, { data: me });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: [tenant] });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/trucks/renewals") return send(200, { data: renewals });
    if (url.pathname === "/v1/trucks") return send(200, { data: trucks, meta: { statusCounts: { active: 1, maintenance: 1, insurance_claim: 1, inactive: 1, sold: 1 } } });
    if (url.pathname.startsWith("/v1/trucks/")) return send(200, { data: trucks.find((tr) => tr.id === url.pathname.split("/").pop()) });
    if (url.pathname === "/v1/drivers") return send(200, { data: drivers, meta: { statusCounts: { active: 1, on_duty: 1, inactive: 1 } } });
    if (url.pathname.startsWith("/v1/drivers/")) return send(200, { data: drivers.find((d) => d.id === url.pathname.split("/").pop()) });
    if (url.pathname === "/v1/subcontractors") return send(200, { data: [{ id: "partner-1", code: "PARTNER", nameTh: "รถร่วมทดสอบ", nameEn: "Partner fixture", status: "active" }] });
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        return res.write(": ui4b fixture\n\n");
    }
    return send(200, { data: [] });
}).listen(3882, "127.0.0.1", () => console.log("UI4B stub API ready on 3882"));
