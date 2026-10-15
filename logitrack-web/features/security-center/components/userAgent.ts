/** A short device label; the original user agent remains available as evidence. */
export function userAgentLabel(value: string | null | undefined, unknownBrowser: string): string {
    const ua = value ?? "";
    const browser = /Edg(?:e|A|iOS)?\//.test(ua) ? "Edge"
        : /(?:OPR|Opera)\//.test(ua) ? "Opera"
        : /(?:Firefox|FxiOS)\//.test(ua) ? "Firefox"
        : /(?:Chrome|CriOS)\//.test(ua) ? "Chrome"
        : /Safari\//.test(ua) ? "Safari" : unknownBrowser;
    const os = /iPhone|iPad|iPod/.test(ua) || /Macintosh.*Mobile\//.test(ua) ? "iOS"
        : /Android/.test(ua) ? "Android"
        : /Windows/.test(ua) ? "Windows"
        : /Mac OS X|Macintosh/.test(ua) ? "macOS"
        : /CrOS/.test(ua) ? "ChromeOS"
        : /Linux/.test(ua) ? "Linux" : "";
    return os ? `${browser} · ${os}` : browser;
}
