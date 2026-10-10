"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import {
    DEFAULT_LANGUAGE,
    isLanguage,
    loadDictionary,
    loadNamespace,
    type Dictionary,
    type Language,
    type SplitNamespace,
} from "./locales/load";
import { namespacesForPath } from "./locales/routes";

type TParams = Record<string, string | number>;

type LanguageContextType = {
    language: Language;
    setLanguage: (lang: Language) => void;
    t: (key: string, fallbackOrParams?: string | TParams) => string;
};

const LanguageContext = createContext<LanguageContextType | undefined>(undefined);

type NamespaceStatus = "ready" | "loading" | "failed";

type NamespaceContextType = {
    /** Marks `names` as needed while the caller is mounted; returns the release function. */
    requireNamespaces: (names: readonly SplitNamespace[]) => () => void;
    loaded: ReadonlySet<SplitNamespace>;
    failed: ReadonlySet<SplitNamespace>;
};

const NamespaceContext = createContext<NamespaceContextType | undefined>(undefined);

const STORAGE_KEY = "language";

function storedLanguage(): Language {
    try {
        const saved = window.localStorage.getItem(STORAGE_KEY);
        return isLanguage(saved) ? saved : DEFAULT_LANGUAGE;
    } catch {
        return DEFAULT_LANGUAGE;
    }
}

// Start fetching the stored language's dictionary, and the split namespaces of the route being
// opened, as soon as this module runs in the browser: in parallel with hydration instead of after the
// provider's first effect. Failures surface there.
if (typeof window !== "undefined") {
    const language = storedLanguage();
    loadDictionary(language).catch(() => undefined);
    for (const ns of namespacesForPath(window.location.pathname)) loadNamespace(language, ns).catch(() => undefined);
}

function translate(dictionary: Dictionary, key: string, fallbackOrParams?: string | TParams): string {
    const value = dictionary[key];
    const str = value !== undefined && value !== "" ? value : (typeof fallbackOrParams === "string" ? fallbackOrParams : key);
    if (typeof fallbackOrParams === "object" && fallbackOrParams !== null) {
        return Object.entries(fallbackOrParams).reduce(
            (acc, [k, v]) => acc.replace(new RegExp(`\\{${k}\\}`, "g"), String(v)),
            str
        );
    }
    return str;
}

/** Same screen as the /app layout's auth wait, so /app routes show one continuous loader. */
function LanguageLoading() {
    return (
        <div className="min-h-screen bg-background flex flex-col items-center justify-center" aria-busy="true">
            <div className="flex flex-col items-center gap-4">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
                <div className="text-center text-muted-foreground">Loading / กำลังโหลด...</div>
            </div>
        </div>
    );
}

function LanguageLoadError() {
    return (
        <div className="min-h-screen bg-background flex flex-col items-center justify-center gap-4 p-6 text-center" role="alert">
            <p className="text-muted-foreground">
                Could not load the interface language. / โหลดภาษาของหน้าจอไม่สำเร็จ
            </p>
            <button
                type="button"
                className="rounded-md border px-4 py-2 text-sm hover:bg-accent"
                onClick={() => window.location.reload()}
            >
                Reload / โหลดใหม่
            </button>
        </div>
    );
}

type Loaded = {
    language: Language;
    base: Dictionary;
    /** Split namespaces merged for `language` (developer-spec.md §10.11 step 2). */
    namespaces: Partial<Record<SplitNamespace, Dictionary>>;
};

const EMPTY_SET: ReadonlySet<SplitNamespace> = new Set();

/**
 * Translation context. Only the active language's dictionary is ever loaded (`./locales/load.ts`,
 * developer-spec.md §10.11): the stored preference, else English, then the other language once the
 * user toggles. Children render once that dictionary is in, so the server and the first client
 * render show the loading screen and hydration never depends on the stored language.
 *
 * The base dictionary leaves out the split namespaces (`accounting`, `driverMonitor`); the route
 * groups that render them ask for them through `useLocaleNamespaces` (the `/app` layout's
 * `RouteNamespaces`), and a language toggle loads the new language's base and the namespaces in use
 * before switching, so the screen never shows a raw key; if any of them fails, the toggle keeps the
 * current language and the mounted page (its unsaved state included).
 *
 * `t` changes only when the language or the merged dictionary does (TW4): effects must not list it as
 * a dependency (a toggle would re-run them); callbacks read it through `useEffectEvent`.
 */
