import { describe, it, expect, vi, afterEach } from "vitest";
import { HttpsError } from "firebase-functions/v2/https";
import { type ShimAnswer, type ShimLogger, callShim, lineNotifyShimUrl } from "./goShim";
import {
    ADD_STOP_MESSAGES,
    LINE_NOTIFY_MESSAGES,
    TASK_NOT_FOUND_REASON,
    type ClaimsStore,
    addStopForwards,
    addStopShimBody,
    driverIdFromMe,
    lineNotifyForwards,
    lineNotifyRecordId,
    mapAddStopAnswer,
    mapLineNotifyAnswer,
    setDriverClaimsViaGo,
} from "./goShimCallables";

const quiet: ShimLogger = { info: () => {}, error: () => {} };

/** A logger that records its error lines. */
function errors(): ShimLogger & { errs: { msg: string; data?: Record<string, unknown> }[] } {
    const errs: { msg: string; data?: Record<string, unknown> }[] = [];
    return { errs, info: () => {}, error: (msg, data) => errs.push({ msg, data }) };
}

const ok = (data: unknown, status = 200): ShimAnswer => ({ status, body: { data }, ms: 3 });
const fail = (status: number, code: string, details: Record<string, unknown> = {}): ShimAnswer =>
    ({ status, body: { error: { code, message: "x", details } }, ms: 3 });

/** An in-memory claims store with the calls it received. */
function store(claims: Record<string, unknown>): ClaimsStore & { claims: Record<string, unknown>; sets: number; marked: string[] } {
    const s = {
        claims: { ...claims },
        sets: 0,
        marked: [] as string[],
        getClaims: async () => ({ ...s.claims }),
        setClaims: async (_uid: string, c: Record<string, unknown>) => {
            s.sets++;
            s.claims = c;
        },
        markDriver: async (uid: string) => {
            s.marked.push(uid);
        },
    };
    return s;
}

async function httpsError(p: Promise<unknown> | (() => unknown)): Promise<HttpsError> {
    try {
        await (typeof p === "function" ? p() : p);
    } catch (e) {
        if (e instanceof HttpsError) return e;
        throw e;
    }
    throw new Error("no HttpsError");
}

describe("setDriverClaims shim (mv-go T32 -> GET /v1/mobile/me)", () => {
    const meDriver = (legacyDocId: string) => ok({ id: "u1", driver: { id: "d-uuid", tenantId: "t1", legacyDocId } });

    it("reads the driverId claim from the driver's Firestore doc id", () => {
        expect(driverIdFromMe(200, meDriver("drvDoc1").body)).toBe("drvDoc1");
        expect(driverIdFromMe(200, ok({ id: "u1", driver: null }).body)).toBeNull();
        expect(driverIdFromMe(403, fail(403, "no_account").body)).toBeNull();
        expect(driverIdFromMe(403, fail(403, "driver_profile_required").body)).toBeNull();
        expect(driverIdFromMe(403, fail(403, "account_disabled").body)).toBeUndefined();
        expect(driverIdFromMe(401, fail(401, "invalid_token", { reason: "revoked" }).body)).toBeUndefined();
        expect(driverIdFromMe(0, null)).toBeUndefined();
        expect(driverIdFromMe(200, ok({ driver: { id: "d" } }).body)).toBeUndefined();
    });

    it("grants the driver claims (merged) with the legacy answer, byte for byte", async () => {
        const s = store({ partnerScopeId: "keep-me" });
        const out = await setDriverClaimsViaGo("uid1", meDriver("drvDoc1"), s, quiet);
        expect(JSON.stringify(out)).toBe('{"success":true,"role":"driver","driverId":"drvDoc1","message":"Driver privileges granted"}');
        expect(s.claims).toEqual({ partnerScopeId: "keep-me", role: "driver", driverId: "drvDoc1" });
        expect(s.marked).toEqual(["uid1"]);
    });

    it("keeps the 'already' answer for a driver, correcting a stale driverId claim", async () => {
        const same = store({ role: "driver", driverId: "drvDoc1" });
        const out = await setDriverClaimsViaGo("uid1", meDriver("drvDoc1"), same, quiet);
        expect(JSON.stringify(out)).toBe('{"success":true,"role":"driver","message":"User already has driver privileges"}');
        expect(same.sets).toBe(0);
        const stale = store({ role: "driver", driverId: "oldDoc", admin: false });
        await setDriverClaimsViaGo("uid1", meDriver("drvDoc2"), stale, quiet);
        expect(stale.claims).toEqual({ role: "driver", driverId: "drvDoc2", admin: false });
        expect(stale.marked).toEqual([]);
    });

    it("answers 'User is not a driver' for a user Go does not know as a driver, writing nothing", async () => {
        for (const answer of [ok({ id: "u1", driver: null }), fail(403, "no_account")]) {
            const s = store({});
            const out = await setDriverClaimsViaGo("uid1", answer, s, quiet);
            expect(JSON.stringify(out)).toBe('{"success":true,"role":null,"message":"User is not a driver"}');
            expect(s.sets).toBe(0);
        }
    });

    it("fails like the legacy callable when Go cannot answer (internal 'Failed to set driver claim')", async () => {
        for (const answer of [fail(401, "invalid_token", { reason: "revoked" }), fail(503, "unavailable"), { status: 0, body: null, ms: 1 }]) {
            const e = await httpsError(setDriverClaimsViaGo("uid1", answer, store({}), quiet));
            expect([e.code, e.message]).toEqual(["internal", "Failed to set driver claim"]);
        }
        const broken = store({});
        broken.setClaims = async () => {
            throw new Error("auth down");
        };
        const e = await httpsError(setDriverClaimsViaGo("uid1", meDriver("d"), broken, quiet));
        expect(e.code).toBe("internal");
    });
});

