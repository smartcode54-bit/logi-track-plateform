// The `/app/*` edge gate (Next 16 `proxy.ts`, Node.js runtime; developer-spec.md §10.5, Appendix E
// §E.8.4; R39, R89): verifies the `lt_at` cookie against the Go JWKS, reads the effective capabilities
// from the internal `GET /v1/me` (cached per session, claims version and tenant for 60 s) and decides
// the route from lib/routeCapabilities.ts before any page code or data request runs. Public routes
// (`/`, `/login`, `/join-network`, `/about`, `/api/*`) are outside the matcher. Logic: lib/bff/edgeGate.ts.
import { NextResponse, type NextRequest } from "next/server";

import { edgeGate } from "@/lib/bff/edgeGate";

export async function proxy(request: NextRequest): Promise<Response> {
    return (await edgeGate(request)) ?? NextResponse.next();
}

export const config = {
    matcher: ["/app/:path*"],
};
