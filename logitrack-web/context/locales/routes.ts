/**
 * Which split translation namespaces a route needs (developer-spec.md §10.11 step 2, Appendix E
 * §E.7 row 9; TW4). A route group lists every split namespace whose keys its pages, or anything they
 * import, render; `routes.test.ts` walks the import graph of every `app/**` page and fails when a page
 * uses a namespace its group does not list, or a route outside a group would load one.
 *
 * Groups:
 * - `accounting`: the accounting pages and the billing utilities (`/app/utilities/*`, gated by
 *   `accounting:recompute_force`). The image-preview text the driver monitor shares with them lives
 *   in the base dictionary (`imagePreview.ts`).
 * - `driverMonitor`: the driver monitor and the task boards that open its trip editor
 *   (`app/app/driver-monitor/EditTripDetailsDialog.tsx`).
 */
import type { SplitNamespace } from "./load";

export const ROUTE_NAMESPACES: ReadonlyArray<{ prefix: string; namespaces: readonly SplitNamespace[] }> = [
    { prefix: "/app/accounting", namespaces: ["accounting"] },
    { prefix: "/app/utilities", namespaces: ["accounting"] },
    { prefix: "/app/driver-monitor", namespaces: ["driverMonitor"] },
    { prefix: "/app/first-mile", namespaces: ["driverMonitor"] },
    { prefix: "/app/line-haul", namespaces: ["driverMonitor"] },
    { prefix: "/app/job-assign", namespaces: ["driverMonitor"] },
];

const NONE: readonly SplitNamespace[] = [];

/** The split namespaces of `pathname`'s route group (none outside every group). */
export function namespacesForPath(pathname: string | null | undefined): readonly SplitNamespace[] {
    if (!pathname) return NONE;
    const group = ROUTE_NAMESPACES.find(({ prefix }) => pathname === prefix || pathname.startsWith(`${prefix}/`));
    return group ? group.namespaces : NONE;
}
