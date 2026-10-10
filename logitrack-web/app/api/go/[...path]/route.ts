// The generic BFF proxy (developer-spec.md §10.3, Appendix E §E.8.2; R37, R38): the browser's only
// way to Go. `/api/go/v1/<rest>` -> `{GO_API_INTERNAL_URL}/v1/<rest>` with the `lt_at` cookie as the
// bearer. No business logic, never a refresh; see lib/bff/goProxy.ts.
import { errorResponse, requestIdFor } from "@/lib/bff/http";
import { PROXY_METHODS, proxyToGo } from "@/lib/bff/goProxy";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function handler(request: Request): Promise<Response> {
    return proxyToGo(request);
}

export const GET = handler;
export const HEAD = handler;
export const POST = handler;
export const PUT = handler;
export const PATCH = handler;
export const DELETE = handler;

/** No CORS preflight: the browser calls the BFF same-origin only. */
export function OPTIONS(request: Request): Response {
    return errorResponse(405, "method_not_allowed", "method not allowed", requestIdFor(request), {}, { Allow: PROXY_METHODS.join(", ") });
}
