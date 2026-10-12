// T49 (issue #69): the LINE card and evidence link requests of the Go pages. A card is a Go job (202 {jobId}); a request
// for a card already waiting is 409 already_exists and that job is followed; a revocation is 204, 200 {legacyLinkOpen}
// when a link the legacy system sent may still open, or 409 legacy_evidence_link before the LINE cut-over; the finished
// job is reported in the legacy page's words.
import { afterEach, describe, expect, it, vi } from "vitest";

import { getQueryClient } from "@/lib/queryClient";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";

import { outcomeText } from "../components/go/LineNotifyActions";
import { isLegacyLinkRefusal, lineOutcome, lineReasonKey, revokeEvidence, startLineNotify } from "./lineNotify.go";

afterEach(() => {
    vi.unstubAllGlobals();
    getQueryClient().clear();
});

describe("LINE card requests over Go", () => {
    it("asks the route of each card with {force} and returns the job", async () => {
        const web = fakeWeb();
        web.on("POST", /\/api\/go\/v1\/(tasks|trips|standby)\/[^/]+\/notify-line$/, () => json(202, { data: { jobId: "j1", status: "queued" } }));
        expect(await startLineNotify("trips", "trip 1", true)).toEqual({ jobId: "j1", joined: false });
        expect(await startLineNotify("standby", "s1", false)).toEqual({ jobId: "j1", joined: false });
        expect(await startLineNotify("tasks", "t1", false)).toEqual({ jobId: "j1", joined: false });
        expect(web.calls.map((c) => [c.method, c.url.split("?")[0], c.body])).toEqual([
            ["POST", "/api/go/v1/trips/trip%201/notify-line", { force: true }],
            ["POST", "/api/go/v1/standby/s1/notify-line", { force: false }],
            ["POST", "/api/go/v1/tasks/t1/notify-line", { force: false }],
        ]);
    });

    it("follows the job already waiting for the same card (409 already_exists) and throws anything else", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/trips/t1/notify-line", () => goErr(409, "already_exists", { jobId: "j-other" }));
        web.on("POST", "/api/go/v1/trips/t2/notify-line", () => goErr(404, "not_found"));
        expect(await startLineNotify("trips", "t1", true)).toEqual({ jobId: "j-other", joined: true });
        await expect(startLineNotify("trips", "t2", true)).rejects.toMatchObject({ code: "not_found" });
    });

    it("revokes a trip's or a standby record's photo link and says what stayed open", async () => {
        const web = fakeWeb();
        web.on("POST", /\/trips\/[^/]+\/evidence\/revoke$/, () => new Response(null, { status: 204 }));
        web.on("POST", "/api/go/v1/standby/s1/evidence/revoke", () => json(200, { data: { revoked: true, legacyLinkOpen: true } }));
        web.on("POST", "/api/go/v1/standby/s2/evidence/revoke", () => goErr(409, "failed_precondition", { reason: "legacy_evidence_link" }));
        web.on("POST", "/api/go/v1/standby/s3/evidence/revoke", () => goErr(409, "failed_precondition", { reason: "other" }));
        expect(await revokeEvidence("trips", "t1")).toEqual({ legacyLinkOpen: false });
        // Closed here, but a link the legacy system sent may still open (no Firestore writer).
        expect(await revokeEvidence("standby", "s1")).toEqual({ legacyLinkOpen: true });
        // Refused while the legacy system still serves the record's link.
        expect(isLegacyLinkRefusal(await revokeEvidence("standby", "s2").catch((e: unknown) => e))).toBe(true);
        expect(isLegacyLinkRefusal(await revokeEvidence("standby", "s3").catch((e: unknown) => e))).toBe(false);
        expect(web.calls.map((c) => c.url.split("?")[0])).toEqual([
            "/api/go/v1/trips/t1/evidence/revoke",
            "/api/go/v1/standby/s1/evidence/revoke",
            "/api/go/v1/standby/s2/evidence/revoke",
            "/api/go/v1/standby/s3/evidence/revoke",
        ]);
    });

    it("reports a finished job as the legacy page reported the callable", () => {
        const t = (key: string) => `[${key}]`;
        expect(lineOutcome({ status: "running" })).toBeNull();
        const sent = lineOutcome({ status: "succeeded", result: { sent: true, photoCount: 4 } });
        expect(sent).toEqual({ kind: "sent", photoCount: 4 });
        expect(outcomeText(sent!, t)).toBe("[lineNotify.sent]");
        const already = lineOutcome({ status: "succeeded", result: { sent: false, skipped: true, reason: "already notified" } });
        expect(outcomeText(already!, t)).toBe("[lineNotify.skipAlready]");
        const off = lineOutcome({ status: "succeeded", result: { sent: false, skipped: true, reason: "LINE_NOTIFY_ENABLED is false" } });
        expect(outcomeText(off!, t)).toBe("[lineNotify.skipDisabled]");
        expect(outcomeText({ kind: "skipped", reason: "no lineGroupId configured" }, t)).toBe("[lineNotify.skipNoGroup]");
        expect(outcomeText({ kind: "skipped", reason: "sent by another message within 24 h" }, t))
            .toBe("[lineNotify.skipGeneric]: sent by another message within 24 h");
        const refused = lineOutcome({ status: "failed", result: { sent: false, skipped: true, reason: "line: push HTTP 400: bad to" }, error: "x" });
        expect(refused).toEqual({ kind: "failed", reason: "line: push HTTP 400: bad to" });
        expect(outcomeText(refused!, t)).toBe("[lineNotify.failed]: line: push HTTP 400: bad to");
        expect(lineReasonKey("anything else")).toBeNull();
    });
});
