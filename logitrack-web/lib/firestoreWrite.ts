/**
 * Small helpers shared by every Firestore write path.
 */

/**
 * Remove `undefined` values from an object before writing to Firestore.
 *
 * Firestore throws "Unsupported field value: undefined" if any field is undefined, and an optional
 * form field that the user left alone arrives as exactly that. Empty string (`""`) is kept — that is
 * a real value meaning "cleared", and stripping it would silently ignore a deliberate edit.
 */
export function stripUndefined<T extends Record<string, unknown>>(obj: T): Partial<T> {
    return Object.fromEntries(
        Object.entries(obj).filter(([, v]) => v !== undefined)
    ) as Partial<T>;
}
