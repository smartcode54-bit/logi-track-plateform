/** Inspect the object key as well as the path; signed and BFF URLs can encode the extension. */
export function looksLikeImageUrl(url: string): boolean {
    try {
        const parsed = new URL(url, "https://local.invalid");
        const path = decodeURIComponent(parsed.searchParams.get("key") ?? parsed.pathname);
        return /\.(jpe?g|png|gif|webp|bmp)$/i.test(path);
    } catch {
        return false;
    }
}
