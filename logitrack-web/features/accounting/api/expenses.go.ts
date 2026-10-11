/**
 * Vehicle expenses over Go (T42, P3; routes of T40, Appendix B §B.2.15 as built; developer-spec.md §10.7
 * row `['expenses', …]`, §10.10; Appendix E §E.2.3 rows "fuel", "other", "audit").
 *
 * `['expenses',{type,status,category,driverId,truckId,from,to}]` is one keyset list
 * (`GET /v1/expenses`, newest first, 100 per page): every filter is in the key and in the SQL, so the
 * fuel and other pages never read another type (the Firestore page scanned every expense and filtered
 * in the browser, `expenses.ts:90-101`) and the audit page's status tabs are server filters (it
 * re-downloaded everything on each switch, `audit/page.tsx:106-108`). The first page carries
 * `meta.statusCounts` over the other filters. Photos are object keys read through `GET /v1/files?key=`.
 * Writes invalidate `['expenses']` and `['badges']`; SSE `expense.*` (tenant:{tid}:expenses) does the same
 * in other tabs.
 */
import { infiniteQueryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath, goUrl } from "@/lib/goFetch";
import { goPostForm } from "@/lib/goForm";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { assertUploadable, PartialCreateError, uploadToGo } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const EXPENSES_PATH = "/v1/expenses";
export const EXPENSES_PAGE_SIZE = 100;

export type ExpenseType = "fuel" | "other";
export type ExpenseStatus = "pending" | "approved" | "rejected";
export const EXPENSE_STATUSES: readonly ExpenseStatus[] = ["pending", "approved", "rejected"];
export const EXPENSE_CATEGORIES = ["tire_repair", "maintenance", "toll", "parking", "other"] as const;
export type ExpenseCategory = (typeof EXPENSE_CATEGORIES)[number];

/** `GET /v1/expenses` item (internal/expenses `Expense`). */
export interface ExpenseDTO {
    id: string;
    tenantId: string;
    type: ExpenseType | string;
    category: string | null;
    status: ExpenseStatus | string;
    /** The instant (RFC 3339). */
    date: string;
    /** The Bangkok calendar day (`yyyy-MM-dd`). */
    expenseDate: string;
    amountThb: number;
    driverId: string | null;
    driverName: string | null;
    truckId: string | null;
    /** The row's frozen plate snapshot. */
    licensePlate: string | null;
    volumeLiters: number | null;
    pricePerLiter: number | null;
    odometerKm: number | null;
    stationTaxId: string | null;
    taxInvId: string | null;
    refillLat: number | null;
    refillLng: number | null;
    note: string | null;
    description: string | null;
    adminNote: string | null;
    distanceKm: number | null;
    tollImportSequence: number | null;
    tollLocation: string | null;
    tollLane: string | null;
    tollSourceType: string | null;
    receiptKey: string | null;
    odometerKey: string | null;
    createdBy: string | null;
    createdAt: string;
    updatedAt: string;
}

export type ExpenseFilter = {
    type?: ExpenseType;
    status?: ExpenseStatus;
    category?: string;
    driverId?: string;
    truckId?: string;
    /** Inclusive Bangkok days, `yyyy-MM-dd`. */
    from?: string;
    to?: string;
};

export type StatusCounts = Partial<Record<ExpenseStatus, number>>;

/** Drops empty values so equal filters build equal keys. */
export function normalizeExpenseFilter(f: ExpenseFilter): ExpenseFilter {
    const out: ExpenseFilter = {};
    for (const [k, v] of Object.entries(f) as [keyof ExpenseFilter, string | undefined][]) {
        if (v !== undefined && v !== "" && v !== "all") (out as Record<string, string>)[k] = v;
    }
    return out;
}

type ExpensesKey = ReturnType<typeof queryKeys.expenses<ExpenseFilter>>;

