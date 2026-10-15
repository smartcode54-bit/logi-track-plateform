import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeWeb, json } from "@/test-utils/fakeWeb";
import { PartialCreateError, UPLOAD_MAX_BYTES_DEFAULT } from "@/lib/goUpload";
import { resetQueryClientForTests } from "@/lib/queryClient";
import { createDriver, driverFormValues, type DriverDetailDTO } from "./drivers";
import { assertDriverUpload, driverUploadAccept, DriverUploadError, type DriverUploadPurpose } from "./driverUploads";
import en from "@/context/locales/en/drivers";
import th from "@/context/locales/th/drivers";
import enAssignments from "@/context/locales/en/assignments";
import thAssignments from "@/context/locales/th/assignments";
import { driverFormResolver } from "../components/driverFormResolver";

const driver = {
    id: "driver-1", tenantId: "own-1", displayName: "Somchai Test", firstName: "Somchai", lastName: "Test", fullNameTh: "สมชาย ใจดี",
    mobile: "0812345678", email: "driver@example.test", status: "active", employmentType: "full_time",
    birthDate: "1990-01-01", idCard: "1234567890123", truckLicenseId: "12345678", customerDriverIds: [], statusHistory: [],
} as unknown as DriverDetailDTO;
const values = driverFormValues(driver);

afterEach(() => { resetQueryClientForTests(); vi.unstubAllGlobals(); });

describe("driver upload contract and recovery", () => {
    it.each(["driver_profile", "driver_id_card", "driver_license"] as DriverUploadPurpose[])("checks %s MIME, size and input accept against Go storage", (purpose) => {
        for (const type of ["image/jpeg", "image/png", "image/webp"]) {
            expect(() => assertDriverUpload(new File(["image"], "image", { type }), purpose)).not.toThrow();
        }
        const pdf = new File(["pdf"], "id.pdf", { type: "application/pdf" });
        if (purpose === "driver_profile") expect(() => assertDriverUpload(pdf, purpose)).toThrow(DriverUploadError);
        else expect(() => assertDriverUpload(pdf, purpose)).not.toThrow();
        expect(() => assertDriverUpload(new File(["gif"], "image.gif", { type: "image/gif" }), purpose)).toThrow(DriverUploadError);
        expect(driverUploadAccept(purpose)).not.toContain("*");
        const large = new File(["x"], "large.png", { type: "image/png" });
        Object.defineProperty(large, "size", { value: UPLOAD_MAX_BYTES_DEFAULT + 1 });
        expect(() => assertDriverUpload(large, purpose)).toThrow(DriverUploadError);
        expect(() => assertDriverUpload(new File([], "empty.png", { type: "image/png" }), purpose)).toThrow(DriverUploadError);
    });

    it.each(["profile", "idCard", "license"] as const)("refuses invalid %s before POST", async (kind) => {
        const web = fakeWeb();
        const files = { profile: null, idCard: null, license: null, [kind]: new File(["x"], "bad.svg", { type: "image/svg+xml" }) };
        await expect(createDriver({ values, files })).rejects.toBeInstanceOf(DriverUploadError);
        expect(web.calls).toHaveLength(0);
    });

    it.each(["presign", "link"])("retains the saved record after %s fails and retries through PATCH", async (failAt) => {
        let failing = true;
        const web = fakeWeb()
            .on("POST", "/api/go/v1/drivers", () => json(201, { data: driver }))
            .on("POST", "/api/go/v1/uploads/presign", () => failing && failAt === "presign"
                ? json(503, { error: { code: "unavailable" } })
                : json(200, { data: { key: "drivers/driver-1/profile.png", url: "/v1/uploads/local/profile", method: "PUT" } }))
            .on("PUT", "/api/go/v1/uploads/local/profile", () => json(200, {}))
            .on("PATCH", "/api/go/v1/drivers/driver-1", () => failing && failAt === "link"
                ? json(503, { error: { code: "unavailable" } }) : json(200, { data: driver }));
        const files = { profile: new File(["image"], "profile.png", { type: "image/png" }), idCard: null, license: null };
        let saved: DriverDetailDTO | undefined;
        try { await createDriver({ values, files }); } catch (error) {
            expect(error).toBeInstanceOf(PartialCreateError);
            saved = (error as PartialCreateError<DriverDetailDTO>).entity;
        }
        expect(saved?.id).toBe(driver.id);
        failing = false;
        await expect(createDriver({ values: { ...values, mobile: "0899999999" }, files, existing: saved })).resolves.toBe(driver.id);
        expect(web.calls.filter((c) => c.method === "POST" && c.url === "/api/go/v1/drivers")).toHaveLength(1);
        const patches = web.calls.filter((c) => c.method === "PATCH");
        expect(patches.at(-1)?.body).toMatchObject({ mobile: "0899999999", profileKey: "drivers/driver-1/profile.png" });
    });
});

describe("driver validation translations", () => {
    it("keeps schema locale keys in RHF for translation at render time", async () => {
        const result = await driverFormResolver()(
            { ...values, firstName: "", birthDate: undefined as never, email: "broken" }, undefined,
            { fields: {}, shouldUseNativeValidation: false },
        );
        expect(result.errors).toMatchObject({
            firstName: { message: "drivers.validation.firstName" },
            birthDate: { message: "drivers.validation.birthDate" },
            email: { message: "drivers.validation.email" },
        });
    });

    it("keeps en/th key parity for drivers and assignments", () => {
        expect(Object.keys(en).sort()).toEqual(Object.keys(th).sort());
        expect(Object.keys(enAssignments).sort()).toEqual(Object.keys(thAssignments).sort());
    });
});
