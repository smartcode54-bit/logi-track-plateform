/**
 * A fake `indexedDB` for tests (jsdom has none): records every `deleteDatabase` call and returns a plain
 * request object the code under test may set `onblocked` / `onerror` on. Install it with
 * `vi.stubGlobal("indexedDB", fakeIndexedDB())`. Used by the TW7 transition cleanup of the Firebase
 * session a pre-TW7 build persisted (lib/legacyFirebaseCleanup.ts).
 */
export interface FakeIndexedDB {
    /** The names passed to `deleteDatabase`, in call order (a refused call is not recorded). */
    deleted: string[];
    /** The request objects returned, in call order. */
    requests: Record<string, unknown>[];
    deleteDatabase(name: string): Record<string, unknown>;
}

export function fakeIndexedDB(options: { throwOn?: string } = {}): FakeIndexedDB {
    const deleted: string[] = [];
    const requests: Record<string, unknown>[] = [];
    return {
        deleted,
        requests,
        deleteDatabase(name: string) {
            if (name === options.throwOn) throw new DOMException("refused", "SecurityError");
            deleted.push(name);
            const request: Record<string, unknown> = {};
            requests.push(request);
            return request;
        },
    };
}
