// Deterministic sessions for the production-build palette acceptance suite, only on the task's stub port.
import http from "node:http";
import fs from "node:fs";
import ts from "typescript";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const catalog = {};
const source = fs.readFileSync(new URL("../../../../shared-docs/schemas/capabilities.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
new Function("exports", compiled.outputText)(catalog);
const roleCaps = (role) => catalog.ROLE_DEFAULT_CAPABILITIES.find((row) => row.role === role)?.capabilities ?? [];
const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui3f5", alg: "EdDSA", use: "sig" };
const tenant = (kind) => ({ id: kind, nameTh: "บริษัทขนส่งวันเพ็ญรัชดาและพันธมิตรทั่วประเทศไทยจำกัด", nameEn: "Wanpen Ratchada Transport and Nationwide Logistics Partners Limited", kind, role: "tenant_admin" });
const staffTenant = tenant("own_fleet");
const partnerTenant = tenant("carrier");
const principals = {
    staff: { tenant: staffTenant, tenants: [staffTenant], platformRoles: ["platform_admin"], customerScopes: [], capabilities: catalog.CAPABILITY_KEYS },
    customer: { tenant: null, tenants: [], platformRoles: [], customerScopes: [{ billingPartyId: "customer", name: staffTenant.nameTh }], capabilities: roleCaps("customer") },
    partner: { tenant: partnerTenant, tenants: [partnerTenant], platformRoles: [], customerScopes: [], capabilities: roleCaps("tenant_admin").filter((cap) => !catalog.STEWARD_CAPABILITIES.includes(cap)) },
};

http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3842");
    const send = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui3f5/session" || url.pathname === "/ui3f5/browser-session") {
        const profile = url.searchParams.get("profile");
        const me = principals[profile];
        if (!me) return send(400, {});
        const token = await new SignJWT({ sid: `ui3f5-${profile}`, ver: 1,
            ...(me.tenant ? { tid: me.tenant.id, rol: me.tenant.role } : { cs: ["customer"] }), plt: me.platformRoles })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui3f5" }).setSubject(profile)
            .setIssuer("ui3f5").setAudience("ui3f5-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui3f5/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://127.0.0.1:3841/app/support" });
            return res.end();
        }
        return send(200, { token });
    }
    let profile;
    try { profile = decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub; } catch { /* No fixture principal. */ }
    const me = principals[profile];
    if (!me) return send(401, { error: { code: "unauthenticated" } });
    if (url.pathname === "/v1/me") return send(200, { data: { ...me, id: profile, email: `${profile}@fixture.test`, displayName: "Navigation fixture", photoUrl: null,
        dispatcher: false, steward: profile === "staff", driver: null, mustChangePassword: false } });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: me.tenants });
    if (url.pathname === "/v1/badges") return send(200, { data: {} });
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        return res.write(": ui3f5 fixture\n\n");
    }
    return send(200, { data: [] });
}).listen(3842, "127.0.0.1", () => console.log("UI3F5 stub API ready on 3842"));
