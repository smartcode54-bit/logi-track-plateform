/**
 * Holidays over Go (T46, issue #66; T45 routes, Appendix B §B.2.17; Appendix E §E.2.2, §E.5 row 6, §E.6).
 * Replaces the listener on the whole `holidays` collection (`holidays/page.tsx:120-144`, which re-subscribed
 * on every language toggle before TW4) and the `saveHoliday`, `deleteHoliday` and `saveGeneratedHolidays`
 * callables:
 *
 * - `['holidays', year]`: `GET /v1/holidays?year` (the public calendar and the caller's tenant's own
 *   holidays of that Bangkok year), a plain fetch (10 min / 60 min; no listener, no poll). The key holds no
 *   language: a toggle re-renders the names from `nameEn` / `nameTh` and reads nothing. `holidays.changed`
 *   (`global` for public rows, `tenant:{tid}:config` for a tenant's) makes `['holidays']` stale;
 * - `POST /v1/holidays` (upsert on day and type), `PATCH /v1/holidays/{id}` (partial), `DELETE
 *   /v1/holidays/{id}` and `PUT /v1/holidays/generate {year, finalHolidays, initialHolidays}`, each making
 *   `['holidays']` stale. Public rows (`tenantId` null) are written by stewards only (R60).
 */
import { queryOptions, useMutation } from "@tanstack/react-query";

import { goFetch, goPath, isApiError } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const HOLIDAYS_PATH = "/v1/holidays";

export const HOLIDAY_TYPES = ["public", "company", "other"] as const;
export type HolidayType = (typeof HOLIDAY_TYPES)[number];
export const HOLIDAY_STATUSES = ["draft", "published", "archived"] as const;
export type HolidayStatus = (typeof HOLIDAY_STATUSES)[number];

/** One holiday (Go `hr.Holiday`): `date` is the Bangkok day, `tenantId` null for the public calendar. */
export interface HolidayDTO {
    id: string;
    tenantId: string | null;
    date: string;
    type: HolidayType;
    status: HolidayStatus;
    name: string;
    nameEn: string | null;
    nameTh: string | null;
    description: string | null;
    descriptionEn: string | null;
    descriptionTh: string | null;
    isRecurring: boolean;
    createdBy: string | null;
    createdAt: string;
    updatedAt: string;
}

/** `['holidays', year]`: one Bangkok year of holidays (a plain fetch, 10 min). */
export function holidaysQuery(year: number) {
    return queryOptions({
        queryKey: queryKeys.holidays.year(year),
        queryFn: ({ signal }) => goFetch<HolidayDTO[]>(HOLIDAYS_PATH, { signal, query: { year } }),
        ...QUERY_POLICY.masterData,
    });
}

/** The display name in a language: the name of that language, else the other one, else `name`. */
export function holidayName(h: Pick<HolidayDTO, "name" | "nameEn" | "nameTh">, language: string): string {
    const th = h.nameTh?.trim();
    const en = h.nameEn?.trim();
    return (language === "th" ? th || en : en || th) || h.name;
}

/** The description in a language: that language's, else the other one, else the combined text. */
export function holidayDescription(h: Pick<HolidayDTO, "description" | "descriptionEn" | "descriptionTh">, language: string): string {
    const th = h.descriptionTh?.trim();
    const en = h.descriptionEn?.trim();
    return (language === "th" ? th || en : en || th) || h.description?.trim() || "";
}

/** The fields of a holiday a write names (absent = unchanged on PATCH; `null` clears an optional text). */
export interface HolidayFields {
    date: string;
    type: HolidayType;
    status: HolidayStatus;
    name?: string;
    nameEn: string | null;
    nameTh: string | null;
    descriptionEn: string | null;
    descriptionTh: string | null;
    description: string | null;
    isRecurring: boolean;
}

/**
 * Go's limits on a holiday's texts, in characters (runes, after trimming): `name`, `nameEn` and `nameTh` at
 * most 300, `description`, `descriptionEn` and `descriptionTh` at most 2000 (`internal/hr/holidays.go`
 * `maxHolidayNameChars`, `maxHolidayDescriptionChars`). The combined `EN (TH)` form counts against the same
 * limit as each part, so two parts that fit alone can still be refused together.
 */
export const HOLIDAY_NAME_MAX_CHARS = 300;
export const HOLIDAY_DESCRIPTION_MAX_CHARS = 2000;

/** The characters Go counts (code points, as `utf8.RuneCountInString`), not UTF-16 units. */
export function holidayTextChars(s: string): number {
    return [...s].length;
}

/** The legacy display name of the two names (`EN (TH)`), which the Firestore projection keeps for the 3.x app. */
export function combinedName(en: string | null | undefined, th: string | null | undefined): string {
    const e = en?.trim() ?? "";
    const t = th?.trim() ?? "";
    return e && t ? `${e} (${t})` : e || t;
}

export const HOLIDAY_WRITE_INVALIDATES = [queryKeys.holidays.all()] as const;

/** `POST /v1/holidays`: created (201) or the holiday of the same day and type updated (200). */
export async function upsertHoliday(input: HolidayFields): Promise<HolidayDTO> {
    return goFetch<HolidayDTO>(HOLIDAYS_PATH, { method: "POST", body: input });
}

/** `PATCH /v1/holidays/{id}` with the fields that changed. */
export async function patchHoliday({ id, fields }: { id: string; fields: Partial<HolidayFields> }): Promise<HolidayDTO> {
    return goFetch<HolidayDTO>(goPath`/v1/holidays/${id}`, { method: "PATCH", body: fields });
}

/** `DELETE /v1/holidays/{id}` (204); a holiday someone else deleted first (404) counts as deleted. */
export async function removeHoliday({ id }: { id: string }): Promise<void> {
    try {
        await goFetch<void>(goPath`/v1/holidays/${id}`, { method: "DELETE" });
    } catch (error) {
        if (isApiError(error) && error.status === 404) return;
        throw error;
    }
}

export interface GenerateHolidaysInput {
    year: number;
    finalHolidays: GeneratedHoliday[];
    initialHolidays: GeneratedHoliday[];
}

/** One generated holiday as `PUT /v1/holidays/generate` takes it. */
export interface GeneratedHoliday {
    date: string;
    type: HolidayType;
    name: string;
    nameEn?: string;
    nameTh?: string;
    isRecurring: boolean;
}

export interface GenerateHolidaysResult {
    saved: number;
    deleted: number;
    duplicates: number;
}

export async function generateHolidays(input: GenerateHolidaysInput): Promise<GenerateHolidaysResult> {
    return goFetch<GenerateHolidaysResult>(`${HOLIDAYS_PATH}/generate`, { method: "PUT", body: input });
}

export function useSaveHoliday() {
    return useMutation({ mutationFn: upsertHoliday, meta: { invalidates: HOLIDAY_WRITE_INVALIDATES } });
}

export function usePatchHoliday() {
    return useMutation({ mutationFn: patchHoliday, meta: { invalidates: HOLIDAY_WRITE_INVALIDATES } });
}

export function useDeleteHoliday() {
    return useMutation({ mutationFn: removeHoliday, meta: { invalidates: HOLIDAY_WRITE_INVALIDATES } });
}

export function useGenerateHolidays() {
    return useMutation({ mutationFn: generateHolidays, meta: { invalidates: HOLIDAY_WRITE_INVALIDATES } });
}

/** The Bangkok year of a `yyyy-MM-dd` day. */
export function yearOfDay(day: string): number {
    return Number(day.slice(0, 4));
}
