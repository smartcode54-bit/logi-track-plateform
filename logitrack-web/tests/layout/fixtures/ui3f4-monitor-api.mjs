// Deterministic browser fixture, bound only to the UI3F4 stub port. No application authorization is changed.
import http from "node:http";
import { decodeJwt, exportJWK, generateKeyPair, SignJWT } from "jose";

const { publicKey, privateKey } = await generateKeyPair("EdDSA");
const jwk = { ...await exportJWK(publicKey), kid: "ui3f4", alg: "EdDSA", use: "sig" };
const caps = ["operations:view_driver_monitor"];
const carrier = { id: "carrier", nameTh: "บริษัทขนส่งรถร่วมทดสอบชื่อยาว", nameEn: "Partner transport", kind: "carrier", role: "tenant_admin" };
const principals = {
    customer: { id: "customer", tenant: null, tenants: [], customerScopes: [{ billingPartyId: "c1", name: "บริษัทลูกค้าทดสอบชื่อยาว" }] },
    partner: { id: "partner", tenant: carrier, tenants: [carrier], customerScopes: [] },
};
const place = (label) => ({ raw: label, key: `code:${label}`, label, resolved: true });
const origin = place("ALANG-A วังทองหลาง จุดรับสินค้าทดสอบ");
const destination = place("SOCN ศูนย์กระจายสินค้าปลายทางทดสอบ");
const rows = Array.from({ length: 5000 }, (_, i) => ({
    id: `trip-${i}`, tenantId: "own", tripNo: `FM-UI3F4-${i}`, status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY",
    createdAt: "2026-10-12T02:00:00Z", updatedAt: "2026-10-12T06:00:00Z", deliveredAt: "2026-10-12T06:00:00Z",
    taskId: null, taskNo: null, taskStatus: null, checkInAt: null, runOrder: null, planAt: null,
    driverId: "d1", driverName: "สมชาย คนขับรถขนส่งทดสอบชื่อยาว", origin, destination,
    stops: [1, 2, 3].map((index) => ({ index, place: destination, status: "delivered", deliveredAt: "2026-10-12T06:00:00Z", completedSeq: index })),
    truck: { truckId: "truck-1", plate: "กข-1234", key: "truck:1", vehicleClass: "6WH", vehicleClassKey: "6WH" },
    partnerCode: null, sealCode: null, isMultiDelivery: true, incidentCount: 0, photoCount: 3,
}));
const facets = { total: 5000, statusCounts: { delivered: 5000 }, incident: 0, origins: [], destinations: [], plates: [], vehicleClasses: [], partners: [] };

http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1:3832");
    console.log(req.method, url.pathname);
    const send = (status, body) => {
        res.writeHead(status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(body));
    };
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [jwk] });
    if (url.pathname === "/ui3f4/session" || url.pathname === "/ui3f4/browser-session" || url.pathname === "/v1/auth/login") {
        let profile = url.searchParams.get("profile");
        if (url.pathname === "/v1/auth/login") {
            let body = "";
            for await (const chunk of req) body += chunk;
            profile = JSON.parse(body).email?.split("@")[0];
        }
        if (!principals[profile]) return send(400, {});
        const token = await new SignJWT({ sid: `ui3f4-${profile}`, ver: 1, ...(profile === "partner" ? { tid: "carrier", rol: "tenant_admin" } : { cs: ["c1"] }) })
            .setProtectedHeader({ alg: "EdDSA", kid: "ui3f4" }).setSubject(profile)
            .setIssuer("ui3f4").setAudience("ui3f4-web").setIssuedAt().setExpirationTime("2h").sign(privateKey);
        if (url.pathname === "/ui3f4/browser-session") {
            res.writeHead(302, { "Set-Cookie": `lt_at=${token}; Path=/; HttpOnly; SameSite=Lax`, Location: "http://localhost:3831/app/driver-monitor" });
            return res.end();
        }
        return send(200, url.pathname === "/v1/auth/login" ? { data: { accessToken: token, refreshToken: "ui3f4_fixture_refresh" } } : { token });
    }
    if (url.pathname === "/v1/events") {
        res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
        res.write(": ui3f4 fixture\n\n");
        return;
    }
    let profile;
    try { profile = decodeJwt((req.headers.authorization ?? "").replace(/^Bearer /, "")).sub; } catch { /* An unsigned request has no fixture principal. */ }
    if (!principals[profile]) return send(401, { error: { code: "unauthenticated" } });
    if (url.pathname === "/v1/me") return send(200, { data: {
        ...principals[profile], email: `${profile}@fixture.test`, displayName: "Fixture", photoUrl: null,
        platformRoles: [], dispatcher: false, steward: false, driver: null, capabilities: caps, mustChangePassword: false,
    } });
    if (url.pathname === "/v1/me/tenants") return send(200, { data: principals[profile].tenants });
    if (url.pathname === "/v1/trips/monitor") {
        const from = Number(url.searchParams.get("cursor") ?? 0);
        const limit = Number(url.searchParams.get("limit") ?? 200);
        return send(200, { data: rows.slice(from, from + limit), ...(from + limit < rows.length ? { nextCursor: String(from + limit) } : {}), meta: { facets } });
    }
    if (url.pathname === "/v1/trips/trip-0") return send(200, { data: { ...rows[0], photos: [], stops: [] } });
    return send(404, { error: { code: "not_found" } });
}).listen(3832, "127.0.0.1", () => console.log("UI3F4 fixture API listening on 3832"));
