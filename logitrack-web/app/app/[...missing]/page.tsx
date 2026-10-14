import { notFound } from "next/navigation";

/**
 * Every `/app` path that no page answers (UI3D): "Page not found" inside the shell (app/app/not-found.tsx). Next sends
 * unmatched URLs to the root not-found, outside the shell, unless a segment catches them, so this one does. The status
 * comes from the edge gate, not from this `notFound()` (the shell renders on the client, lib/bff/edgeGate.ts): an
 * unmapped path is let through with 404; a path below a mapped prefix the principal may open (`/app/drivers/foo`)
 * gets the same page with status 200 (the gate cannot know no page is there); below a prefix the principal may not
 * open (`/app/payroll/foo`) the gate answers the 403 page first. Specific routes always win over a catch-all.
 *
 * Not a page of its own: the route maps (lib/routeCapabilities.test.ts, Go's TestEveryWebPageIsMapped) skip it.
 */
export default function MissingPage(): never {
    notFound();
}
