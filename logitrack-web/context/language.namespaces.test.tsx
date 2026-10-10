// TW4 (developer-spec.md §10.6, §10.11 step 2; Appendix E §E.3.5, §E.7 row 9): `t` is memoised on
// the language and the merged dictionary, the split namespaces load per route group, and a language
// toggle refetches nothing at the eight sites that listed `t` as an effect dependency.
import React, { useEffect, useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "fs";
import path from "path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Dictionary, Language, SplitNamespace } from "./locales/load";

const base: Record<Language, Dictionary> = {
    en: { "greeting": "Hello", "shell.reload": "Reload", "trucks.detail.notFound": "Not found" },
    th: { "greeting": "สวัสดี", "shell.reload": "โหลดใหม่", "trucks.detail.notFound": "ไม่พบ" },
};
const split: Record<Language, Record<SplitNamespace, Dictionary>> = {
    en: { accounting: { "accounting.title": "Accounting" }, driverMonitor: { "driverMonitor.title": "Monitor" } },
    th: { accounting: { "accounting.title": "บัญชี" }, driverMonitor: { "driverMonitor.title": "ติดตาม" } },
};
const loads: string[] = [];
// Namespace chunks (`<language>:<ns>`) whose request fails, as a flaky network would.
const failingChunks = new Set<string>();
const loadDictionary = vi.fn(async (language: Language) => {
    loads.push(language);
    return base[language];
});
const loadNamespace = vi.fn(async (language: Language, ns: SplitNamespace) => {
    loads.push(`${language}:${ns}`);
    if (failingChunks.has(`${language}:${ns}`)) throw new Error(`chunk locale-${language}-${ns} failed`);
    return split[language][ns];
});

vi.mock("./locales/load", async (importOriginal) => {
    const actual = await importOriginal<typeof import("./locales/load")>();
    return {
        ...actual,
        loadDictionary: (language: Language) => loadDictionary(language),
        loadNamespace: (language: Language, ns: SplitNamespace) => loadNamespace(language, ns),
    };
});

// The truck preview hook (one of the eight sites) with its data source counted.
const getTruckByIdClient = vi.hoisted(() => vi.fn(async () => null));
vi.mock("@/features/trucks/services/truckService", () => ({ getTruckByIdClient }));
vi.mock("@/app/app/truck-assignment/actions.client", () => ({ getTruckAssignmentHistory: vi.fn(async () => []) }));
vi.mock("@/features/subcontractors/services/subcontractorService", () => ({ getSubcontractors: vi.fn(async () => []) }));
vi.mock("next/navigation", () => ({ useSearchParams: () => ({ get: () => "truck-1" }) }));
const setCustomLastItem = vi.fn();
vi.mock("@/context/breadcrumb", () => ({ useBreadcrumb: () => ({ setCustomLastItem }) }));

const { LanguageProvider, useLanguage } = await import("./language");
const { RouteNamespaces } = await import("./locales/RouteNamespaces");
const { useTruckPreview } = await import("@/features/trucks/hooks/useTruckPreview");

let stored: string | null;

beforeEach(() => {
    stored = null;
    loads.length = 0;
    failingChunks.clear();
    loadDictionary.mockClear();
    loadNamespace.mockClear();
    getTruckByIdClient.mockClear();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => stored);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((_k, v) => {
        stored = String(v);
    });
});

afterEach(() => {
    vi.restoreAllMocks();
});

function Toggle() {
    const { setLanguage, language } = useLanguage();
    return (
        <button type="button" onClick={() => setLanguage(language === "en" ? "th" : "en")}>
            toggle
        </button>
    );
}

function Text({ k }: { k: string }) {
    const { t } = useLanguage();
    return <p data-testid={k}>{t(k)}</p>;
}

