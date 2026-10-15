import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";
import { useBillingJob } from "./useBillingJob";

const input = { fromDateStr: "2026-10-01", toDateStr: "2026-10-31" };
const type = "billing.backfill-trips";
function harness() {
    const web = fakeWeb();
    const client = createQueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    return { web, client, wrapper };
}
afterEach(() => { sessionStorage.clear(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("useBillingJob default contract", () => {
    it("starts, follows and returns a completed result without accessing storage", async () => {
        const { web, client, wrapper } = harness();
        const storage = vi.spyOn(Storage.prototype, "getItem");
        const invalidate = vi.spyOn(client, "invalidateQueries");
        web.on("POST", `/api/go/v1/jobs/${type}`, () => json(202, { data: { jobId: "j1" } }));
        web.on("GET", "/api/go/v1/jobs/j1", () => json(200, { data: { id: "j1", status: "succeeded", result: { written: 2 } } }));
        const { result } = renderHook(() => useBillingJob<{ written: number }>(), { wrapper });
        expect(result.current.running).toBe(false);
        await act(() => result.current.start(type, input));
        await waitFor(() => expect(result.current.result).toEqual({ written: 2 }));
        expect(result.current.running).toBe(false);
        expect(storage).not.toHaveBeenCalled();
        expect(invalidate).not.toHaveBeenCalled();
        expect(web.calls.find((c) => c.method === "POST")?.body).toEqual(input);
    });
    it("keeps 409 as a start error and does not attach without opting in", async () => {
        const { web, wrapper } = harness();
        web.on("POST", `/api/go/v1/jobs/${type}`, () => goErr(409, "already_exists", { jobId: "j-existing" }));
        const { result } = renderHook(() => useBillingJob(), { wrapper });
        await act(() => result.current.start(type, input));
        expect(result.current.jobId).toBeNull();
        expect(result.current.error).toMatchObject({ code: "already_exists" });
        expect(web.count("GET", "/api/go/v1/jobs/j-existing")).toBe(0);
    });
});

describe("opt-in restoration", () => {
    it("refreshes income and billing once when an attached job completes through polling", async () => {
        const { web, client, wrapper } = harness();
        const invalidate = vi.spyOn(client, "invalidateQueries");
        web.on("POST", `/api/go/v1/jobs/${type}`, () => goErr(409, "already_exists", { jobId: "other-owner" }));
        let status = "running";
        web.on("GET", "/api/go/v1/jobs/other-owner", () => json(200, { data: { id: "other-owner", type, status } }));
        const { result, rerender } = renderHook(() => useBillingJob({ storageKey: "scope" }), { wrapper });
        await act(() => result.current.start(type, input));
        await waitFor(() => expect(result.current.job?.status).toBe("running"));
        expect(invalidate).not.toHaveBeenCalled();
        status = "succeeded";
        await act(() => client.refetchQueries({ queryKey: ["jobs", "other-owner"] }));
        await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["income"] }));
        expect(invalidate).toHaveBeenCalledWith({ queryKey: ["billing"] });
        rerender();
        await act(() => client.refetchQueries({ queryKey: ["jobs", "other-owner"] }));
        expect(invalidate).toHaveBeenCalledTimes(2);
        expect(sessionStorage.getItem("scope")).toBeNull();
    });
    it("attaches to 409, stores only its id and follows the existing job", async () => {
        const { web, wrapper } = harness();
        const onAlreadyRunning = vi.fn();
        web.on("POST", `/api/go/v1/jobs/${type}`, () => goErr(409, "already_exists", { jobId: "j-existing" }));
        web.on("GET", "/api/go/v1/jobs/j-existing", () => json(200, { data: { id: "j-existing", status: "running" } }));
        const { result } = renderHook(() => useBillingJob({ storageKey: "viewer:tenant:type:scope", onAlreadyRunning }), { wrapper });
        await act(() => result.current.start(type, input));
        await waitFor(() => expect(result.current.job?.status).toBe("running"));
        expect(result.current.error).toBeNull();
        expect(sessionStorage.getItem("viewer:tenant:type:scope")).toBe("j-existing");
        expect(onAlreadyRunning).toHaveBeenCalledOnce();
    });
    it.each([403, 404])("clears restored ids that return %s", async (status) => {
        const { web, wrapper } = harness();
        sessionStorage.setItem("scope", "gone");
        web.on("GET", "/api/go/v1/jobs/gone", () => goErr(status, status === 403 ? "permission_denied" : "not_found"));
        const restored = vi.fn();
        const { result } = renderHook(() => useBillingJob({ storageKey: "scope", onRestored: restored }), { wrapper });
        await waitFor(() => expect(result.current.error).toMatchObject({ status }));
        expect(result.current.jobId).toBeNull();
        expect(sessionStorage.getItem("scope")).toBeNull();
        expect(restored).toHaveBeenCalledOnce();
    });
    it("switches scope without following another scope's job", async () => {
        const { web, wrapper } = harness();
        sessionStorage.setItem("viewer-a:tenant-a:type:scope", "ja");
        sessionStorage.setItem("viewer-b:tenant-b:type:scope", "jb");
        for (const id of ["ja", "jb"]) web.on("GET", `/api/go/v1/jobs/${id}`, () => json(200, { data: { id, status: "running" } }));
        const { result, rerender } = renderHook(({ key }) => useBillingJob({ storageKey: key }), { wrapper, initialProps: { key: "viewer-a:tenant-a:type:scope" } });
        await waitFor(() => expect(result.current.jobId).toBe("ja"));
        rerender({ key: "viewer-b:tenant-b:type:scope" });
        await waitFor(() => expect(result.current.jobId).toBe("jb"));
        expect(sessionStorage.getItem("viewer-a:tenant-a:type:scope")).toBe("ja");
    });
    it("keeps working when sessionStorage is unavailable", async () => {
        const { web, wrapper } = harness();
        vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("disabled"); });
        vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("disabled"); });
        vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("disabled"); });
        web.on("POST", `/api/go/v1/jobs/${type}`, () => json(202, { data: { jobId: "j1" } }));
        web.on("GET", "/api/go/v1/jobs/j1", () => json(200, { data: { id: "j1", status: "succeeded", result: {} } }));
        const { result } = renderHook(() => useBillingJob({ storageKey: "scope" }), { wrapper });
        await act(() => result.current.start(type, input));
        await waitFor(() => expect(result.current.succeeded).toBe(true));
    });
});
