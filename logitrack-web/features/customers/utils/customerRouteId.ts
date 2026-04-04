/**
 * Resolves Firestore customer document id from the URL path.
 * With `output: "export"` and Firebase Hosting rewrites to `placeholder.html`,
 * `useParams().id` can remain `"placeholder"` while the address bar shows the real id.
 */
export function getCustomerIdFromPathname(pathname: string | null | undefined): string | undefined {
    if (!pathname) return undefined;
    const parts = pathname.split("/").filter(Boolean);
    const i = parts.indexOf("customers");
    if (i < 0 || !parts[i + 1]) return undefined;
    if (parts[i + 1] === "new") return undefined;
    if (parts[i + 2] === "edit") return parts[i + 1];
    if (parts[i + 2]) return undefined;
    return parts[i + 1];
}
