/**
 * Lazy translation dictionaries (developer-spec.md §10.11, Appendix E §E.7 row 9).
 *
 * Step 1 (TW9): each language's base dictionary is one chunk (`./en`, `./th`), loaded with
 * `import()` only when that language is active, so a route's initial JS holds no dictionary and the
 * browser fetches exactly the active one; a toggle fetches the other once.
 *
 * Step 2 (TW4): the two largest namespaces, `accounting` (about a quarter of each dictionary) and
 * `driverMonitor`, are not in the base chunk. Each is its own chunk per language, loaded only on the
 * routes of its group (`./routes.ts`), so the other routes neither download nor parse them.
 *
 * `scripts/bundle-report.mjs --check` fails the build when a dictionary module reaches a route's
 * initial JS, or when a split namespace reaches a base dictionary chunk.
 */

export const LANGUAGES = ["en", "th"] as const;
export type Language = (typeof LANGUAGES)[number];
export const DEFAULT_LANGUAGE: Language = "en";

/** Translation key -> text, every namespace of one language merged. */
export type Dictionary = Record<string, string>;

/** Namespaces left out of the base dictionary and loaded per route group (`./routes.ts`). */
export const SPLIT_NAMESPACES = ["accounting", "driverMonitor"] as const;
export type SplitNamespace = (typeof SPLIT_NAMESPACES)[number];

type Loader = () => Promise<{ default: Dictionary }>;

const loaders: Record<Language, Loader> = {
    en: () => import(/* webpackChunkName: "locale-en" */ "./en"),
    th: () => import(/* webpackChunkName: "locale-th" */ "./th"),
};

const namespaceLoaders: Record<Language, Record<SplitNamespace, Loader>> = {
    en: {
        accounting: () => import(/* webpackChunkName: "locale-en-accounting" */ "./en/accounting"),
        driverMonitor: () => import(/* webpackChunkName: "locale-en-driverMonitor" */ "./en/driverMonitor"),
    },
    th: {
        accounting: () => import(/* webpackChunkName: "locale-th-accounting" */ "./th/accounting"),
        driverMonitor: () => import(/* webpackChunkName: "locale-th-driverMonitor" */ "./th/driverMonitor"),
    },
};

const pending = new Map<string, Promise<Dictionary>>();

export function isLanguage(value: unknown): value is Language {
    return typeof value === "string" && (LANGUAGES as readonly string[]).includes(value);
}

function once(key: string, loader: Loader): Promise<Dictionary> {
    let promise = pending.get(key);
    if (!promise) {
        promise = loader().then((m) => m.default);
        pending.set(key, promise);
        promise.catch(() => pending.delete(key));
    }
    return promise;
}

/**
 * The base dictionary of `language` (every namespace except the split ones), fetched once per page
 * load; concurrent and later calls share the same promise. A failed fetch is forgotten so the next
 * call retries.
 */
export function loadDictionary(language: Language): Promise<Dictionary> {
    return once(language, loaders[language]);
}

/** One split namespace of `language`, fetched once per page load (same rules as `loadDictionary`). */
export function loadNamespace(language: Language, namespace: SplitNamespace): Promise<Dictionary> {
    return once(`${language}:${namespace}`, namespaceLoaders[language][namespace]);
}
