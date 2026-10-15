// Local-only production browser fixture. Never connects to a real API.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4d", alg: "EdDSA", use: "sig" };
const tenant = { id: "own_fleet", nameTh: "บริษัทขนส่งตัวอย่าง", nameEn: "Example Transport", kind: "own_fleet", role: "tenant_admin" };
const customer = { id: "customer", billingPartyId: "party", code: "CJ", name: "Example Customer" };
http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3892");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui4d/session" || url.pathname === "/ui4d/browser-session") {
        const token = await new SignJWT({ sid: "ui4d-session", ver: 1, tid: tenant.id, rol: tenant.role, plt: ["platform_admin"] })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui4d" }).setSubject("ui4d")
            .setIssuer("ui4d").setAudience("ui4d-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui4d/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://127.0.0.1:3891/app/accounting/income" });
            return res.end();
        }
        return send(200, { token });
    }
    try { if (decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub !== "ui4d") throw new Error(); }
    catch { return send(401, { error: { code: "unauthenticated" } }); }
    if (url.pathname === "/v1/me") return send(200, { data: { id: "ui4d", email: "finance@fixture.test", displayName: "Finance fixture", photoUrl: null, tenant, tenants: [tenant], platformRoles: ["platform_admin"], capabilities: catalog.CAPABILITY_KEYS, customerScopes: [], dispatcher: false, steward: true, driver: null, mustChangePassword: false } });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: [tenant] });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/events") { res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" }); return res.write(": fixture\n\n"); }
    if (url.pathname === "/v1/customers") return send(200, { data: [customer] });
    if (url.pathname === "/v1/trips") return send(200, { data: [{ id: "trip", tripNo: "TR-001", taskId: "task", jobCategory: "PRIMARY", vehicleClass: "4W", deliveredAt: "2026-10-04T19:00:00Z", originPlace: { label: "Bang Na" }, destinationPlace: { label: "SOC East" }, billing: { billingAxisDate: "2026-10-05", priced: true, estimateThb: 1234.50, baseRateThb: 1200, rateMultiplier: 1.02875, customerId: customer.id, billingPartyId: customer.billingPartyId, rateEntryId: "rate", lookupHubCode: "BNA", lookupDestinationCode: "SOCE" } }] });
    if (url.pathname === "/v1/standby") return send(200, { data: [] });
    if (url.pathname === "/v1/billing/rows") return send(200, { data: { rows: [], unpriced: [], customerId: "all", year: 2026, month: 10, basis: "delivered", totals: {}, typeCounts: {}, categoryCounts: {}, issues: {}, filterOptions: { plates: [], vehicleClasses: [], reviewMonths: [] } } });
    return send(200, { data: [] });
}).listen(3892, "127.0.0.1", () => console.log("UI4D stub API ready on 3892"));
