/**
 * Where a sign-in or a refresh bounce may send the browser next (developer-spec.md §10.4, Appendix E
 * §E.8.3): a same-origin path in the protected `/app` route group (the `proxy.ts` matcher), else
 * `/app`, whose gate sends the principal to its role home (R89). Absolute URLs, `//host`,
 * backslashes and control characters are refused, so a crafted `?next=` can never leave the origin.
 *
 * Shared by the BFF (`GET /api/auth/refresh?next=`, lib/bff/authRoutes.ts) and the login page; no
 * server code here.
 */

/** Where `next` lands when it is missing or unacceptable. */
export const DEFAULT_NEXT = "/app";

export function safeNext(raw: string | null | undefined): string {
    if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.includes("\\")) return DEFAULT_NEXT;
    for (let i = 0; i < raw.length; i++) {
        const c = raw.charCodeAt(i);
        if (c < 0x20 || c === 0x7f) return DEFAULT_NEXT;
    }
    let url: URL;
    try {
        url = new URL(raw, "http://web.invalid");
    } catch {
        return DEFAULT_NEXT;
    }
    if (url.origin !== "http://web.invalid") return DEFAULT_NEXT;
    if (url.pathname !== "/app" && !url.pathname.startsWith("/app/")) return DEFAULT_NEXT;
    return url.pathname + url.search;
}
