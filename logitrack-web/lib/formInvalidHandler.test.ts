import { describe, it, expect, vi, beforeEach } from "vitest";
import type { FieldErrors, UseFormReturn } from "react-hook-form";

const toastError = vi.fn();
vi.mock("sonner", () => ({ toast: { error: (msg: string) => toastError(msg) } }));

import { createInvalidHandler } from "./formInvalidHandler";

/** Echo the key and params back so assertions can read what the handler passed. */
const t = (key: string, params?: Record<string, string | number>) =>
    `${key}|count=${params?.count}|field=${params?.field}`;

function makeForm(setFocus: (name: string) => void = () => {}) {
    return { setFocus } as unknown as UseFormReturn<Record<string, unknown>>;
}

describe("createInvalidHandler", () => {
    beforeEach(() => {
        toastError.mockClear();
    });

    it("names the first field's message, not its key", () => {
        const errors = {
            fullNameTh: { type: "too_small", message: "Thai full name is required" },
        } as unknown as FieldErrors;

        createInvalidHandler(makeForm(), t)(errors);

        expect(toastError).toHaveBeenCalledWith(
            "common.toast.validationError|count=1|field=Thai full name is required"
        );
    });

    it("walks into nested groups instead of reporting the group object", () => {
        // RHF nests errors under object fields — the first key here is a group, not an error.
        const errors = {
            customerDriverIds: {
                SPX: {
                    appId: { type: "invalid_type", message: "App ID is required" },
                },
            },
        } as unknown as FieldErrors;

        const focused: string[] = [];
        createInvalidHandler(makeForm((n) => focused.push(n)), t)(errors);

        expect(toastError).toHaveBeenCalledWith(
            "common.toast.validationError|count=1|field=App ID is required"
        );
        expect(focused).toEqual(["customerDriverIds.SPX.appId"]);
    });

    it("counts leaves, not top-level groups", () => {
        const errors = {
            mobile: { type: "too_small", message: "Mobile number is required" },
            customerDriverIds: {
                SPX: {
                    appId: { type: "invalid_type", message: "App ID is required" },
                    workId: { type: "invalid_type", message: "Work ID is required" },
                },
            },
        } as unknown as FieldErrors;

        createInvalidHandler(makeForm(), t)(errors);

        expect(toastError).toHaveBeenCalledWith(
            "common.toast.validationError|count=3|field=Mobile number is required"
        );
    });

    it("falls back to the field path when a leaf carries no message", () => {
        const errors = { province: { type: "too_small" } } as unknown as FieldErrors;

        createInvalidHandler(makeForm(), t)(errors);

        expect(toastError).toHaveBeenCalledWith(
            "common.toast.validationError|count=1|field=province"
        );
    });

    it("survives setFocus throwing on non-input fields (DatePicker)", () => {
        const errors = { birthDate: { type: "invalid_date", message: "Birth date required" } } as unknown as FieldErrors;
        const form = makeForm(() => {
            throw new Error("not a registered input");
        });

        expect(() => createInvalidHandler(form, t)(errors)).not.toThrow();
        expect(toastError).toHaveBeenCalledWith(
            "common.toast.validationError|count=1|field=Birth date required"
        );
    });

    it("stays silent when there is nothing to report", () => {
        createInvalidHandler(makeForm(), t)({} as FieldErrors);
        expect(toastError).not.toHaveBeenCalled();
    });
});
