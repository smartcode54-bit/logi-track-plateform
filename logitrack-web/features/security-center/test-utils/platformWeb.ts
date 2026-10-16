import { fakeWeb, goErr, json, type FakeCall } from "@/test-utils/fakeWeb";

/** Register both DTO planes for shared member/platform page fixtures; calls retain their real URL. */
export function fakeD1Web() {
    const web = fakeWeb();
    const on = web.on.bind(web);
    web.on = (method, path, reply) => {
        on(method, path, reply);
        if (method !== "GET" || typeof path !== "string" || !/^\/api\/go\/v1\/(users(?:\/[^/]+(?:\/sessions)?)?|tenants\/[^/]+\/members|security-events|security\/overview|stats\/users-by-role|api-keys|app-installations(?:\/stats)?)$/.test(path)) return web;
        on(method, path.replace("/v1/", "/v1/platform/"), async (call: FakeCall) => {
            const url = new URL(call.url, "http://web.test");
            if (url.pathname === "/api/go/v1/platform/users" && url.searchParams.get("q")) return goErr(403, "support_access_masked");
            const response = await reply(call);
            if (!response.ok) return response;
            const body = await response.json();
            const meta = { redacted: [], ...body.meta };
            if (path === "/api/go/v1/security-events") {
                delete meta.types;
                body.data = body.data?.map((row: { details: Record<string, unknown> | null }) => ({ ...row, details: row.details && Object.fromEntries(Object.entries(row.details).filter(([key]) => ["action", "basis", "outcome", "version"].includes(key))) }));
            }
            return json(response.status, { ...body, meta });
        });
        return web;
    };
    return web;
}
