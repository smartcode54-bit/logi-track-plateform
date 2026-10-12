// T46 (issue #66; Appendix E §E.2.2, §E.5 row 12, §E.10 rows 10-11): the payroll, compensation config and
// penalty pages over Go. Runs are keyset pages with the filters and the status counts on the server, a
// generation is the payroll.run job (no generateDriverPayoutRun callable), approval and the status moves are
// Go routes (no approveDriverPayout callable, no browser write), the penalty page's three reads run in
// parallel, and the config page appends versions. No Firebase module is imported (the mocks throw).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/firebase/client", () => {
    throw new Error("the Go HR pages must not load the Firebase client");
});
vi.mock("firebase/firestore", () => {
    throw new Error("the Go HR pages must not import Firestore");
});
vi.mock("firebase/functions", () => {
    throw new Error("the Go HR pages must not call a Cloud Function");
});

import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { queryKeys } from "@/lib/queryKeys";
import { PAYROLL_PAGE_SIZE, type PayrollRunDTO, type PayrollRunDetailDTO } from "../../api/payroll.go";
import type { CompConfigDTO } from "../../api/compConfig.go";
import type { PenaltyDTO } from "../../api/penalties.go";
import { PayrollPage } from "./PayrollPage";
import { PayrollConfigPage } from "./PayrollConfigPage";
import { PenaltiesPage } from "./PenaltiesPage";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/payroll");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK on the HR pages");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    configureFirebaseBridge(undefined);
});

const MANAGE = ["hr:view_payroll", "hr:manage_payroll"];
const q = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

function run(n: number, over: Partial<PayrollRunDTO> = {}): PayrollRunDTO {
    return {
        id: `r${n}`,
        tenantId: "t-own",
        driverId: `d${n}`,
        driverName: `Driver ${n}`,
        payPeriod: "2026-08",
        round: "R2",
        periodStart: "2026-08-15T17:00:00Z",
        periodEnd: "2026-08-31T17:00:00Z",
        status: "draft",
        totalEarningsThb: 1050,
        totalDeductionsThb: 950,
        netPayThb: 100,
        currency: "THB",
        paymentDate: null,
        paymentMethod: null,
        remarks: null,
        approvedBy: null,
        approvedAt: null,
        ledgerTransactionId: null,
        createdAt: "2026-09-01T02:00:00Z",
        updatedAt: "2026-09-01T02:00:00Z",
        ...over,
    };
}

const COUNTS = { draft: 1, pending_approval: 2, approved: 0, paid: 5, cancelled: 0 };

function base(capabilities = MANAGE) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities }) }));
    web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: { domains: { auth: "go", hr: "go" } } }));
    return web;
}

function payrollWeb(capabilities = MANAGE) {
    const web = base(capabilities);
    const state = { status: "draft" as PayrollRunDTO["status"] };
    web.on("GET", "/api/go/v1/payroll", (call) => {
        const p = q(call);
        if (p.get("cursor") === "c2") return json(200, { data: [run(3, { status: "paid", netPayThb: 900 })] });
        if (p.get("status") === "paid") return json(200, { data: [run(3, { status: "paid", netPayThb: 900 })], meta: { statusCounts: COUNTS } });
        return json(200, { data: [run(1, { status: state.status }), run(2, { status: "pending_approval", netPayThb: 200 })], nextCursor: "c2", meta: { statusCounts: COUNTS } });
    });
    web.on("GET", /^\/api\/go\/v1\/payroll\/r\d+$/, (call) => {
        const id = call.url.split("?")[0].split("/").pop() ?? "";
        const detail: PayrollRunDetailDTO = {
            ...run(Number(id.slice(1)), { status: state.status }),
            lineItems: [
                { lineNo: 0, type: "earning", category: "TRIP_COMMISSION", name: "Trip pay (weekday)", amountThb: 650, quantity: 2, unitRateThb: 325, description: null, referenceId: null, meta: null },
                { lineNo: 1, type: "earning", category: "HELPER_PAY", name: "Helper/training pay", amountThb: 400, quantity: 1, unitRateThb: 400, description: null, referenceId: null, meta: null },
                { lineNo: 2, type: "deduction", category: "SOCIAL_SECURITY", name: "Social security", amountThb: 750, quantity: null, unitRateThb: null, description: null, referenceId: null, meta: null },
                { lineNo: 3, type: "deduction", category: "PENALTY", name: "Late against SLA", amountThb: 200, quantity: null, unitRateThb: null, description: null, referenceId: "p1", meta: null },
            ],
            penaltyApplications: [{ penaltyId: "p1", appliedThb: 200 }],
        };
        return json(200, { data: detail });
    });
    web.on("POST", "/api/go/v1/payroll/r1/approve", () => {
        state.status = "approved";
        return json(200, { data: { ...run(1, { status: "approved" }), lineItems: [], penaltyApplications: [] } });
    });
    web.on("POST", "/api/go/v1/payroll/r1/status", (call) => {
        state.status = (call.body as { status: PayrollRunDTO["status"] }).status;
        return json(200, { data: { ...run(1, { status: state.status }), lineItems: [], penaltyApplications: [] } });
    });
    return { web, state };
}