export function LanguageProvider({ children }: { children: React.ReactNode }) {
    const [state, setState] = useState<Loaded | null>(null);
    // null: the stored preference. `fallback` marks the default language tried after it failed.
    const [choice, setChoice] = useState<{ language: Language; fallback: boolean } | null>(null);
    const [failed, setFailed] = useState(false);
    const [nsFailed, setNsFailed] = useState<ReadonlySet<SplitNamespace>>(EMPTY_SET);

    // Namespaces wanted by mounted route groups, reference-counted; `active` is their sorted list.
    const counts = useRef(new Map<SplitNamespace, number>());
    const [active, setActive] = useState<readonly SplitNamespace[]>([]);

    const requireNamespaces = useCallback((names: readonly SplitNamespace[]) => {
        const publish = () => setActive([...counts.current.keys()].sort());
        for (const n of names) counts.current.set(n, (counts.current.get(n) ?? 0) + 1);
        publish();
        return () => {
            for (const n of names) {
                const c = (counts.current.get(n) ?? 0) - 1;
                if (c > 0) counts.current.set(n, c);
                else counts.current.delete(n);
            }
            publish();
        };
    }, []);

    // One load per choice: the base dictionary and the namespaces in use, switched in together. The
    // cleanup drops a load that a newer choice overtook, so the language asked for last always wins,
    // and the current language stays on screen until the new one is in.
    useEffect(() => {
        const language = choice?.language ?? storedLanguage();
        // A user's toggle (not the first load, not the fallback after it) has a language on screen to keep.
        const isToggle = choice !== null && !choice.fallback;
        // Child effects (the route group's request) run before this one, so the map is current.
        const wanted = [...counts.current.keys()];
        let current = true;
        Promise.all([loadDictionary(language), Promise.allSettled(wanted.map((ns) => loadNamespace(language, ns)))]).then(
            ([base, settled]) => {
                if (!current) return;
                const failedNs = wanted.filter((_, i) => settled[i].status === "rejected");
                if (isToggle && failedNs.length > 0) {
                    // Switching without the mounted group's namespace would unmount its page (and any
                    // unsaved state in it) for a spinner or an error screen: keep the language on screen,
                    // as for a failed base dictionary; the user can toggle again.
                    console.error(`[language] switching to "${language}" failed: ${failedNs.join(",")} did not load`);
                    return;
                }
                // The first load and the fallback commit the base anyway: the shell renders, and the group
                // shows its failed state (RouteNamespaces) while the effect below retries its chunk.
                setState((prev) => {
                    // Re-choosing the current language keeps the same value, so `t` stays the same too.
                    if (prev?.language === language && prev.base === base) return prev;
                    const namespaces: Loaded["namespaces"] = {};
                    settled.forEach((result, i) => {
                        if (result.status === "fulfilled") namespaces[wanted[i]] = result.value;
                    });
                    return { language, base, namespaces };
                });
            },
            (error) => {
                if (!current) return;
                console.error(`[language] loading "${language}" failed`, error);
                if (choice === null && language !== DEFAULT_LANGUAGE) {
                    setChoice({ language: DEFAULT_LANGUAGE, fallback: true });
                } else if (choice === null || choice.fallback) {
                    setFailed(true);
                }
                // A failed toggle keeps the language on screen; the user can toggle again. A toggle
                // switches only when the base dictionary and every namespace of the mounted group loaded.
            }
        );
        return () => {
            current = false;
        };
    }, [choice]);

    // Namespaces a route group needs that the current language has not merged yet.
    const language = state?.language;
    const missingKey = state ? active.filter((ns) => !state.namespaces[ns]).join(",") : "";
    useEffect(() => {
        if (!language || missingKey === "") return;
        let current = true;
        for (const ns of missingKey.split(",") as SplitNamespace[]) {
            loadNamespace(language, ns).then(
                (dictionary) => {
                    if (!current) return;
                    setState((prev) =>
                        prev && prev.language === language && !prev.namespaces[ns]
                            ? { ...prev, namespaces: { ...prev.namespaces, [ns]: dictionary } }
                            : prev
                    );
                    setNsFailed((prev) => (prev.has(ns) ? new Set([...prev].filter((n) => n !== ns)) : prev));
                },
                (error) => {
                    if (!current) return;
                    console.error(`[language] loading the "${ns}" namespace of "${language}" failed`, error);
                    setNsFailed((prev) => (prev.has(ns) ? prev : new Set([...prev, ns])));
                }
            );
        }
        return () => {
            current = false;
        };
    }, [language, missingKey]);

    const setLanguage = useCallback((lang: Language) => {
        try {
            window.localStorage.setItem(STORAGE_KEY, lang);
        } catch {
            // Storage can be unavailable (private mode); the choice then lasts for this page only.
        }
        setChoice({ language: lang, fallback: false });
    }, []);

    const messages = useMemo<Dictionary | null>(() => {
        if (!state) return null;
        const parts = Object.values(state.namespaces);
        return parts.length === 0 ? state.base : Object.assign({}, state.base, ...parts);
    }, [state]);

    const t = useCallback<LanguageContextType["t"]>(
        (key, fallbackOrParams) => translate(messages ?? {}, key, fallbackOrParams),
        [messages]
    );

    const value = useMemo<LanguageContextType | null>(
        () => (language ? { language, setLanguage, t } : null),
        [language, setLanguage, t]
    );

    const loaded = useMemo<ReadonlySet<SplitNamespace>>(
        () => (state ? new Set(Object.keys(state.namespaces) as SplitNamespace[]) : EMPTY_SET),
        [state]
    );
    const nsValue = useMemo<NamespaceContextType>(
        () => ({ requireNamespaces, loaded, failed: nsFailed }),
        [requireNamespaces, loaded, nsFailed]
    );

    if (!value) return failed ? <LanguageLoadError /> : <LanguageLoading />;
    return (
        <LanguageContext.Provider value={value}>
            <NamespaceContext.Provider value={nsValue}>{children}</NamespaceContext.Provider>
        </LanguageContext.Provider>
    );
}

export function useLanguage() {
    const context = useContext(LanguageContext);
    if (context === undefined) {
        throw new Error("useLanguage must be used within a LanguageProvider");
    }
    return context;
}

/**
 * Asks for the split namespaces `names` while the caller is mounted (a route group, see
 * `./locales/routes.ts`) and reports whether the active language has them: `ready` once merged into
 * `t`, `failed` when a chunk could not be fetched.
 */
export function useLocaleNamespaces(names: readonly SplitNamespace[]): NamespaceStatus {
    const context = useContext(NamespaceContext);
    if (context === undefined) {
        throw new Error("useLocaleNamespaces must be used within a LanguageProvider");
    }
    const { requireNamespaces, loaded, failed } = context;
    const key = names.join(",");
    useEffect(() => {
        if (key === "") return;
        return requireNamespaces(key.split(",") as SplitNamespace[]);
    }, [key, requireNamespaces]);
    if (names.every((n) => loaded.has(n))) return "ready";
    return names.some((n) => failed.has(n)) ? "failed" : "loading";
}
