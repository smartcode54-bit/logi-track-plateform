import { notFound } from "next/navigation";

/**
 * Every `/app` path that no page answers (UI3D): "Page not found" inside the shell (app/app/not-found.tsx), HTTP 404.
 * Next sends unmatched URLs to the root not-found, outside the shell, unless a segment catches them, so this one does:
 * a path under a mapped prefix (`/app/trucks/x/y`) lands here, and so does every unmapped path, which the edge gate
 * lets through with the status already set to 404 (lib/bff/edgeGate.ts: the shell renders on the client, so this
 * `notFound()` cannot set it). Specific routes always win over a catch-all.
 *
 * Not a page of its own: the route maps (lib/routeCapabilities.test.ts, Go's TestEveryWebPageIsMapped) skip it.
 */
export default function MissingPage(): never {
    notFound();
}
