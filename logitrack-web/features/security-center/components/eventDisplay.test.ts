// @vitest-environment node
import { readFileSync } from "node:fs";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import en from "@/context/locales/en/securityCenter";
import th from "@/context/locales/th/securityCenter";
import { eventTargetLabel, eventsCsv, type SecurityEventDTO } from "../api/events";
import { buildTypeLabel, eventTypeLabel, formatDateTime, formatRelative, permissionHint } from "./eventDisplay";

const dictionaries = { en, th };
const translate = (language: "en" | "th") => (key: string, fallback?: string | Record<string, string | number>) => {
    const dictionary = dictionaries[language] as Record<string, string>;
    const value = dictionary[key] ?? (typeof fallback === "string" ? fallback : key);
    return typeof fallback === "object" ? Object.entries(fallback).reduce((s, [k, v]) => s.replaceAll(`{${k}}`, String(v)), value) : value;
};
const catalogue = readFileSync(path.resolve(__dirname, "../../../../logitrack-api/internal/security/feed.go"), "utf8").match(/var EventTypes = \[\]string\{([\s\S]*?)\n\}/)![1];
const types = [...catalogue.matchAll(/"([a-z_]+)"/g)].map((match) => match[1]);

afterEach(() => vi.useRealTimers());

describe.each(["en", "th"] as const)("event display in %s", (language) => {
    const t = translate(language);
    it.each(types)("localizes the known event %s", (type) => {
        const key = `securityCenter.securityEvents.types.${type}`;
        expect(eventTypeLabel(type, t)).toBe((dictionaries[language] as Record<string, string>)[key]);
        expect(eventTypeLabel(type, t)).not.toBe(type);
        expect(eventTypeLabel(type, t)).not.toBe(t("securityCenter.ui4.unknownEvent"));
    });
    it("uses a localized fallback for future snake_case event types", () => {
        expect(eventTypeLabel("future_event_type", t)).toBe(t("securityCenter.ui4.unknownEvent"));
    });
    it.each([
        { details: {}, targetUserId: null, label: "—", csvLabel: "" },
        { details: null, targetUserId: null, label: "—", csvLabel: "" },
        { details: {}, targetUserId: "target-id", label: null, csvLabel: null },
        { details: { targetUid: "legacy-id" }, targetUserId: null, label: null, csvLabel: null },
        { details: { name: "Named target" }, targetUserId: null, label: "Named target", csvLabel: "Named target" },
    ])("distinguishes an absent target from an unidentified one: %j", ({ details, targetUserId, label, csvLabel }) => {
        const event: SecurityEventDTO = {
            id: "event-1", createdAt: "2026-10-14T09:00:00Z", type: "role_matrix_saved", severity: "info", summary: "Evidence",
            actorUserId: null, actorEmail: null, actorLegacyUid: null, tenantId: "tenant-1", requestId: null, details, targetUserId,
        };
        expect(eventTargetLabel(event, t)).toBe(label ?? t("securityCenter.ui4.unknownTarget"));
        expect(eventsCsv([event], t).split("\n")[1]).toMatch(new RegExp(`,"${csvLabel ?? t("securityCenter.ui4.unknownTarget")}"$`));
    });
    it("uses an explicit locale for absolute and relative timestamps", () => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date("2026-10-14T10:00:00Z"));
        expect(formatDateTime("2026-10-14T09:00:00Z", language)).toBe(new Date("2026-10-14T09:00:00Z").toLocaleString(language === "th" ? "th-TH" : "en-GB"));
        const relative = formatRelative("2026-10-14T09:00:00Z", language);
        expect(relative).toContain(language === "th" ? "ชั่วโมง" : "hour");
        if (language === "th") expect(relative).not.toMatch(/ago|Oct/);
        for (const value of [null, undefined, "", "invalid"]) {
            expect(formatDateTime(value, language)).toBe("—");
            expect(formatRelative(value, language)).toBe("—");
        }
    });
    it("names production, test and unknown build types", () => {
        expect(buildTypeLabel("prod", t)).toBe(t("securityCenter.ui4.production"));
        expect(buildTypeLabel("dev", t)).toBe(t("securityCenter.ui4.test"));
        expect(buildTypeLabel("future", t)).toBe(t("securityCenter.ui4.unknownBuildType"));
    });
    it("names the roles page and the exact catalogue permission", () => {
        expect(permissionHint("users:revoke_sessions", language, t)).toContain(language === "th" ? "บทบาทและสิทธิ์" : "Roles and permissions");
        expect(permissionHint("security:view_mobile_clients", language, t)).toContain(language === "th" ? "ดูอุปกรณ์แอปคนขับ" : "View mobile clients");
    });
});