describe("addDeliveryStop shim (mv-go T32 -> POST /v1/mobile/tasks/{id}/stops)", () => {
    it("forwards a driver's call only; any other Firebase user keeps the legacy path", () => {
        expect(addStopForwards("https://api.test", { role: "driver", driverId: "drvDoc1" })).toBe(true);
        expect(addStopForwards("https://api.test", { driverId: "drvDoc1" })).toBe(true);
        expect(addStopForwards("https://api.test", {})).toBe(false); // a fresh Google sign-in: no claims
        expect(addStopForwards("https://api.test", { role: "driver", admin: true })).toBe(false);
        expect(addStopForwards("https://api.test", undefined)).toBe(false);
        expect(addStopForwards("", { role: "driver", driverId: "drvDoc1" })).toBe(false); // the shim is off
    });

    it("sends the destination, a trimmed sourceId and isCustom true only, never a billing party", () => {
        expect(addStopShimBody({ taskId: "t", destination: "SPK1", sourceId: " hubDoc ", isCustom: false,
            destinationLinkedCustomerId: "cust", destinationLinkedCustomerName: "C", destinationCustomerLinkKind: "customer" }))
            .toEqual({ destination: "SPK1", sourceId: "hubDoc" });
        expect(addStopShimBody({ destination: "x", sourceId: null, isCustom: true })).toEqual({ destination: "x", isCustom: true });
    });

    it("rebuilds {ok, message} byte for byte", () => {
        expect(JSON.stringify(mapAddStopAnswer(ok({ taskId: "t", upgradedFromSingle: true, pending: false, stops: [] }), quiet)))
            .toBe('{"ok":true,"message":"Task upgraded to multi-delivery; stop added"}');
        expect(JSON.stringify(mapAddStopAnswer(ok({ upgradedFromSingle: false, pending: true }, 202), quiet)))
            .toBe('{"ok":true,"message":"Delivery stop added successfully"}');
        expect(JSON.stringify(mapAddStopAnswer(fail(409, "duplicate_destination", { field: "destination" }), quiet)))
            .toBe('{"ok":false,"message":"Delivery stop with same destination already exists"}');
    });

    it("throws the legacy HttpsErrors", async () => {
        const cases: [ShimAnswer, string, string][] = [
            [fail(404, "not_found", { reason: TASK_NOT_FOUND_REASON }), "not-found", ADD_STOP_MESSAGES.notFound],
            [fail(403, "not_task_owner"), "permission-denied", ADD_STOP_MESSAGES.notOwner],
            [fail(403, "permission_denied"), "permission-denied", ADD_STOP_MESSAGES.notOwner],
            [fail(409, "failed_precondition", { reason: "no_primary_destination" }), "failed-precondition", ADD_STOP_MESSAGES.noPrimary],
            [fail(422, "invalid_argument", { fields: [{ field: "destination", reason: "required" }] }), "invalid-argument",
                ADD_STOP_MESSAGES.destinationRequired],
            [fail(409, "failed_precondition", { reason: "document_changed" }), "internal", ADD_STOP_MESSAGES.failed],
            [fail(401, "invalid_token", { reason: "revoked" }), "internal", ADD_STOP_MESSAGES.failed],
            [fail(503, "unavailable"), "internal", ADD_STOP_MESSAGES.failed],
            [{ status: 0, body: null, ms: 1 }, "internal", ADD_STOP_MESSAGES.failed],
        ];
        for (const [answer, code, message] of cases) {
            const e = await httpsError(() => mapAddStopAnswer(answer, quiet));
            expect([e.code, e.message]).toEqual([code, message]);
        }
    });

    afterEach(() => vi.unstubAllGlobals());

    // Only Go's task_not_found is a missing task. Every other failure is a logged internal error, logged once (by
    // callShim or by the mapping): an API without FIREBASE_PROJECT_ID or without the route, a wrong
    // LOGITRACK_API_BASE_URL, a cf_shim key on the internal listener, a rate limit, a write-back that is not configured.
    it("logs every unexpected answer once as an internal failure, never as 'Task not found'", async () => {
        const html = new Response("<html><body>404 Not Found</body></html>", { status: 404, headers: { "Content-Type": "text/html" } });
        const envelope = (status: number, code: string, details: Record<string, unknown> = {}) =>
            new Response(JSON.stringify({ error: { code, message: "x", details } }), { status });
        const cases: [string, Response][] = [
            ["bare not_found 404 (no verifier, an older API without the route)", envelope(404, "not_found")],
            ["HTML 404 (not the Go API)", html],
            ["cf_shim key on the internal listener", envelope(403, "permission_denied", { reason: "api_key_scope" })],
            ["rate limited", envelope(429, "resource_exhausted", { bucket: "shim_user" })],
            ["write-back not configured", envelope(503, "unavailable", { reason: "write_back_unconfigured" })],
            ["revoked key", envelope(401, "invalid_token", { reason: "revoked" })],
        ];
        for (const [name, res] of cases) {
            vi.stubGlobal("fetch", async () => res);
            const log = errors();
            const answer = await callShim("addDeliveryStop", "POST", "https://api.test/v1/mobile/tasks/t/stops", "k", "Bearer t", {}, log);
            const e = await httpsError(() => mapAddStopAnswer(answer, log));
            expect([name, e.code, e.message]).toEqual([name, "internal", ADD_STOP_MESSAGES.failed]);
            expect([name, log.errs.length]).toEqual([name, 1]);
        }
        // The expected refusals are no errors of the shim.
        for (const res of [envelope(404, "not_found", { reason: TASK_NOT_FOUND_REASON }), envelope(403, "not_task_owner")]) {
            vi.stubGlobal("fetch", async () => res);
            const log = errors();
            const answer = await callShim("addDeliveryStop", "POST", "https://api.test/v1/mobile/tasks/t/stops", "k", "Bearer t", {}, log);
            await httpsError(() => mapAddStopAnswer(answer, log));
            expect(log.errs).toEqual([]);
        }
    });
});

