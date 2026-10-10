/**
 * Lazy translation dictionaries (developer-spec.md §10.11, Appendix E §E.7 row 9).
 *
 * Each language is one chunk (`./en`, `./th`), loaded with `import()` only when that language is
 * active, so a route's initial JS holds no dictionary and the browser fetches exactly the active
 * one; a toggle fetches the other once. `scripts/bundle-report.mjs --check` fails the build when a
 * dictionary module reaches a route's initial JS.
 */

export const LANGUAGES = ["en", "th"] as const;
export type Language = (typeof LANGUAGES)[number];
export const DEFAULT_LANGUAGE: Language = "en";

/** Translation key -> text, every namespace of one language merged. */
export type Dictionary = Record<string, string>;

const loaders: Record<Language, () => Promise<{ default: Dictionary }>> = {
    en: () => import(/* webpackChunkName: "locale-en" */ "./en"),
    th: () => import(/* webpackChunkName: "locale-th" */ "./th"),
};

const pending = new Map<Language, Promise<Dictionary>>();

export function isLanguage(value: unknown): value is Language {
    return typeof value === "string" && (LANGUAGES as readonly string[]).includes(value);
}

/**
 * The dictionary of `language`, fetched once per page load; concurrent and later calls share the
 * same promise. A failed fetch is forgotten so the next call retries.
 */
export function loadDictionary(language: Language): Promise<Dictionary> {
    let promise = pending.get(language);
    if (!promise) {
        promise = loaders[language]().then((m) => m.default);
        pending.set(language, promise);
        promise.catch(() => pending.delete(language));
    }
    return promise;
}
