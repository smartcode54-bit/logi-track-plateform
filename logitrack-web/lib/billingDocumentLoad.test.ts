import { beforeEach, describe, expect, it, vi } from "vitest";

// The real render module pulls in jspdf and xlsx-js-style; these tests only need its loading hooks.
const renderModule = vi.hoisted(() => ({
    preloadBillingRender: vi.fn<() => Promise<void>>(),
    downloadBillingZip: vi.fn(),
}));
vi.mock("./billingDocumentRender", () => renderModule);

import { loadBillingRender, loadThenSaveThenRender } from "./billingDocumentLoad";

describe("loadThenSaveThenRender", () => {
    it("does not save the statement when the renderer fails to load", async () => {
        const chunkError = Object.assign(new Error("Loading chunk 6423 failed."), { name: "ChunkLoadError" });
        const save = vi.fn(async () => "CJSF-202609-004");
        const render = vi.fn(async () => {});

        await expect(
            loadThenSaveThenRender({ load: () => Promise.reject(chunkError), save, render }),
        ).rejects.toBe(chunkError);
        expect(save).not.toHaveBeenCalled();
        expect(render).not.toHaveBeenCalled();
    });

    it("loads, then saves, then renders with the saved invoice number", async () => {
        const calls: string[] = [];
        const renderer = { id: "renderer" };
        const render = vi.fn(async () => {
            calls.push("render");
        });

        await loadThenSaveThenRender({
            load: async () => {
                calls.push("load");
                return renderer;
            },
            save: async () => {
                calls.push("save");
                return "CJSF-202609-004";
            },
            render,
        });

        expect(calls).toEqual(["load", "save", "render"]);
        expect(render).toHaveBeenCalledWith(renderer, "CJSF-202609-004");
    });

    it("still renders when the save reported no invoice number", async () => {
        const render = vi.fn(async () => {});
        await loadThenSaveThenRender({ load: async () => "renderer", save: async () => undefined, render });
        expect(render).toHaveBeenCalledWith("renderer", undefined);
    });
});

describe("loadBillingRender", () => {
    beforeEach(() => {
        renderModule.preloadBillingRender.mockReset();
    });

    it("waits for jszip and the font before handing out the renderer", async () => {
        renderModule.preloadBillingRender.mockResolvedValue(undefined);
        const render = await loadBillingRender();
        expect(renderModule.preloadBillingRender).toHaveBeenCalledTimes(1);
        expect(render.downloadBillingZip).toBe(renderModule.downloadBillingZip);
    });

    it("rejects when the preload fails, so the caller saves nothing", async () => {
        renderModule.preloadBillingRender.mockRejectedValue(new Error("[pdfThai] Fetch failed (404)"));
        await expect(loadBillingRender()).rejects.toThrow("Fetch failed");
    });
});