describe("payroll over Go", () => {
    it("lists keyset pages with the server's filters and counts, and loads the next page by cursor", async () => {
        const { web } = payrollWeb();
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        expect(await screen.findByText("Driver 1")).toBeInTheDocument();
        expect(screen.getByTestId("payroll-pending")).toHaveTextContent("2");
        expect(screen.getByTestId("payroll-paid")).toHaveTextContent("5");
        expect(screen.getByTestId("hr-shown")).toHaveTextContent("Showing 2 of 8");
        expect(screen.getByTestId("payroll-loaded-net")).toHaveTextContent("฿300");
        const first = web.calls.find((c) => c.url.startsWith("/api/go/v1/payroll?"))!;
        expect(q(first).get("limit")).toBe(String(PAYROLL_PAGE_SIZE));
        expect(q(first).get("status")).toBeNull();

        await user.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText("Driver 3")).toBeInTheDocument();
        expect(web.calls.some((c) => c.url.includes("cursor=c2"))).toBe(true);

        await user.click(screen.getByRole("button", { name: /^Paid/ }));
        await waitFor(() => expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/payroll?") && q(c).get("status") === "paid")).toBe(true));
        await waitFor(() => expect(screen.queryByText("Driver 1")).toBeNull());
    });

    it("polls the list every 60 s while visible (Appendix E §E.5 row 12)", async () => {
        payrollWeb();
        const { client } = renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        const query = client.getQueryCache().find({ queryKey: queryKeys.payroll.list({}) });
        expect(query?.observers[0]?.options.refetchInterval).toBe(60_000);
        expect(query?.observers[0]?.options.refetchIntervalInBackground).toBe(false);
    });

    it("generates through the payroll.run job and refreshes the runs when it succeeds", async () => {
        const { web } = payrollWeb();
        web.on("POST", "/api/go/v1/jobs/payroll.run", () => json(202, { data: { jobId: "j1" } }));
        web.on("GET", "/api/go/v1/jobs/j1", () =>
            json(200, { data: { id: "j1", type: "payroll.run", status: "succeeded", result: { period: "2026-08", round: "R1", written: 3, skippedApproved: 1, drivers: 4 } } })
        );
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        const listCalls = () => web.calls.filter((c) => c.method === "GET" && c.url.startsWith("/api/go/v1/payroll?")).length;
        const before = listCalls();

        const month = screen.getByLabelText("Month to generate");
        await user.clear(month);
        await user.type(month, "2026-08");
        await user.click(screen.getByRole("button", { name: "Generate Payroll" }));

        expect(await screen.findByTestId("payroll-job-result")).toHaveTextContent("3 written, 1 approved skipped");
        const post = web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/jobs/payroll.run")!;
        expect(post.body).toEqual({ period: "2026-08", round: "R1" });
        await waitFor(() => expect(listCalls()).toBeGreaterThan(before));
    });

    it("follows a generation of the same round already running (409 already_exists with its job)", async () => {
        const { web } = payrollWeb();
        web.on("POST", "/api/go/v1/jobs/payroll.run", () => goErr(409, "already_exists", { jobId: "j9" }));
        web.on("GET", "/api/go/v1/jobs/j9", () => json(200, { data: { id: "j9", type: "payroll.run", status: "running" } }));
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        await user.click(screen.getByRole("button", { name: "Generate Payroll" }));
        expect(await screen.findByText(/already being generated/)).toBeInTheDocument();
        await waitFor(() => expect(web.count("GET", "/api/go/v1/jobs/j9")).toBeGreaterThan(0));
    });

    it("names the missing compensation config (409 no_compensation_config)", async () => {
        const { web } = payrollWeb();
        web.on("POST", "/api/go/v1/jobs/payroll.run", () => goErr(409, "failed_precondition", { reason: "no_compensation_config" }));
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        await user.click(screen.getByRole("button", { name: "Generate Payroll" }));
        expect(await screen.findByText(/There is no compensation config yet/)).toBeInTheDocument();
    });

    it("approves a run through Go after a confirmation; the runs and the penalties are stale", async () => {
        const { web } = payrollWeb();
        const user = userEvent.setup();
        const { client } = renderWithProviders(<PayrollPage />);
        client.setQueryData(queryKeys.penalties({}), { pages: [], pageParams: [] });
        await screen.findByText("Driver 1");

        await user.click(screen.getByRole("button", { name: "Manage Payroll Driver 1" }));
        await user.click(await screen.findByRole("menuitem", { name: "Approve" }));
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("Approve the run of Driver 1");
        expect(web.calls.filter((c) => c.method === "POST")).toHaveLength(0);
        await user.click(within(dialog).getByRole("button", { name: "Approve" }));

        await waitFor(() => expect(web.count("POST", "/api/go/v1/payroll/r1/approve")).toBe(1));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
        expect(client.getQueryState(queryKeys.penalties({}))?.isInvalidated).toBe(true);
    });

    it("explains a stale run (409 stale_run) instead of approving it", async () => {
        const { web } = payrollWeb();
        web.on("POST", "/api/go/v1/payroll/r2/approve", () => goErr(409, "failed_precondition", { reason: "stale_run", status: "pending_approval", penaltyIds: ["p1"] }));
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 2");
        await user.click(screen.getByRole("button", { name: "Manage Payroll Driver 2" }));
        await user.click(await screen.findByRole("menuitem", { name: "Approve" }));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Approve" }));
        expect(await screen.findByText(/Generate the round again, then approve/)).toBeInTheDocument();
    });

    it("marks an approved run as paid with the payment day and method", async () => {
        const { web, state } = payrollWeb();
        state.status = "approved";
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        await user.click(screen.getByRole("button", { name: "Manage Payroll Driver 1" }));
        await user.click(await screen.findByRole("menuitem", { name: "Mark as paid" }));
        const dialog = await screen.findByRole("dialog");
        const method = within(dialog).getByLabelText("Payment method");
        await user.clear(method);
        await user.type(method, "cash");
        await user.click(within(dialog).getByRole("button", { name: "Mark as paid" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/payroll/r1/status")).toBe(1));
        const body = web.calls.find((c) => c.url === "/api/go/v1/payroll/r1/status")!.body as Record<string, string>;
        expect(body.status).toBe("paid");
        expect(body.paymentMethod).toBe("cash");
        expect(body.paymentDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    });

    it("opens a run's payslip with its line breakdown (ADR 0013)", async () => {
        payrollWeb();
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await user.click(await screen.findByText("Driver 1"));
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByText("Trip pay (weekday)")).toBeInTheDocument();
        expect(within(dialog).getByText("2 × ฿325")).toBeInTheDocument();
        expect(within(dialog).getByText("Late against SLA")).toBeInTheDocument();
        expect(within(dialog).getByTestId("payroll-detail-net")).toHaveTextContent("฿100");
        expect(within(dialog).getByText(/deducts 1 penalty installment/)).toBeInTheDocument();
    });

    it("a viewer without hr:manage_payroll reads the runs but gets no generation and no actions", async () => {
        const { web } = payrollWeb(["hr:view_payroll"]);
        const user = userEvent.setup();
        renderWithProviders(<PayrollPage />);
        await screen.findByText("Driver 1");
        expect(screen.queryByRole("button", { name: "Generate Payroll" })).toBeNull();
        await user.click(screen.getByRole("button", { name: "Manage Payroll Driver 1" }));
        expect(await screen.findByRole("menuitem", { name: "View Details" })).toBeInTheDocument();
        expect(screen.queryByRole("menuitem", { name: "Approve" })).toBeNull();
        expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/payroll/config"))).toBe(false);
    });
});

function version(over: Partial<CompConfigDTO> = {}): CompConfigDTO {
    return {
        id: "cc2",
        tenantId: "t-own",
        effectiveFrom: "2026-08-31T17:00:00Z",
        effectiveFromDate: "2026-09-01",
        weekdayRateThb: 320,
        holidayRateThb: 350,
        payStandby: false,
        helperDayRateThb: 400,
        fuelIncentiveTiers: [{ minKmPerLitre: 10, amountThb: 1000 }],
        fuelMinRefuelsPerMonth: 5,
        tripVolumeTiers: [{ minTrips: 50, amountThb: 1000 }],
        sso: { ratePercent: 5, baseExistingThb: 15000, baseNewThb: 12000, existingHiredBeforeYear: 2026, maxAgeInclusive: 55, probationMonths: 3 },
        penaltyTypes: [{ code: "LATE_SLA", nameEn: "Late against SLA", nameTh: "ส่งงานล่าช้ากว่า SLA", defaultAmountThb: 3000 }],
        createdBy: "u-1",
        createdAt: "2026-09-01T02:00:00Z",
        ...over,
    };
}

describe("compensation config over Go", () => {
    it("starts from the version in effect and appends a new version", async () => {
        const web = base();
        web.on("GET", "/api/go/v1/payroll/config", (call) =>
            q(call).get("at") ? json(200, { data: version() }) : json(200, { data: [version(), version({ id: "cc1", effectiveFromDate: "2026-01-01", weekdayRateThb: 300 })] })
        );
        web.on("POST", "/api/go/v1/payroll/config", (call) => json(201, { data: version({ id: "cc3", ...(call.body as object), effectiveFromDate: "2026-10-12" }) }));
        const user = userEvent.setup();
        renderWithProviders(<PayrollConfigPage />);
        const weekday = await screen.findByLabelText("Weekday rate (THB/trip)");
        expect(weekday).toHaveValue(320);
        expect(await screen.findByText("Versions (2)")).toBeInTheDocument();
        const at = web.calls.find((c) => c.url.startsWith("/api/go/v1/payroll/config?"))!;
        expect(Number.isNaN(Date.parse(q(at).get("at") ?? ""))).toBe(false);

        await user.clear(weekday);
        await user.type(weekday, "340");
        await user.click(screen.getByRole("button", { name: "Save new version" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/payroll/config")).toBe(1));
        const body = web.calls.find((c) => c.method === "POST")!.body as Record<string, unknown>;
        expect(body.weekdayRateThb).toBe(340);
        expect(body.effectiveFrom).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        expect(body.penaltyTypes).toEqual(version().penaltyTypes);
        expect(body).not.toHaveProperty("id");
        expect(body).not.toHaveProperty("createdAt");
    });

    it("a tenant without a version starts from the defaults", async () => {
        const web = base();
        web.on("GET", "/api/go/v1/payroll/config", (call) => (q(call).get("at") ? json(200, { data: null }) : json(200, { data: [] })));
        renderWithProviders(<PayrollConfigPage />);
        expect(await screen.findByText(/No compensation config yet/)).toBeInTheDocument();
        expect(screen.getByLabelText("Weekday rate (THB/trip)")).toHaveValue(300);
    });

    it("lists the fields Go refused (422)", async () => {
        const web = base();
        web.on("GET", "/api/go/v1/payroll/config", (call) => (q(call).get("at") ? json(200, { data: version() }) : json(200, { data: [version()] })));
        web.on("POST", "/api/go/v1/payroll/config", () => goErr(422, "invalid_argument", { fields: [{ field: "penaltyTypes[0].code", reason: "duplicate" }] }));
        const user = userEvent.setup();
        renderWithProviders(<PayrollConfigPage />);
        await screen.findByLabelText("Weekday rate (THB/trip)");
        await user.click(screen.getByRole("button", { name: "Save new version" }));
        expect(await screen.findByText(/penaltyTypes\[0\]\.code/)).toBeInTheDocument();
    });
});

function penalty(n: number, over: Partial<PenaltyDTO> = {}): PenaltyDTO {
    return {
        id: `p${n}`,
        tenantId: "t-own",
        driverId: "d1",
        driverName: `Driver ${n}`,
        typeCode: "LATE_SLA",
        typeName: "Late against SLA",
        totalThb: 600,
        remainingThb: 400,
        installmentsTotal: 3,
        installmentsPaid: 1,
        installmentThb: 200,
        reason: "late",
        evidenceKey: null,
        status: "partially_deducted",
        incurredAt: "2026-08-21T02:00:00Z",
        createdBy: "u-1",
        createdAt: "2026-08-21T02:00:00Z",
        updatedAt: "2026-09-02T03:00:00Z",
        ...over,
    };
}

function penaltiesWeb() {
    const web = base();
    let rows = [penalty(1), penalty(2, { status: "cleared", remainingThb: 0, installmentThb: 0, installmentsPaid: 1, installmentsTotal: 1 })];
    web.on("GET", "/api/go/v1/payroll/config", () => json(200, { data: version() }));
    web.on("GET", "/api/go/v1/penalties", () => json(200, { data: rows }));
    web.on("GET", "/api/go/v1/drivers", () => json(200, { data: [{ id: "d7", displayName: "Somchai" }] }));
    web.on("POST", "/api/go/v1/penalties", (call) => {
        const b = call.body as { totalThb: number };
        rows = [penalty(9, { driverName: "Somchai", totalThb: b.totalThb, remainingThb: b.totalThb, status: "pending" }), ...rows];
        return json(201, { data: rows[0] });
    });
    web.on("POST", "/api/go/v1/penalties/p1/cancel", () => {
        rows = rows.map((r) => (r.id === "p1" ? { ...r, status: "cancelled" as const } : r));
        return json(200, { data: rows.find((r) => r.id === "p1") });
    });
    return web;
}

describe("penalties over Go", () => {
    it("reads the list and the active config in parallel, and searches drivers on the server only on demand", async () => {
        const web = penaltiesWeb();
        renderWithProviders(<PenaltiesPage />);
        expect(await screen.findByText("Driver 1")).toBeInTheDocument();
        expect(web.count("GET", "/api/go/v1/penalties")).toBe(1);
        expect(web.count("GET", "/api/go/v1/payroll/config")).toBe(1);
        expect(web.count("GET", "/api/go/v1/drivers")).toBe(0); // no list of every driver
        // The open penalty shows its next R2 deduction; the cleared one none, and no cancel.
        expect(screen.getByText("1/3")).toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: /^Cancel penalty/ })).toHaveLength(1);
    });

    it("adds a penalty in whole baht on a Bangkok day, the type's name left to Go", async () => {
        const web = penaltiesWeb();
        const user = userEvent.setup();
        renderWithProviders(<PenaltiesPage />);
        await screen.findByText("Driver 1");

        const pickers = screen.getAllByRole("combobox");
        await user.click(pickers[0]); // the form's driver picker comes first
        await user.click(await screen.findByRole("option", { name: "Somchai" }));
        await user.click(screen.getByRole("combobox", { name: "Type" }));
        await user.click(await screen.findByRole("option", { name: "Late against SLA" }));
        expect(screen.getByLabelText("Amount (THB)")).toHaveValue(3000); // the type's default
        const inst = screen.getByLabelText("Installments");
        await user.clear(inst);
        await user.type(inst, "3");
        await user.click(screen.getByRole("button", { name: "Add penalty" }));

        await waitFor(() => expect(web.count("POST", "/api/go/v1/penalties")).toBe(1));
        const body = web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/penalties")!.body as Record<string, unknown>;
        expect(body).toMatchObject({ driverId: "d7", typeCode: "LATE_SLA", totalThb: 3000, installmentsTotal: 3 });
        expect(body.incurredAt).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        expect(body).not.toHaveProperty("typeName");
        expect(await screen.findByRole("cell", { name: "Somchai" })).toBeInTheDocument();
    });

    it("cancels an open penalty after a confirmation", async () => {
        const web = penaltiesWeb();
        const user = userEvent.setup();
        renderWithProviders(<PenaltiesPage />);
        await screen.findByText("Driver 1");
        await user.click(screen.getByRole("button", { name: "Cancel penalty Driver 1" }));
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("฿400 remaining");
        await user.click(within(dialog).getByRole("button", { name: "Cancel penalty" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/penalties/p1/cancel")).toBe(1));
        await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel penalty Driver 1" })).toBeNull());
    });
});
