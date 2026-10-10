// Liveness of the standalone Next.js server (developer-spec.md §15.1, TW2): the compose
// healthcheck and the deploy job poll it. It reads nothing and calls nothing (no Go, no
// Firebase), so it answers while dependencies are down and reveals no state.
export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export function GET(): Response {
  return Response.json({ status: "ok" }, { headers: { "Cache-Control": "no-store" } });
}

export function HEAD(): Response {
  return new Response(null, { headers: { "Cache-Control": "no-store" } });
}
