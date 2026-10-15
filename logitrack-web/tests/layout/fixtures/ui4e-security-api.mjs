// Deterministic local data for the UI4E production-build layout checks.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const compiled = ts.transpileModule(fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } });
new Function("exports", compiled.outputText)(catalog);
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui4e", alg: "EdDSA", use: "sig" };
const now = new Date().toISOString();
const tenant = { id: "t-own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "tenant_admin" };
const system = { database: "ok", redis: "ok", scheduler: { leaderPresent: true }, outbox: { pending: 2, oldestSeconds: 2 }, jobs: { queued: 2, running: 1, failed24h: 0, succeeded24h: 8 } };

const roles = ["tenant_admin", "manager", "operation_staff", "operator", "user", "driver"];
const events = ["user_created", "login_failed", "mobile_release_published", "future_event_type"].map((type, index) => ({
    id: `e-${index}`, createdAt: now, type, severity: index === 1 ? "warning" : "info", summary: "Original server evidence",
    actorUserId: "user-uuid", actorEmail: index ? "admin@fixture.test" : null, actorLegacyUid: null, targetUserId: "target-uuid", tenantId: tenant.id, requestId: "request-1", details: { reason: "Original detail" },
}));
const users = ["active", "reset_required", "disabled"].map((status, index) => ({
    id: `u-${index}`, displayName: `ผู้ใช้งาน ${index + 1}`, email: `user${index}@fixture.test`, photoUrl: null, status, mustChangePassword: index === 1, lastLoginAt: now, createdAt: now,
    memberships: [{ ...tenant, tenantId: tenant.id, tenantNameTh: tenant.nameTh, tenantNameEn: tenant.nameEn, tenantKind: tenant.kind, role: "operator", status: "active" }], scopes: [], platformRoles: [], driver: null,
}));
// Own-fleet reach includes linked-carrier members and, for stewards, membershipless accounts.
users.push({ ...users[0], id: "u-carrier", displayName: "Carrier member", email: "carrier@fixture.test", memberships: [
    { tenantId: "t-car", tenantNameTh: "รถร่วม", tenantNameEn: "Linked carrier", tenantKind: "carrier", role: "operator", status: "active" },
] });
users.push({ ...users[0], id: "u-scope", displayName: "Scope-only account", email: "scope@fixture.test", memberships: [], scopes: [
    { kind: "customer", billingPartyId: "bp-1", name: "Customer only" },
] });
const gate = { flavor: "prod", latestVersion: "3.2.0", latestBuildNumber: "12", minAllowedVersion: "3.1.0", apkDownloadUrl: "https://example.test/app.apk", releasedAt: now, releasedBy: "cmd/release" };
const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3902");
    const send = (data, meta) => { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify({ data, ...(meta ? { meta } : {}) })); };
    if (url.pathname === "/.well-known/jwks.json") { res.writeHead(200, { "Content-Type": "application/json" }); return res.end(JSON.stringify({ keys: [jwk] })); }
    if (["/ui4e/session", "/ui4e/browser-session"].includes(url.pathname)) {
        const profile = url.searchParams.get("profile") === "tenant" ? "tenant" : "platform";
        const token = await new SignJWT({ sid: profile, tid: tenant.id, rol: "tenant_admin", ver: 1, plt: profile === "platform" ? ["platform_admin"] : [] })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui4e" }).setSubject(profile).setIssuer("ui4e").setAudience("ui4e-web").setIssuedAt().setExpirationTime("4h").sign(privateKey);
        if (url.pathname === "/ui4e/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://localhost:3901/app/security-center" });
            return res.end();
        }
        return send({ token });
    }
    let profile;
    try { profile = decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub; } catch { /* An unauthenticated fixture request. */ }
    const scope = { all: profile === "platform", tenantId: profile === "platform" ? null : tenant.id };
    if (!profile) { res.writeHead(401); return res.end(); }
    if (url.pathname === "/v1/me") return send({ id: profile, email: `${profile}@fixture.test`, displayName: "UI4E fixture", photoUrl: null, tenant, tenants: [tenant], platformRoles: profile === "platform" ? ["platform_admin"] : [], customerScopes: [], dispatcher: false, steward: true, driver: null, mustChangePassword: false, capabilities: catalog.CAPABILITY_KEYS });
    if (url.pathname === "/v1/me/tenants") return send([tenant]);
    // Layout data is static; close the feed so page navigations cannot exhaust browser connections.
    if (url.pathname === "/v1/events") { res.writeHead(204); return res.end(); }
    if (url.pathname === "/v1/badges") return send({});
    if (url.pathname === "/v1/security/overview") return send({ generatedAt: now, scope, users: { total: 12, byStatus: { active: 10 } }, sessions: { active: 7, revoked24h: 1 }, logins: { failed24h: 3, lockouts24h: 0 }, events: { last24h: 4, last7d: 9, bySeverity24h: { warning: 1 }, hourly24h: Array.from({ length: 24 }, (_, i) => i % 4), daily7d: [1, 2, 0, 1, 2, 1, 2] }, installations: { total: 3, seen7d: 3, belowFloor: 0, belowLatest: 1 }, quarantine: { rows: 3 }, system });
    if (url.pathname === "/v1/security/status") return send(system);
    if (url.pathname === "/v1/stats/users-by-role") return send({ scope, total: 12, none: 0, tenantRoles: { tenant_admin: 1, manager: 2, operation_staff: 1, operator: 1, user: 1, driver: 6 }, scopes: {}, platformRoles: {} });
    if (url.pathname === "/v1/security-events") return send(events, { types: events.map((event) => event.type) });
    if (url.pathname === "/v1/users") return send(users);
    if (url.pathname === "/v1/tenants") return send([{ ...tenant, code: "OWN", status: "active", legalType: "company", createdAt: now }]);
    if (url.pathname === "/v1/roles/matrix") return send({ layer: "tenant", tenantId: tenant.id, tenantKind: tenant.kind, roles: roles.map((role) => ({ role, capabilities: Object.fromEntries(catalog.CAPABILITY_CATALOG.filter((cap) => ["operations", "users", "security"].includes(cap.module)).map((cap) => [cap.key, { default: true, effective: true, overridden: role === "manager", overridable: role !== "tenant_admin", platformOverride: role === "operator" ? true : undefined }])) })) });
    if (url.pathname === "/v1/api-keys") return send([{ id: "key-1", name: "Partner feed", scope: "cf_shim", keyPrefix: "lt_example", capabilities: ["fleet:view_trucks"], tenantId: tenant.id, tenantNameTh: tenant.nameTh, tenantNameEn: tenant.nameEn, status: "active", createdAt: now, createdByEmail: "admin@fixture.test", lastUsedAt: now }]);
    if (url.pathname === "/v1/app-installations") return send(["prod", "dev", "future"].map((flavor, index) => ({ driverId: `driver-uuid-${index}`, installId: `install-uuid-${index}`, driverName: `คนขับ ${index + 1}`, tenantName: index === 1 ? "Linked carrier" : tenant.nameTh, platform: index === 1 ? "ios" : "android", appVersion: "3.2.0", buildNumber: "12", flavor, lastSeenAt: now, versionStatus: "current" })));
    if (url.pathname === "/v1/app-installations/stats") return send({ total: 3, byStatus: { current: 3 }, flavor: "prod", minAllowedVersion: "3.1.0", latestVersion: "3.2.0" });
    if (url.pathname === "/v1/app-releases") return send([{ id: "r-1", flavor: "prod", version: "3.2.0", buildNumber: "12", releasedAt: now, releasedBy: "cmd/release", latest: true }], { settings: gate });
    return send([]);
});
server.listen(3902, "127.0.0.1", () => console.log("UI4E fixture ready on 3902"));
