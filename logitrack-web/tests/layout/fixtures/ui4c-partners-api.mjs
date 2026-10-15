// Deterministic partner records and a signed session for the UI4C production-build checks.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4c", alg: "EdDSA", use: "sig" };
const tenant = { id: "own-fleet", nameTh: "องค์กรตัวอย่าง", nameEn: "Fixture organisation", kind: "own_fleet", role: "tenant_admin" };
const me = { id: "ui4c", email: "operator@fixture.test", displayName: "UI4C operator", photoUrl: null,
    tenant, tenants: [tenant], platformRoles: ["platform_admin"], customerScopes: [], capabilities: catalog.CAPABILITY_KEYS,
    dispatcher: false, steward: true, driver: null, mustChangePassword: false };
const carrier = {
    id: "ui4c-partner", code: "UI4C", nameTh: "รถร่วมตัวอย่างเชียงใหม่และลำพูน", nameEn: "Fixture partner", legalType: "company", status: "pending",
    contactPerson: "Fixture contact", phone: "0812345678", email: "contact@fixture.test", fleetSize: 4, lineGroupId: null,
    contractorTenantId: "own-fleet", billingPartyId: null, billingDateBasis: "delivered", createdAt: "2026-10-01T00:00:00Z",
    taxId: null, website: null, address: null, designation: "Fleet coordinator", dispatchCenter: "Chiang Mai depot",
    serviceRegions: ["Chiang Mai", "Lamphun"], vehicleTypes: ["6-wheeler"], updatedAt: null,
    documents: [
        { fileId: "file-open", key: "private/open", url: "https://files.fixture.test/company?signature=example", kind: "company_doc", position: 0 },
        { fileId: "file-missing", key: "private/missing", url: null, kind: "company_doc", position: 1 },
    ],
};
const trucks = Array.from({ length: 4 }, (_, i) => ({ id: `truck-${i}`, tenantId: carrier.id, type: "6W", licensePlate: `TEST-${i}`, status: i === 0 ? "maintenance" : "active" }));

http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3902");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (["/ui4c/session", "/ui4c/browser-session"].includes(url.pathname)) {
        const token = await new SignJWT({ sid: "ui4c", ver: 1, tid: tenant.id, rol: tenant.role, plt: me.platformRoles })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui4c" }).setSubject(me.id)
            .setIssuer("ui4c").setAudience("ui4c-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui4c/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://127.0.0.1:3901/app/subcontractors" });
            return res.end();
        }
        return send(200, { token });
    }
    try { if (decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub !== me.id) throw new Error("session"); }
    catch { return send(401, { error: { code: "unauthenticated" } }); }
    if (url.pathname === "/v1/me") return send(200, { data: me });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: me.tenants });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/subcontractors") return send(200, { data: [carrier] });
    if (url.pathname === "/v1/subcontractors/ui4c-partner") return send(200, { data: carrier });
    if (url.pathname === "/v1/trucks") return send(200, { data: trucks });
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        return res.write(": ui4c fixture\n\n");
    }
    return send(200, { data: [] });
}).listen(3902, "127.0.0.1", () => console.log("UI4C stub API ready on 3902"));
