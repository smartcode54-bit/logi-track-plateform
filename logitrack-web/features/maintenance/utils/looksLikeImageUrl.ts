/** Heuristic: URL is likely an image we can show in <img> / preview dialog. */
export function looksLikeImageUrl(url: string): boolean {
    if (/\.(jpe?g|png|gif|webp|bmp)(\?|#|$)/i.test(url)) return true;
    if (url.includes("firebasestorage.googleapis.com")) return true;
    return false;
}
