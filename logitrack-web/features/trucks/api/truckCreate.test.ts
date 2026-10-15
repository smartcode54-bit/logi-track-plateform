import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";
import { truckDefaultValues } from "@/validate/truckSchema";
import { createTruck, TruckFilesSaveError } from "./trucks";
import { uploadToGo } from "@/lib/goUpload";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
vi.mock("@/lib/goUpload", () => ({ uploadToGo: vi.fn() }));
afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks(); resetQueryClientForTests(); });
const values = { ...truckDefaultValues, licensePlate: "1กก-1234", province: "กรุงเทพมหานคร", brand: "Isuzu", model: "FRR", year: "2023", color: "ขาว", type: "6 Wheels", truckStatus: "active" as const, imageFrontRight: "blob:photo" };

describe("truck create upload recovery", () => {
    it.each(["upload", "link", "retry"])("invalidates saved fleet data after a %s failure", async (failure) => {
        const client = getQueryClient();
        const keys = [queryKeys.trucks.all(), queryKeys.trucks.detail("saved-truck"), queryKeys.renewals()];
        for (const key of keys) client.setQueryData(key, []);
        fakeWeb().on("POST", "/api/go/v1/trucks", () => json(200, { data: { id: "saved-truck" } }))
            .on("PATCH", "/api/go/v1/trucks/saved-truck", () => goErr(503, "unavailable"));
        if (failure === "link") vi.mocked(uploadToGo).mockResolvedValue("trucks/saved-truck/photo.png");
        else vi.mocked(uploadToGo).mockRejectedValue(new Error("upload failed"));
        await expect(createTruck({
            values, pending: new Map([["blob:photo", new File(["image"], "photo.png", { type: "image/png" })]]),
            ...(failure === "retry" ? { createdId: "saved-truck" } : {}),
        })).rejects.toBeInstanceOf(TruckFilesSaveError);
        for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    });
    it("validates photo MIME and size before creating any record", async () => {
        const web = fakeWeb();
        for (const file of [new File(["pdf"], "photo.pdf", { type: "application/pdf" }), new File([], "empty.png", { type: "image/png" })]) {
            await expect(createTruck({ values, pending: new Map([["blob:photo", file]]) })).rejects.toMatchObject({ code: "invalid_argument" });
        }
        expect(web.calls).toHaveLength(0);
    });
    it("retains the created id before uploads fail and PATCHes it on retry", async () => {
        const web = fakeWeb().on("POST", "/api/go/v1/trucks", () => json(200, { data: { id: "saved-truck" } }))
            .on("PATCH", "/api/go/v1/trucks/saved-truck", () => json(200, { data: {} }));
        const onCreated = vi.fn();
        const save = { values, onCreated, pending: new Map([["blob:photo", new File(["image"], "photo.png", { type: "image/png" })]]) };
        vi.mocked(uploadToGo).mockRejectedValueOnce(new Error("upload failed")).mockResolvedValue("trucks/saved-truck/photo.png");
        await expect(createTruck(save)).rejects.toBeInstanceOf(TruckFilesSaveError);
        expect(onCreated).toHaveBeenCalledWith("saved-truck");
        expect(onCreated.mock.invocationCallOrder[0]).toBeLessThan(vi.mocked(uploadToGo).mock.invocationCallOrder[0]);
        expect(await createTruck({ ...save, createdId: "saved-truck" })).toBe("saved-truck");
        expect(web.count("POST", "/api/go/v1/trucks")).toBe(1);
        expect(web.count("PATCH", "/api/go/v1/trucks/saved-truck")).toBe(1);
    });
});