export function expensesQuery(filter: ExpenseFilter, enabled = true) {
    const f = normalizeExpenseFilter(filter);
    return infiniteQueryOptions({
        queryKey: queryKeys.expenses(f),
        queryFn: goInfiniteQueryFn<ExpenseDTO, ExpensesKey>(EXPENSES_PATH, {
            query: ([, p]) => ({ ...p, limit: EXPENSES_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.list,
        enabled,
    });
}

export function expenseRows(pages: GoPage<ExpenseDTO>[] | undefined): ExpenseDTO[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

/** `meta.statusCounts` of the first page (counts over every row of the other filters). */
export function statusCountsOf(pages: GoPage<ExpenseDTO>[] | undefined): StatusCounts {
    const meta = pages?.[0]?.meta as { statusCounts?: StatusCounts } | undefined;
    return meta?.statusCounts ?? {};
}

/** The browser URL of an object key (`GET /v1/files?key=` answers 302 to a signed GET). */
export function fileHref(key: string | null | undefined): string | null {
    return key ? goUrl("/v1/files", { key }) : null;
}

export async function invalidateExpenses(client: QueryClient = getQueryClient()) {
    await Promise.all([
        client.invalidateQueries({ queryKey: ["expenses"] }),
        client.invalidateQueries({ queryKey: queryKeys.badges() }),
    ]);
}

// --- writes --------------------------------------------------------------------------------------

/** `POST /v1/expenses` body (admin entry: approved unless `status` says otherwise). */
export interface ExpenseInput {
    type: ExpenseType;
    driverId?: string;
    truckId?: string;
    /** RFC 3339 instant, a Bangkok wall time or a day. */
    date: string;
    amountThb: number;
    category?: string;
    volumeLiters?: number;
    pricePerLiter?: number;
    odometerKm?: number;
    stationTaxId?: string;
    taxInvId?: string;
    note?: string;
    description?: string;
    adminNote?: string;
    distanceKm?: number;
}

/** `PATCH /v1/expenses/{id}`: a partial update (`null` clears a field). */
export type ExpensePatch = Partial<Record<keyof Omit<ExpenseInput, "type">, string | number | null>> & {
    receiptKey?: string | null;
    odometerKey?: string | null;
};

function compact<T extends object>(v: T): T {
    return Object.fromEntries(Object.entries(v).filter(([, x]) => x !== undefined && x !== "")) as T;
}

export type ExpenseFiles = { receipt?: File | null; odometer?: File | null };

const expenseUploads = (files: ExpenseFiles) => [
    { file: files.receipt, purpose: "expense_receipt" as const },
    { file: files.odometer, purpose: "expense_odometer" as const },
];

/**
 * Uploads an existing expense's photos (`expense_receipt`, `expense_odometer`) and links them with one
 * `PATCH`; with no file it is a no-op that resolves the row unchanged.
 */
export async function attachExpenseFiles(expense: ExpenseDTO, files: ExpenseFiles, client: QueryClient = getQueryClient()): Promise<ExpenseDTO> {
    assertUploadable(expenseUploads(files));
    const patch: ExpensePatch = {};
    if (files.receipt) patch.receiptKey = await uploadToGo(files.receipt, { purpose: "expense_receipt", entityId: expense.id });
    if (files.odometer) patch.odometerKey = await uploadToGo(files.odometer, { purpose: "expense_odometer", entityId: expense.id });
    if (Object.keys(patch).length === 0) return expense;
    const out = await goFetch<ExpenseDTO>(goPath`/v1/expenses/${expense.id}`, { method: "PATCH", body: patch });
    await invalidateExpenses(client);
    return out;
}

/**
 * Creates an expense, then uploads its photos under the new id and links them with a `PATCH` (every upload
 * names its entity, developer-spec.md §9.4). A photo Go would refuse (type, size) throws
 * `UploadRefusedError` before the `POST`, so nothing is written. A failure after the row exists throws
 * `PartialCreateError` carrying the row (the list is refreshed first): the caller retries with
 * `attachExpenseFiles`, never a second `POST` (an admin entry is approved at once, so a duplicate would be
 * a second approved expense).
 */
export async function createExpense(input: ExpenseInput, files: ExpenseFiles = {}, client: QueryClient = getQueryClient()): Promise<ExpenseDTO> {
    assertUploadable(expenseUploads(files));
    const created = await goFetch<ExpenseDTO>(EXPENSES_PATH, { method: "POST", body: compact(input) });
    try {
        const out = await attachExpenseFiles(created, files, client);
        if (out === created) await invalidateExpenses(client);
        return out;
    } catch (err) {
        await invalidateExpenses(client);
        throw new PartialCreateError(created, err);
    }
}

export async function patchExpense(id: string, patch: ExpensePatch, client: QueryClient = getQueryClient()): Promise<ExpenseDTO> {
    const out = await goFetch<ExpenseDTO>(goPath`/v1/expenses/${id}`, { method: "PATCH", body: patch });
    await invalidateExpenses(client);
    return out;
}

/** `POST /v1/expenses/{id}/status`: approving a fuel row with an odometer runs the PM check in the same tx. */
export interface StatusResult extends ExpenseDTO {
    pmCheck: { created: boolean; maintenanceId: string | null; outcome: string } | null;
}

export async function setExpenseStatus(id: string, status: ExpenseStatus, adminNote?: string, client: QueryClient = getQueryClient()) {
    const out = await goFetch<StatusResult>(goPath`/v1/expenses/${id}/status`, {
        method: "POST",
        body: adminNote === undefined ? { status } : { status, adminNote },
    });
    await Promise.all([invalidateExpenses(client), out.pmCheck?.created ? client.invalidateQueries({ queryKey: queryKeys.maintenance() }) : null]);
    return out;
}

/** Go's cap on one `POST /v1/expenses/bulk-status` (internal/expenses `MaxBulk`; more is `422 ids too_many`). */
export const EXPENSES_BULK_MAX = 500;

/** A bulk status change that stopped part way: `done` rows of `total` were changed before `failure`. */
export class BulkStatusError extends Error {
    constructor(
        readonly done: StatusResult[],
        readonly total: number,
        readonly failure: unknown
    ) {
        super(`bulk status stopped after ${done.length} of ${total}`);
        this.name = "BulkStatusError";
    }
}

/**
 * Sets the status of any number of expenses in sequential requests of at most `EXPENSES_BULK_MAX` ids
 * (each one transaction on the server), results in order. A failed request stops the run with
 * `BulkStatusError` (the rows of the requests before it are changed, the rest are not); the expense and
 * maintenance keys are refreshed either way.
 */
export async function bulkExpenseStatus(ids: string[], status: ExpenseStatus, adminNote?: string, client: QueryClient = getQueryClient()) {
    const out: StatusResult[] = [];
    try {
        for (let i = 0; i < ids.length; i += EXPENSES_BULK_MAX) {
            const chunk = ids.slice(i, i + EXPENSES_BULK_MAX);
            try {
                out.push(
                    ...(await goFetch<StatusResult[]>(`${EXPENSES_PATH}/bulk-status`, {
                        method: "POST",
                        body: adminNote === undefined ? { ids: chunk, status } : { ids: chunk, status, adminNote },
                    }))
                );
            } catch (err) {
                throw new BulkStatusError(out, ids.length, err);
            }
        }
    } finally {
        await Promise.all([invalidateExpenses(client), client.invalidateQueries({ queryKey: queryKeys.maintenance() })]);
    }
    return out;
}

export interface TollImportResult {
    created: number;
    skipped: number;
    skippedRows: { row: number; reason: string }[];
    dryRun: boolean;
}

/**
 * `POST /v1/expenses/toll-import` as a form upload: Go parses the sheet (the `ผ่านทาง` rows, its own
 * column matching) and writes one `other`/`toll` row per line; `dryRun` reports without writing.
 */
export async function importTolls(
    input: { file: File; driverId: string; truckId: string; dryRun: boolean },
    client: QueryClient = getQueryClient()
): Promise<TollImportResult> {
    const form = new FormData();
    form.set("file", input.file);
    if (input.driverId) form.set("driverId", input.driverId);
    if (input.truckId) form.set("truckId", input.truckId);
    form.set("dryRun", String(input.dryRun));
    const out = await goPostForm<TollImportResult>(`${EXPENSES_PATH}/toll-import`, form);
    if (!input.dryRun) await invalidateExpenses(client);
    return out;
}

// --- pure helpers --------------------------------------------------------------------------------

/** Total amount and litres of loaded rows. */
export function expenseTotals(rows: ExpenseDTO[]) {
    let amount = 0;
    let liters = 0;
    for (const r of rows) {
        amount += r.amountThb || 0;
        liters += r.volumeLiters || 0;
    }
    return { amount: Math.round(amount * 100) / 100, liters: Math.round(liters * 100) / 100, count: rows.length };
}

export const KMPL_MIN = 2;
export const KMPL_MAX = 25;

export type FuelFlag = "no-odometer" | "chain-start" | "odometer-rollback" | "out-of-range";

export interface FuelEfficiency {
    distanceKm: number | null;
    kmPerLiter: number | null;
    flag: FuelFlag | null;
}

/**
 * km/L of each loaded fuel row, chained per truck (not per driver, so the odometer continues across
 * drivers sharing a truck) in odometer order: the distance since the truck's previous refill with an
 * odometer, divided by this refill's litres (the Firestore page's rule, `fuel/page.tsx`
 * `computeKmPerLiter`). A row without a truck or odometer gets no figure. Rows outside the loaded pages
 * are not seen, so the first loaded refill of a truck starts its chain.
 */
export function fuelEfficiency(rows: ExpenseDTO[]): Map<string, FuelEfficiency> {
    const out = new Map<string, FuelEfficiency>();
    const byTruck = new Map<string, ExpenseDTO[]>();
    for (const r of rows) {
        const key = r.truckId ?? (r.licensePlate ? `plate:${r.licensePlate}` : "");
        if (!key) {
            out.set(r.id, { distanceKm: null, kmPerLiter: null, flag: r.odometerKm == null ? "no-odometer" : null });
            continue;
        }
        const list = byTruck.get(key) ?? [];
        list.push(r);
        byTruck.set(key, list);
    }
    for (const list of byTruck.values()) {
        const sorted = [...list].sort((a, b) => {
            const ao = a.odometerKm ?? Number.POSITIVE_INFINITY;
            const bo = b.odometerKm ?? Number.POSITIVE_INFINITY;
            return ao !== bo ? ao - bo : a.date.localeCompare(b.date);
        });
        let prevOdo: number | null = null;
        for (const r of sorted) {
            const odo = r.odometerKm;
            let eff: FuelEfficiency = { distanceKm: null, kmPerLiter: null, flag: null };
            if (odo == null) eff.flag = "no-odometer";
            else if (prevOdo == null) eff.flag = "chain-start";
            else if (odo < prevOdo) eff.flag = "odometer-rollback";
            else {
                const distance = odo - prevOdo;
                eff = { distanceKm: distance, kmPerLiter: null, flag: null };
                if (r.volumeLiters != null && r.volumeLiters > 0) {
                    const raw = distance / r.volumeLiters;
                    eff.kmPerLiter = Math.round(raw * 10) / 10;
                    if (raw < KMPL_MIN || raw > KMPL_MAX) eff.flag = "out-of-range";
                }
            }
            if (odo != null) prevOdo = odo;
            out.set(r.id, eff);
        }
    }
    return out;
}
