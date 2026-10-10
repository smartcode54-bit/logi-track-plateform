// BFF session route (R38; developer-spec.md §10.4, Appendix E §E.8.3). Logic: lib/bff/authRoutes.ts.
import { firebaseToken } from "@/lib/bff/authRoutes";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const POST = (request: Request) => firebaseToken(request);
