// T46 (issue #66; Appendix E §E.2.2, §E.5 row 10): leave requests over Go. The list is keyset pages of 50
// with the filters and the status counts on the server, polled every 60 s while visible (AC: no unbounded
// listener on leave_requests, `leave-requests/page.tsx:61-66`); decisions are Go routes, a rejection with its
// reason, and a decision written back to the driver app (202) says it is waiting for the sync. The web has no
// Firebase SDK since TW7.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { queryKeys } from "@/lib/queryKeys";
import { LEAVE_PAGE_SIZE, type LeaveRequestDTO } from "../api/leave.go";
import { LeaveRequestsPage } from "./LeaveRequestsPage";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/leave-requests");
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

const q = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

function leave(n: number, over: Partial<LeaveRequestDTO> = {}): LeaveRequestDTO {
    return {
        id: `l${n}`,
        tenantId: "t-own",
        driverId: `d${n}`,
        driverName: `Driver ${n}`,
        type: "sick",
        status: "pending",
        startDate: "2026-10-05",
        endDate: "2026-10-06",
        days: 2,
        reason: `reason ${n}`,
        approverUserId: null,
        decidedAt: null,
        rejectionReason: null,
        attachments: [],
        createdAt: "2026-09-29T13:00:00Z",
        updatedAt: "2026-09-29T13:00:00Z",
        ...over,
    };
}

const COUNTS = { pending: 3, approved: 10, rejected: 2, cancelled: 1 };

function setup(capabilities = ["hr:view_leave", "hr:manage_leave"]) {
    const web = fakeWeb();
    const rows = new Map<string, LeaveRequestDTO>([
        ["l1", leave(1, { attachments: [{ key: "leave/l1/a.jpg", url: "https://media.test/a.jpg?sig" }, { key: "leave/l1/b.jpg", url: null }] })],
        ["l2", leave(2, { status: "approved", type: "business" })],
    ]);
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities }) }));
    web.on("GET", "/api/go/v1/leave-requests", (call) => {
        const p = q(call);
        if (p.get("cursor") === "c2") return json(200, { data: [leave(3, { status: "rejected", rejectionReason: "no certificate" })] });
        const status = p.get("status");
        const data = [...rows.values()].filter((r) => !status || r.status === status);
        return json(200, { data, nextCursor: status ? undefined : "c2", meta: { statusCounts: COUNTS } });
    });
    web.on("POST", /^\/api\/go\/v1\/leave-requests\/[^/]+\/(approve|reject|cancel)$/, (call) => {
        const [, , , , , id, action] = call.url.split("/");
        const cur = rows.get(id);
        if (!cur) return goErr(404, "not_found");
        const to = action === "approve" ? "approved" : action === "reject" ? "rejected" : "cancelled";
        const from = action === "cancel" ? "approved" : "pending";
        if (cur.status !== from) return goErr(409, "failed_precondition", { reason: "transition_not_allowed", status: cur.status, to });
        const next = { ...cur, status: to as LeaveRequestDTO["status"], rejectionReason: action === "reject" ? (call.body as { reason: string }).reason : cur.rejectionReason };
        rows.set(id, next);
        return json(200, { data: next });
    });
    return { web, rows };
}

