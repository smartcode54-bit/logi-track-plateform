"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { Loader2 } from "lucide-react";
import { DEFAULT_LANGUAGE, isLanguage, loadDictionary, type Dictionary, type Language } from "./locales/load";

type TParams = Record<string, string | number>;

type LanguageContextType = {
    language: Language;
    setLanguage: (lang: Language) => void;
    t: (key: string, fallbackOrParams?: string | TParams) => string;
};

const LanguageContext = createContext<LanguageContextType | undefined>(undefined);

const STORAGE_KEY = "language";

function storedLanguage(): Language {
    try {
        const saved = window.localStorage.getItem(STORAGE_KEY);
        return isLanguage(saved) ? saved : DEFAULT_LANGUAGE;
    } catch {
        return DEFAULT_LANGUAGE;
    }
}

// Start fetching the stored language's dictionary as soon as this module runs in the browser, in
// parallel with hydration instead of after the provider's first effect. Failures surface there.
if (typeof window !== "undefined") {
    loadDictionary(storedLanguage()).catch(() => undefined);
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

/**
 * Translation context. Only the active language's dictionary is ever loaded (`./locales/load.ts`,
 * developer-spec.md §10.11): the stored preference, else English, then the other language once the
 * user toggles. Children render once that dictionary is in, so the server and the first client
 * render show the loading screen and hydration never depends on the stored language.
 */
export function LanguageProvider({ children }: { children: React.ReactNode }) {
    const [state, setState] = useState<{ language: Language; dictionary: Dictionary } | null>(null);
    // null: the stored preference. `fallback` marks the default language tried after it failed.
    const [choice, setChoice] = useState<{ language: Language; fallback: boolean } | null>(null);
    const [failed, setFailed] = useState(false);

    // One load per choice. The cleanup drops a load that a newer choice overtook, so the language
    // asked for last always wins, and the current language stays on screen until the new one is in.
    useEffect(() => {
        const language = choice?.language ?? storedLanguage();
        let current = true;
        loadDictionary(language).then(
            (dictionary) => {
                // Re-choosing the current language keeps the same value, so `t` stays the same too.
                if (current) setState((prev) => (prev?.dictionary === dictionary ? prev : { language, dictionary }));
            },
            (error) => {
                if (!current) return;
                console.error(`[language] loading "${language}" failed`, error);
                if (choice === null && language !== DEFAULT_LANGUAGE) {
                    setChoice({ language: DEFAULT_LANGUAGE, fallback: true });
                } else if (choice === null || choice.fallback) {
                    setFailed(true);
                }
                // A failed toggle keeps the language on screen; the user can toggle again.
            }
        );
        return () => {
            current = false;
        };
    }, [choice]);

    const setLanguage = useCallback((lang: Language) => {
        try {
            window.localStorage.setItem(STORAGE_KEY, lang);
        } catch {
            // Storage can be unavailable (private mode); the choice then lasts for this page only.
        }
        setChoice({ language: lang, fallback: false });
    }, []);

    const value = useMemo<LanguageContextType | null>(() => {
        if (!state) return null;
        const { language, dictionary } = state;
        return {
            language,
            setLanguage,
            t: (key, fallbackOrParams) => translate(dictionary, key, fallbackOrParams),
        };
    }, [state, setLanguage]);

    if (!value) return failed ? <LanguageLoadError /> : <LanguageLoading />;
    return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>;
}

export function useLanguage() {
    const context = useContext(LanguageContext);
    if (context === undefined) {
        throw new Error("useLanguage must be used within a LanguageProvider");
    }
    return context;
}
