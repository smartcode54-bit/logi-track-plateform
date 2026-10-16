// Local isolated fixture: GO_API_INTERNAL_URL=http://127.0.0.1:4052, WEB_PUBLIC_ORIGIN=http://localhost:4051,
// JWT_ISSUER=psa-consent, JWT_AUDIENCE=psa-consent-web, SESSION_COOKIE_SECURE=false.
import http from "node:http";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const compiled = ts.transpileModule(fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } });
new Function("exports", compiled.outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "psa-consent", alg: "EdDSA", use: "sig" };
const tenant = { id: "t-own", nameTh: "บริษัททดสอบ", nameEn: "Fixture fleet", kind: "own_fleet", role: "tenant_admin" };
const sessions = new Map();
const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:4052");
    const send = (data, status = 200) => { res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" }); res.end(JSON.stringify({ data })); };
    const refuse = (status, code) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify({ error: { code, message: code, requestId: "psa-consent" } })); };
    if (url.pathname === "/.well-known/jwks.json") { res.writeHead(200, { "Content-Type": "application/json" }); return res.end(JSON.stringify({ keys: [jwk] })); }
    if (["/psa-consent/session", "/psa-consent/browser-session"].includes(url.pathname)) {
        const id = randomUUID();
        sessions.set(id, { enabled: false, version: "5", changedAt: null, changedBy: null, eligibleToToggle: true, ratified: false });
        const token = await new SignJWT({ sid: id, tid: tenant.id, rol: "tenant_admin", ver: 1, plt: [] }).setProtectedHeader({ alg: "EdDSA", kid: "psa-consent" }).setSubject(id).setIssuer("psa-consent").setAudience("psa-consent-web").setIssuedAt().setExpirationTime("4h").sign(privateKey);
        if (url.pathname === "/psa-consent/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://localhost:4051/app/settings/support-access" });
            return res.end();
        }
        return send({ token });
    }
    let id;
    try { id = decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub; } catch { /* Session setup is the only anonymous endpoint. */ }
    const state = sessions.get(id);
    if (!state) return refuse(401, "unauthenticated");
    const consent = () => ({ enabled: state.enabled, version: state.version, changedAt: state.changedAt, changedBy: state.changedBy, eligibleToToggle: state.eligibleToToggle });
    if (url.pathname === "/v1/me") return send({ id, email: "admin@fixture.test", displayName: "Company administrator", photoUrl: null, tenant, tenants: [tenant], platformRoles: [], customerScopes: [], dispatcher: false, steward: true, driver: null, mustChangePassword: false, capabilities: catalog.ROLE_DEFAULT_CAPABILITIES.find((row) => row.role === "tenant_admin").capabilities });
    if (url.pathname === "/v1/me/tenants") return send([tenant]);
    if (url.pathname === "/v1/events") { res.writeHead(204); return res.end(); }
    if (url.pathname === "/v1/badges") return send({});
    if (url.pathname === "/v1/tenants/t-own/support-access") {
        if (req.method === "PUT") {
            let raw = "";
            for await (const chunk of req) raw += chunk;
            const body = JSON.parse(raw);
            if (body.expectedVersion !== state.version) return refuse(409, "support_access_changed");
            state.enabled = body.enabled;
            state.version = String(Number(state.version) + 1);
            state.changedAt = "2026-10-16T03:00:00Z";
            state.changedBy = id;
        }
        return send(consent());
    }
    if (url.pathname === "/v1/tenants/t-own/members") return send([
        { user: { id, displayName: "Company administrator", email: "admin@fixture.test" }, role: "tenant_admin", status: "active", grantedByPlatform: false, grantVersion: "1", ratifiedAt: null, eligibilityBasis: "member", eligibleToRatify: false },
        { user: { id: "u-new", displayName: "New administrator", email: "new.administrator.with.a.long.address@fixture.example.test" }, role: "tenant_admin", status: "active", grantedByPlatform: true, grantVersion: state.ratified ? "8" : "7", ratifiedAt: state.ratified ? "2026-10-16T03:00:00Z" : null, eligibilityBasis: state.ratified ? "ratified" : "pending", eligibleToRatify: !state.ratified },
        { user: { id: "u-later", displayName: "สมาชิกที่ต้องให้ผู้ดูแลก่อนหน้ารับรอง / Awaiting an earlier administrator", email: "later@fixture.test" }, role: "operator", status: "active", grantedByPlatform: true, grantVersion: "3", ratifiedAt: null, eligibilityBasis: "pending", eligibleToRatify: false },
    ]);
    if (url.pathname === "/v1/tenants/t-own/members/u-new/ratify" && req.method === "POST") {
        let raw = "";
        for await (const chunk of req) raw += chunk;
        if (JSON.parse(raw).expectedGrantVersion !== "7" || state.ratified) return refuse(409, "membership_grant_changed");
        state.ratified = true;
        return send({ grantVersion: "8", ratifiedAt: "2026-10-16T03:00:00Z" });
    }
    return refuse(404, "not_found");
});
server.listen(4052, "127.0.0.1", () => console.log(`PSA consent fixture listening on 4052 (PID ${process.pid})`));
