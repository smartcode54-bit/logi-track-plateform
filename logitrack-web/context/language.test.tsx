import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Dictionary, Language } from "./locales/load";

const dictionaries: Record<Language, Dictionary> = {
    en: { "greeting": "Hello {name}", "only.en": "English only" },
    // An empty value falls back like a missing one.
    th: { "greeting": "สวัสดี {name}", "only.en": "" },
};
const deferred = new Map<Language, { resolve: () => void; reject: (e: unknown) => void }>();
const loadDictionary = vi.fn((language: Language): Promise<Dictionary> => {
    return new Promise((resolve, reject) => {
        deferred.set(language, { resolve: () => resolve(dictionaries[language]), reject });
    });
});

vi.mock("./locales/load", async (importOriginal) => {
    const actual = await importOriginal<typeof import("./locales/load")>();
    return { ...actual, loadDictionary: (language: Language) => loadDictionary(language) };
});

const { LanguageProvider, useLanguage } = await import("./language");

function Probe() {
    const { language, setLanguage, t } = useLanguage();
    return (
        <div>
            <p data-testid="text">{t("greeting", { name: "Ann" })}</p>
            <p data-testid="fallback">{t("only.en", "fallback text")}</p>
            <p data-testid="missing">{t("missing.key")}</p>
            <p data-testid="language">{language}</p>
            <button type="button" onClick={() => setLanguage("en")}>to en</button>
            <button type="button" onClick={() => setLanguage("th")}>to th</button>
        </div>
    );
}

async function settle(language: Language) {
    await act(async () => {
        deferred.get(language)?.resolve();
    });
}

describe("LanguageProvider", () => {
    let stored: string | null;

    beforeEach(() => {
        stored = null;
        deferred.clear();
        loadDictionary.mockClear();
        vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => stored);
        vi.spyOn(Storage.prototype, "setItem").mockImplementation((_key, value) => {
            stored = String(value);
        });
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("shows the loading screen until the stored language's dictionary is in, and loads only that one", async () => {
        stored = "th";
        render(<LanguageProvider><Probe /></LanguageProvider>);

        expect(screen.getByText("Loading / กำลังโหลด...")).toBeInTheDocument();
        expect(screen.queryByTestId("text")).toBeNull();

        await settle("th");
        expect(screen.getByTestId("text")).toHaveTextContent("สวัสดี Ann");
        expect(screen.getByTestId("language")).toHaveTextContent("th");
        expect(loadDictionary.mock.calls.map(([l]) => l)).toEqual(["th"]);
    });

    it("keeps the fallback, key and empty-value behaviour of t()", async () => {
        stored = "th";
        render(<LanguageProvider><Probe /></LanguageProvider>);
        await settle("th");
        expect(screen.getByTestId("fallback")).toHaveTextContent("fallback text");
        expect(screen.getByTestId("missing")).toHaveTextContent("missing.key");
    });

    it("uses English when nothing valid is stored", async () => {
        stored = "fr";
        render(<LanguageProvider><Probe /></LanguageProvider>);
        await settle("en");
        expect(screen.getByTestId("text")).toHaveTextContent("Hello Ann");
        expect(loadDictionary.mock.calls.map(([l]) => l)).toEqual(["en"]);
    });

    it("loads the other language on a toggle, keeps the current one on screen meanwhile, and stores the choice", async () => {
        const user = userEvent.setup();
        render(<LanguageProvider><Probe /></LanguageProvider>);
        await settle("en");

        await user.click(screen.getByRole("button", { name: "to th" }));
        expect(stored).toBe("th");
        expect(screen.getByTestId("text")).toHaveTextContent("Hello Ann");

        await settle("th");
        expect(screen.getByTestId("text")).toHaveTextContent("สวัสดี Ann");
    });

    it("applies the last toggle when an earlier load finishes later", async () => {
        const user = userEvent.setup();
        render(<LanguageProvider><Probe /></LanguageProvider>);
        await settle("en");

        await user.click(screen.getByRole("button", { name: "to th" }));
        await user.click(screen.getByRole("button", { name: "to en" }));
        await settle("en");
        await settle("th"); // the earlier request resolves last
        expect(screen.getByTestId("language")).toHaveTextContent("en");
        expect(screen.getByTestId("text")).toHaveTextContent("Hello Ann");
    });

    it("keeps t() when the current language is chosen again", async () => {
        const user = userEvent.setup();
        const seen = new Set<unknown>();
        function TrackT() {
            seen.add(useLanguage().t);
            return null;
        }
        render(<LanguageProvider><Probe /><TrackT /></LanguageProvider>);
        await settle("en");
        await user.click(screen.getByRole("button", { name: "to en" }));
        await settle("en");
        expect(seen.size).toBe(1);
    });

    it("falls back to English, then shows a reload screen, when dictionaries cannot load", async () => {
        stored = "th";
        vi.spyOn(console, "error").mockImplementation(() => undefined);
        render(<LanguageProvider><Probe /></LanguageProvider>);

        await act(async () => deferred.get("th")?.reject(new Error("ChunkLoadError")));
        expect(loadDictionary.mock.calls.map(([l]) => l)).toEqual(["th", "en"]);

        await act(async () => deferred.get("en")?.reject(new Error("ChunkLoadError")));
        expect(screen.getByRole("alert")).toHaveTextContent("Could not load the interface language.");
        expect(screen.getByRole("button", { name: "Reload / โหลดใหม่" })).toBeInTheDocument();
    });
});
