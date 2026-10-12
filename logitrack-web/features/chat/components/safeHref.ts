/**
 * The link of a chat image: Go's presigned GET (an absolute http(s) URL) or the local storage backend's
 * same-origin `/media/...` path; anything else is not a link (never a `javascript:` or data URL).
 */
export function safeHref(url: string | null | undefined): string | null {
    if (!url) return null;
    if (/^https?:\/\//i.test(url)) return url;
    if (url.startsWith("/") && !url.startsWith("//")) return url;
    return null;
}