describe("sendCustomerLineNotification shim (mv-go T49 -> POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify)", () => {
    it("forwards a driver's unforced call only; admins, forced resends and claim-less users keep the legacy path", () => {
        expect(lineNotifyForwards("https://api.test", { role: "driver", driverId: "drvDoc1" }, false)).toBe(true);
        expect(lineNotifyForwards("https://api.test", { driverId: "drvDoc1" }, false)).toBe(true);
        expect(lineNotifyForwards("https://api.test", { role: "driver", driverId: "drvDoc1" }, true)).toBe(false);
        expect(lineNotifyForwards("https://api.test", { role: "admin", admin: true }, false)).toBe(false);
        expect(lineNotifyForwards("https://api.test", {}, false)).toBe(false);
        expect(lineNotifyForwards("", { role: "driver", driverId: "drvDoc1" }, false)).toBe(false); // the shim is off
    });

    it("targets the record of the event, its Firestore doc id path-escaped", () => {
        expect(lineNotifyShimUrl("https://api.test", "checkin", "task/1")).toBe("https://api.test/v1/mobile/tasks/task%2F1/line-notify");
        expect(lineNotifyShimUrl("https://api.test", "delivered", "SPXTH01")).toBe("https://api.test/v1/mobile/trips/SPXTH01/line-notify");
        expect(lineNotifyShimUrl("https://api.test", "standby", "sb1")).toBe("https://api.test/v1/mobile/standby/sb1/line-notify");
        expect(lineNotifyRecordId("checkin", { taskId: " t1 ", tripId: "x" })).toBe("t1");
        expect(lineNotifyRecordId("delivered", { tripId: "trip1" })).toBe("trip1");
        expect(lineNotifyRecordId("standby", { standbyId: "sb1" })).toBe("sb1");
    });

    it("refuses a call without its record id with the legacy invalid-argument texts", async () => {
        for (const [event, message] of [["checkin", LINE_NOTIFY_MESSAGES.checkinId], ["delivered", LINE_NOTIFY_MESSAGES.deliveredId],
            ["standby", LINE_NOTIFY_MESSAGES.standbyId]] as const) {
            const e = await httpsError(() => lineNotifyRecordId(event, { taskId: event === "checkin" ? " " : "t" }));
            expect([e.code, e.message]).toEqual(["invalid-argument", message]);
        }
    });

    it("rebuilds {ok, skipped?, reason?} byte for byte", () => {
        expect(JSON.stringify(mapLineNotifyAnswer(ok({ ok: true }, 202), quiet))).toBe('{"ok":true}');
        expect(JSON.stringify(mapLineNotifyAnswer(ok({ ok: true, skipped: true, reason: "already notified" }), quiet)))
            .toBe('{"ok":true,"skipped":true,"reason":"already notified"}');
        expect(JSON.stringify(mapLineNotifyAnswer(ok({ ok: true, skipped: true, reason: "already queued" }), quiet)))
            .toBe('{"ok":true,"skipped":true,"reason":"already queued"}');
    });

    it("answers every failure with the legacy internal 'LINE push failed', logged once", async () => {
        const cases: ShimAnswer[] = [
            fail(403, "permission_denied", { missingCapability: ["mobile:checkin"] }),
            fail(422, "invalid_argument", { fields: [{ field: "force", reason: "not_allowed" }] }),
            fail(429, "resource_exhausted", { bucket: "shim_user" }),
        ];
        for (const answer of cases) {
            const log = errors();
            const e = await httpsError(() => mapLineNotifyAnswer(answer, log));
            expect([e.code, e.message, log.errs.length]).toEqual(["internal", LINE_NOTIFY_MESSAGES.failed, 1]);
        }
        // callShim logged these already (unreachable, refused credentials, a server error): not twice.
        for (const answer of [{ status: 0, body: null, ms: 1 }, fail(401, "invalid_token", { reason: "revoked" }), fail(503, "unavailable")]) {
            const log = errors();
            const e = await httpsError(() => mapLineNotifyAnswer(answer, log));
            expect([e.code, log.errs.length]).toEqual(["internal", 0]);
        }
    });

    it("sends the cf_shim key and the caller's own ID token, an empty body, and reads Go's answer", async () => {
        const seen: { url: string; headers: Record<string, string>; body: string }[] = [];
        vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
            seen.push({ url, headers: init.headers as Record<string, string>, body: String(init.body) });
            return new Response(JSON.stringify({ data: { ok: true } }), { status: 202 });
        });
        const answer = await callShim("sendCustomerLineNotification", "POST", lineNotifyShimUrl("https://api.test", "delivered", "fsTrip1"),
            "ltk_key", "Bearer fb-id-token", {}, quiet);
        expect(JSON.stringify(mapLineNotifyAnswer(answer, quiet))).toBe('{"ok":true}');
        expect(seen).toEqual([{ url: "https://api.test/v1/mobile/trips/fsTrip1/line-notify",
            headers: { "X-Api-Key": "ltk_key", Authorization: "Bearer fb-id-token", "Content-Type": "application/json" }, body: "{}" }]);
        vi.unstubAllGlobals();
    });
});