describe("route-group namespaces", () => {
    it("a route outside every group renders with the base dictionary and loads no namespace", async () => {
        render(
            <LanguageProvider>
                <RouteNamespaces pathname="/app/dashboard">
                    <Text k="greeting" />
                </RouteNamespaces>
            </LanguageProvider>
        );
        expect(await screen.findByTestId("greeting")).toHaveTextContent("Hello");
        expect(loadNamespace).not.toHaveBeenCalled();
    });

    it("an accounting route waits for its namespace only, then renders its keys", async () => {
        render(
            <LanguageProvider>
                <RouteNamespaces pathname="/app/accounting/income">
                    <Text k="accounting.title" />
                </RouteNamespaces>
            </LanguageProvider>
        );
        expect(await screen.findByTestId("accounting.title")).toHaveTextContent("Accounting");
        expect(loads.filter((l) => l.includes(":"))).toEqual(["en:accounting"]);
    });

    it("a toggle switches base and the route's namespace together", async () => {
        const user = userEvent.setup();
        render(
            <LanguageProvider>
                <Toggle />
                <RouteNamespaces pathname="/app/driver-monitor">
                    <Text k="driverMonitor.title" />
                    <Text k="greeting" />
                </RouteNamespaces>
            </LanguageProvider>
        );
        expect(await screen.findByTestId("driverMonitor.title")).toHaveTextContent("Monitor");
        await user.click(screen.getByText("toggle"));
        expect(await screen.findByText("ติดตาม")).toBeInTheDocument();
        expect(screen.getByTestId("greeting")).toHaveTextContent("สวัสดี");
        // Only the route's namespace, never the other one (load.ts caches each chunk per page load).
        expect(loads).toEqual(["en", "en:driverMonitor", "th", "th:driverMonitor"]);
        await user.click(screen.getByText("toggle"));
        expect(await screen.findByText("Monitor")).toBeInTheDocument();
        expect(loads.slice(4)).toEqual(["en", "en:driverMonitor"]);
    });

    // A page with unsaved local state (an open dialog's draft) and a count of its mounts.
    function DraftPage({ onMount }: { onMount: () => void }) {
        const { t } = useLanguage();
        const [draft, setDraft] = useState("");
        useEffect(onMount, [onMount]);
        return (
            <div>
                <p>{t("accounting.title")}</p>
                <input aria-label="draft" value={draft} onChange={(e) => setDraft(e.target.value)} />
            </div>
        );
    }

    it("a toggle whose namespace chunk fails keeps the current language and the page mounted", async () => {
        vi.spyOn(console, "error").mockImplementation(() => undefined);
        const user = userEvent.setup();
        const onMount = vi.fn();
        render(
            <LanguageProvider>
                <Toggle />
                <RouteNamespaces pathname="/app/accounting/income">
                    <DraftPage onMount={onMount} />
                </RouteNamespaces>
            </LanguageProvider>
        );
        expect(await screen.findByText("Accounting")).toBeInTheDocument();
        await user.type(screen.getByLabelText("draft"), "unsaved");

        failingChunks.add("th:accounting");
        await user.click(screen.getByText("toggle"));
        await waitFor(() => expect(loads).toContain("th:accounting"));
        await act(async () => undefined);
        // English stays on screen, as for a failed base dictionary; nothing unmounted.
        expect(screen.getByText("Accounting")).toBeInTheDocument();
        expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
        expect(onMount).toHaveBeenCalledTimes(1);

        // Once the chunk loads, the next toggle switches, still without a remount.
        failingChunks.clear();
        await user.click(screen.getByText("toggle"));
        expect(await screen.findByText("บัญชี")).toBeInTheDocument();
        expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
        expect(onMount).toHaveBeenCalledTimes(1);
    });

    it("on the first load a failing namespace still shows the base language and the group's failed state", async () => {
        vi.spyOn(console, "error").mockImplementation(() => undefined);
        failingChunks.add("en:accounting");
        render(
            <LanguageProvider>
                <Toggle />
                <RouteNamespaces pathname="/app/accounting/income">
                    <Text k="accounting.title" />
                </RouteNamespaces>
            </LanguageProvider>
        );
        // The shell (base dictionary) is up; only the group shows its failure, with a reload.
        expect(await screen.findByText("shell.namespaceLoadFailed")).toBeInTheDocument();
        expect(screen.getByText("toggle")).toBeInTheDocument();
        expect(screen.getByText("Reload")).toBeInTheDocument();
        expect(screen.queryByTestId("accounting.title")).toBeNull();
    });
});

describe("t is memoised (context/language.tsx)", () => {
    it("keeps its identity across renders and changes only with the language", async () => {
        const ts: unknown[] = [];
        let rerender: () => void = () => undefined;
        function Probe() {
            const { t } = useLanguage();
            const [, setN] = useState(0);
            useEffect(() => {
                rerender = () => setN((n) => n + 1);
            }, []);
            useEffect(() => {
                ts.push(t);
            });
            return <Toggle />;
        }
        const user = userEvent.setup();
        render(
            <LanguageProvider>
                <Probe />
            </LanguageProvider>
        );
        await screen.findByText("toggle");
        await act(async () => rerender());
        await act(async () => rerender());
        expect(new Set(ts).size).toBe(1);
        await user.click(screen.getByText("toggle"));
        await screen.findByText("toggle");
        await act(async () => rerender());
        expect(new Set(ts).size).toBe(2);
    });
});

describe("a language toggle refetches nothing at the eight sites (Appendix E §E.3.5)", () => {
    it("useTruckPreview loads the truck once across toggles", async () => {
        const user = userEvent.setup();
        function Harness() {
            useTruckPreview();
            return <Toggle />;
        }
        render(
            <LanguageProvider>
                <Harness />
            </LanguageProvider>
        );
        await screen.findByText("toggle");
        await act(async () => undefined);
        expect(getTruckByIdClient).toHaveBeenCalledTimes(1);
        await user.click(screen.getByText("toggle"));
        await user.click(screen.getByText("toggle"));
        await act(async () => undefined);
        expect(getTruckByIdClient).toHaveBeenCalledTimes(1);
    });

    // The effect scanner that found the sites: no effect at these files lists `t` any more.
    const SITES = [
        "app/app/holidays/page.tsx",
        "features/security-center/components/SessionManagementActiveUsers.tsx",
        "features/drivers/components/EditDriverForm.tsx",
        "features/drivers/components/DriverPreview.tsx",
        "features/trucks/components/form/EditTruckForm.tsx",
        "features/trucks/hooks/useTruckPreview.ts",
        "features/customers/components/EditCustomerForm.tsx",
        "features/accounting/components/EditBillingDialog.tsx",
    ];

    function effectDependencyLists(src: string): string[] {
        const lists: string[] = [];
        for (const m of src.matchAll(/use(?:Layout)?Effect\(/g)) {
            let i = (m.index ?? 0) + m[0].length;
            let depth = 1;
            while (i < src.length && depth > 0) {
                if (src[i] === "(") depth++;
                else if (src[i] === ")") depth--;
                i++;
            }
            const deps = /\[([^[\]]*)\]\s*\)$/.exec(src.slice(m.index, i).trimEnd());
            if (deps) lists.push(deps[1]);
        }
        return lists;
    }

    it.each(SITES)("%s", (file) => {
        const src = readFileSync(path.resolve(__dirname, "..", file), "utf8");
        const withT = effectDependencyLists(src).filter((deps) => /(^|[\s,])t([\s,]|$)/.test(deps));
        expect(withT).toEqual([]);
    });
});