describe("leave requests over Go", () => {
    it("lists keyset pages of 50 with the server's counts; the status filter and the next page are server reads", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        expect(await screen.findByText("Driver 1")).toBeInTheDocument();
        expect(screen.getByTestId("leave-total")).toHaveTextContent("16");
        expect(screen.getByTestId("leave-pending")).toHaveTextContent("3");
        expect(screen.getByTestId("leave-approved")).toHaveTextContent("10");
        expect(screen.getByTestId("hr-shown")).toHaveTextContent("Showing 2 of 16");
        const first = web.calls.find((c) => c.url.startsWith("/api/go/v1/leave-requests?"))!;
        expect(q(first).get("limit")).toBe(String(LEAVE_PAGE_SIZE));

        await user.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText("Driver 3")).toBeInTheDocument();
        expect(web.calls.some((c) => c.url.includes("cursor=c2"))).toBe(true);

        await user.click(screen.getByRole("button", { name: /^Approved/ }));
        await waitFor(() => expect(web.calls.some((c) => q(c).get("status") === "approved")).toBe(true));
        await waitFor(() => expect(screen.queryByText("Driver 1")).toBeNull());
        expect(screen.getByText("Driver 2")).toBeInTheDocument();
    });

    it("polls every 60 s while visible instead of a listener (Appendix E §E.5 row 10)", async () => {
        setup();
        const { client } = renderWithProviders(<LeaveRequestsPage />);
        await screen.findByText("Driver 1");
        const query = client.getQueryCache().find({ queryKey: queryKeys.leave({}) });
        expect(query?.observers[0]?.options.refetchInterval).toBe(60_000);
        expect(query?.observers[0]?.options.refetchIntervalInBackground).toBe(false);
    });

    it("approves a pending request through Go", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await user.click(await screen.findByText("Driver 1"));
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByRole("link", { name: /Document 1/ })).toHaveAttribute("href", "https://media.test/a.jpg?sig");
        expect(within(dialog).getByText(/Document 2: not available/)).toBeInTheDocument();
        await user.click(within(dialog).getByRole("button", { name: "Approve Request" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/leave-requests/l1/approve")).toBe(1));
        expect(web.calls.find((c) => c.url.endsWith("/approve"))!.body).toEqual({});
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    });

    it("a rejection needs its reason and sends it", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await screen.findByText("Driver 1");
        await user.click(screen.getByRole("button", { name: "Manage Request Driver 1" }));
        await user.click(await screen.findByRole("menuitem", { name: "Reject" }));
        const dialog = await screen.findByRole("dialog");
        const confirm = within(dialog).getByRole("button", { name: "Confirm Rejection" });
        expect(confirm).toBeDisabled();
        await user.type(within(dialog).getByLabelText(/Reason for Rejection/), "no medical certificate");
        await user.click(confirm);
        await waitFor(() => expect(web.count("POST", "/api/go/v1/leave-requests/l1/reject")).toBe(1));
        expect(web.calls.find((c) => c.url.endsWith("/reject"))!.body).toEqual({ reason: "no medical certificate" });
    });

    it("a decision written back to the driver app (202) says it waits for the sync", async () => {
        const { web } = setup();
        web.handlers.unshift((call) =>
            call.method === "POST" && call.url === "/api/go/v1/leave-requests/l1/approve"
                ? json(202, { data: { pending: true, id: "l1", status: "approved", collection: "leave_requests", docId: "LEGACY1", updateTime: "x", document: {} } })
                : undefined
        );
        const success = vi.spyOn(toast, "success");
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await user.click(await screen.findByText("Driver 1"));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Approve Request" }));
        await waitFor(() => expect(success).toHaveBeenCalledWith(expect.stringMatching(/the list shows it once it syncs/)));
    });

    it("explains a deployment without the write-back (503 write_back_unconfigured)", async () => {
        const { web } = setup();
        web.handlers.unshift((call) =>
            call.method === "POST" && call.url.startsWith("/api/go/v1/leave-requests/") ? goErr(503, "unavailable", { reason: "write_back_unconfigured" }) : undefined
        );
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await user.click(await screen.findByText("Driver 1"));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Approve Request" }));
        expect(await screen.findByText(/write-back is not configured/)).toBeInTheDocument();
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it("cancels an approved request; a pending one offers no cancel", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await user.click(await screen.findByRole("button", { name: "Manage Request Driver 2" }));
        const cancelItem = await screen.findByRole("menuitem", { name: "Cancel approved leave" });
        expect(cancelItem).toHaveClass("text-destructive");
        await user.click(cancelItem);
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).queryByRole("button", { name: "Approve Request" })).toBeNull();
        const cancelButton = within(dialog).getByRole("button", { name: "Cancel request" });
        expect(cancelButton).toHaveClass("text-destructive");
        await user.click(cancelButton);
        await waitFor(() => expect(web.count("POST", "/api/go/v1/leave-requests/l2/cancel")).toBe(1));
    });

    it("a viewer without hr:manage_leave reads but cannot decide", async () => {
        const { web } = setup(["hr:view_leave"]);
        const user = userEvent.setup();
        renderWithProviders(<LeaveRequestsPage />);
        await user.click(await screen.findByText("Driver 1"));
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).queryByRole("button", { name: "Approve Request" })).toBeNull();
        expect(within(dialog).queryByRole("button", { name: "Reject" })).toBeNull();
        expect(web.calls.filter((c) => c.method === "POST")).toHaveLength(0);
    });
});

describe("evidence links", () => {
    it("are Go's presigned http(s) URLs or same-origin media paths, never another scheme", async () => {
        const { evidenceHref } = await import("../api/leave.go");
        expect(evidenceHref("https://media.test/leave/a.jpg?X-Amz-Signature=x")).toBe("https://media.test/leave/a.jpg?X-Amz-Signature=x");
        expect(evidenceHref("/media/leave/a.jpg?sig=1")).toBe("/media/leave/a.jpg?sig=1");
        for (const bad of ["javascript:alert(1)", "data:text/html,x", "//evil.test/x", "", null, undefined]) expect(evidenceHref(bad)).toBeNull();
    });
});
