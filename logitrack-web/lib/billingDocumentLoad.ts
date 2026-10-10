/**
 * Loading and ordering for the Billing Document download (developer-spec.md §10.11, Appendix E
 * §E.7.1 rows 3-5). The render module carries jspdf and xlsx-js-style, so it is fetched only when a
 * document is generated; that fetch can fail (a redeploy replaced the chunk the open tab knows, or
 * the network dropped). Saving the statement allocates an invoice number and writes a draft, which
 * cannot be undone from here, so everything that can fail on the network is loaded first.
 */
import type * as BillingRender from "./billingDocumentRender";

export type BillingRenderModule = typeof BillingRender;

/** Loads the render module and what it would otherwise fetch half-way through (jszip, Sarabun). */
export async function loadBillingRender(): Promise<BillingRenderModule> {
    const render = await import("./billingDocumentRender");
    await render.preloadBillingRender();
    return render;
}

/**
 * Runs the three steps of a Billing Document download in the only safe order: `load` the renderer,
 * then `save` the statement (returns its invoice number, or undefined when the save failed and the
 * caller still wants the files), then `render` with that number. A failed `load` rejects before
 * anything is saved.
 */
export async function loadThenSaveThenRender<R>(steps: {
    load: () => Promise<R>;
    save: () => Promise<string | undefined>;
    render: (renderer: R, invoiceNumber: string | undefined) => Promise<void>;
}): Promise<void> {
    const renderer = await steps.load();
    const invoiceNumber = await steps.save();
    await steps.render(renderer, invoiceNumber);
}
