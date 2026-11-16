"use client";

/**
 * Loads one P&L month from the source collections and maps it to `PnlInputs` (spec §4.1).
 * Timestamps become Dates here so `lib/pnlCompute.ts` stays pure. Read-only — this file writes
 * nothing (billing snapshots are server-only, Confirmed Pattern 2026-10-01).
 */
import {
    collection,
    documentId,
    getDocs,
    getDocsFromServer,
    query,
    Timestamp,
    where,
    type DocumentData,
    type QueryDocumentSnapshot,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { bangkokDateKey } from "@/lib/compensationCompute";
import { extractHubId, normalizeDestinationCode } from "@/lib/billingCompute";
import { driverDisplayName, driverIdentityKeys } from "@/lib/driverName";
import { primaryHubLabelFromFirestoreData } from "@/lib/hubDisplay";
import { bangkokMonthBounds, DAY_MS } from "@/lib/pnl/settings";
import type {
    PnlDistanceRow,
    PnlFuelRowInput,
    PnlHelperTaskInput,
    PnlInputs,
    PnlJobCategory,
    PnlMaintenanceInput,
    PnlOtherExpenseInput,
    PnlRenewalInput,
    PnlRoute,
    PnlStandbyInput,
    PnlTripInput,
    PnlTruckInput,
} from "@/lib/pnlCompute";
import { maintenanceDisplayCost } from "@/features/maintenance/utils/maintenanceDisplayCost";
import { listPnlFixedCosts, listPnlRates } from "./pnlSettings";

/** F17 — a source the current user cannot read. The page names the collection instead of showing partial figures. */
export class PnlSourceError extends Error {
    /** Firestore error code, e.g. "permission-denied". */
    public readonly code?: string;

    constructor(public readonly source: string, cause: unknown) {
        super(`P&L source "${source}" could not be read: ${cause instanceof Error ? cause.message : String(cause)}`);
        this.name = "PnlSourceError";
        const code = (cause as { code?: unknown } | null)?.code;
        this.code = typeof code === "string" ? code : undefined;
    }

    /**
     * The P&L settings collections are new: until `firestore.rules` with their `match` blocks is
     * deployed, the deny-all fallback blocks them even for admins.
     */
    get settingsRulesMissing(): boolean {
        return this.code === "permission-denied" && this.source.startsWith("pnl_");
    }
}

async function load<T>(source: string, fn: () => Promise<T>): Promise<T> {
    try {
        return await fn();
    } catch (e) {
        throw new PnlSourceError(source, e);
    }
}

type Doc = Record<string, unknown>;
type Snap = QueryDocumentSnapshot<DocumentData>;

function toDate(v: unknown): Date | undefined {
    if (!v) return undefined;
    if (v instanceof Date) return v;
    const ts = v as { toDate?: () => Date; seconds?: number };
    if (typeof ts.toDate === "function") return ts.toDate();
    if (typeof ts.seconds === "number") return new Date(ts.seconds * 1000);
    if (typeof v === "string") {
        const d = new Date(v);
        return Number.isNaN(d.getTime()) ? undefined : d;
    }
    return undefined;
}
const str = (v: unknown) => (typeof v === "string" && v.trim() !== "" ? v.trim() : undefined);
const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : typeof v === "string" && v.trim() !== "" && Number.isFinite(Number(v)) ? Number(v) : undefined);
const jobCategory = (v: unknown): PnlJobCategory | undefined => (v === "PRIMARY" || v === "SUPPLEMENTARY" ? v : undefined);

const TASK_LOOKBACK_DAYS = 10;
const TRIP_BUFFER_DAYS = 3;
const FUEL_LOOKBACK_DAYS = 60;

/** Route of a task: origin + one destination, or every stop of a multi-drop job. */
function taskRoute(task: Doc | undefined): PnlRoute | null {
    if (!task) return null;
    const originCode = extractHubId(str(task.sourceHub)) || undefined;
    const stops = Array.isArray(task.deliveryStops) ? (task.deliveryStops as Doc[]) : [];
    const destinationCodes =
        task.isMultiDelivery === true && stops.length >= 2
            ? stops.map((s) => normalizeDestinationCode(str(s.destination))).filter(Boolean)
            : [normalizeDestinationCode(str(task.destination))].filter(Boolean);
    return { originCode, destinationCodes };
}

export interface PnlMonthSources {
    inputs: Omit<PnlInputs, "now">;
    /** Upper-cased hub/SOC code → display label (Thai first). */
    hubLabels: Record<string, string>;
    /** All trucks (own + subcontractor) for filters and the settings truck picker. */
    trucks: PnlTruckInput[];
}

/** One P&L month. */
export async function fetchPnlMonthSources(year: number, month: number): Promise<PnlMonthSources> {
    const { start, end } = bangkokMonthBounds(year, month);
    return fetchPnlRangeSources(start, end, year, month);
}

/**
 * January → `lastMonth` of a year in ONE load (year view): ~12× the trip/task/expense reads of a
 * month, but the master collections (trucks, distances, maintenance, …) are read once.
 * `inputs.month` is `lastMonth`; `computePnlYear` sets it per month.
 */
export async function fetchPnlYearSources(year: number, lastMonth: number): Promise<PnlMonthSources> {
    return fetchPnlRangeSources(bangkokMonthBounds(year, 1).start, bangkokMonthBounds(year, lastMonth).end, year, lastMonth);
}

async function fetchPnlRangeSources(start: Date, end: Date, year: number, month: number): Promise<PnlMonthSources> {
    const ts = (ms: number) => Timestamp.fromMillis(ms);

    const [tripSnap, taskSnap, standbySnap, expenseSnap] = await Promise.all([
        load(COLLECTIONS.TRIP_RECORDS, () =>
            getDocsFromServer(query(collection(db, COLLECTIONS.TRIP_RECORDS),
                where("createdAt", ">=", ts(start.getTime() - TRIP_BUFFER_DAYS * DAY_MS)),
                where("createdAt", "<", ts(end.getTime() + TRIP_BUFFER_DAYS * DAY_MS))))),
        load(COLLECTIONS.TASKS, () =>
            getDocs(query(collection(db, COLLECTIONS.TASKS),
                where("date", ">=", ts(start.getTime() - TASK_LOOKBACK_DAYS * DAY_MS)),
                where("date", "<", ts(end.getTime() + TRIP_BUFFER_DAYS * DAY_MS))))),
        load(COLLECTIONS.STANDBY_RECORDS, () =>
            getDocsFromServer(query(collection(db, COLLECTIONS.STANDBY_RECORDS),
                where("endedAt", ">=", ts(start.getTime() - DAY_MS)),
                where("endedAt", "<", ts(end.getTime() + DAY_MS))))),
        load(COLLECTIONS.VEHICLE_EXPENSES, () =>
            getDocs(query(collection(db, COLLECTIONS.VEHICLE_EXPENSES),
                where("date", ">=", ts(start.getTime() - FUEL_LOOKBACK_DAYS * DAY_MS)),
                where("date", "<", ts(end.getTime()))))),
    ]);
    const [truckSnap, driverSnap, customerSnap, hubSnap, maintSnap, txSnap, hubSocSnap, socHubSnap, rates, fixedCosts] =
        await Promise.all([
            load(COLLECTIONS.TRUCKS, () => getDocs(collection(db, COLLECTIONS.TRUCKS))),
            load(COLLECTIONS.DRIVERS, () => getDocs(collection(db, COLLECTIONS.DRIVERS))),
            load(COLLECTIONS.CUSTOMERS, () => getDocs(collection(db, COLLECTIONS.CUSTOMERS))),
            load(COLLECTIONS.HUBS, () => getDocs(collection(db, COLLECTIONS.HUBS))),
            load(COLLECTIONS.MAINTENANCE, () => getDocs(collection(db, COLLECTIONS.MAINTENANCE))),
            load(COLLECTIONS.TRANSACTIONS, () => getDocs(collection(db, COLLECTIONS.TRANSACTIONS))),
            load(COLLECTIONS.HUB_SOC_DISTANCES, () => getDocs(collection(db, COLLECTIONS.HUB_SOC_DISTANCES))),
            load(COLLECTIONS.SOC_HUB_DISTANCES, () => getDocs(collection(db, COLLECTIONS.SOC_HUB_DISTANCES))),
            load(COLLECTIONS.PNL_RATES, listPnlRates),
            load(COLLECTIONS.PNL_FIXED_COSTS, listPnlFixedCosts),
        ]);

    // Tasks indexed by doc id AND by their `taskId` field — trip_records.taskId holds either.
    const taskByKey = new Map<string, Doc>();
    const taskDocs = new Map<string, Doc>();
    const indexTask = (s: Snap) => {
        const d = s.data() as Doc;
        taskDocs.set(s.id, d);
        taskByKey.set(s.id, d);
        const lt = str(d.taskId);
        if (lt) taskByKey.set(lt, d);
    };
    taskSnap.docs.forEach(indexTask);
    const wantedTaskKeys = new Set<string>();
    for (const s of [...tripSnap.docs, ...standbySnap.docs]) {
        const k = str(s.data().taskId);
        if (k && !taskByKey.has(k)) wantedTaskKeys.add(k);
    }
    if (wantedTaskKeys.size > 0) {
        await load(COLLECTIONS.TASKS, async () => {
            const keys = [...wantedTaskKeys];
            for (let i = 0; i < keys.length; i += 30) {
                const chunk = keys.slice(i, i + 30);
                const byId = await getDocs(query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk)));
                byId.docs.forEach(indexTask);
                const rest = chunk.filter((k) => !taskByKey.has(k));
                if (rest.length > 0) {
                    const byField = await getDocs(query(collection(db, COLLECTIONS.TASKS), where("taskId", "in", rest)));
                    byField.docs.forEach(indexTask);
                }
            }
        });
    }

    const driverNameByKey = new Map<string, string>();
    const driverIdentity: Record<string, string[]> = {};
    driverSnap.docs.forEach((s) => {
        const d = s.data() as Doc;
        const keys = driverIdentityKeys(d, s.id);
        const name = driverDisplayName(d, s.id);
        keys.forEach((k) => {
            driverNameByKey.set(k, name);
            driverIdentity[k] = keys;
        });
    });
    const customerName = new Map<string, string>();
    const planBasisCustomerIds: string[] = [];
    customerSnap.docs.forEach((s) => {
        const d = s.data() as Doc;
        customerName.set(s.id, str(d.name) ?? str(d.code) ?? s.id);
        if (d.billingDateBasis === "plan") planBasisCustomerIds.push(s.id);
    });
    const hubLabels: Record<string, string> = {};
    hubSnap.docs.forEach((s) => {
        const d = s.data() as Doc;
        const code = str(d.source_id) ?? str(d.hubId) ?? str(d.hubCode);
        if (!code) return;
        const label = primaryHubLabelFromFirestoreData(d);
        hubLabels[code.toUpperCase()] = label;
        hubLabels[normalizeDestinationCode(code)] ??= label;
    });

    const resolveCustomer = (trip: Doc | undefined, task: Doc | undefined) =>
        str(trip?.billingCustomerId) ?? str(task?.billingCustomerId) ?? str(task?.sourceHubLinkedCustomerId) ?? str(task?.destinationLinkedCustomerId);

    const trips: PnlTripInput[] = tripSnap.docs.map((s) => {
        const d = s.data() as Doc;
        const task = str(d.taskId) ? taskByKey.get(str(d.taskId)!) : undefined;
        const route = taskRoute(task);
        const breakdown = Array.isArray(d.billingMultiDeliveryBreakdown) ? (d.billingMultiDeliveryBreakdown as Doc[]) : [];
        const isMultiDrop = (route?.destinationCodes.length ?? 0) >= 2 || (!task && breakdown.length >= 2);
        const destinationCodes = isMultiDrop
            ? route && route.destinationCodes.length >= 2
                ? route.destinationCodes
                : breakdown.map((b) => normalizeDestinationCode(str(b.destination))).filter(Boolean)
            : [str(d.billingLookupDestination) ?? route?.destinationCodes[0] ?? normalizeDestinationCode(str(d.destination))].filter(
                  (c): c is string => !!c,
              );
        const customerId = resolveCustomer(d, task);
        const driverId = str(d.driverId);
        return {
            id: s.id,
            status: String(d.status ?? ""),
            truckId: str(d.truckId) ?? str(task?.truckId),
            driverId,
            driverName: driverId ? driverNameByKey.get(driverId) ?? str(d.driverName) : str(d.driverName),
            customerId,
            customerName: customerId ? customerName.get(customerId) : undefined,
            revenueThb: num(d.billingEstimateThb),
            checkInAt: toDate(task?.checkInAt),
            deliveredAt: toDate(d.deliveredTimestamp),
            originCode: str(d.billingLookupHubId) ?? route?.originCode ?? (extractHubId(str(d.origin)) || undefined),
            destinationCodes,
            isMultiDrop,
            vehicleClass: str(task?.truckType) ?? str(d.truckType),
            jobCategory: jobCategory(d.jobCategory) ?? jobCategory(task?.jobCategory),
            hasTask: !!task,
            helperDriverIds: Array.isArray(task?.helperDriverIds) ? (task!.helperDriverIds as string[]) : [],
            taskDate: toDate(task?.date),
        };
    });

    const standbys: PnlStandbyInput[] = standbySnap.docs.map((s) => {
        const d = s.data() as Doc;
        const task = str(d.taskId) ? taskByKey.get(str(d.taskId)!) : undefined;
        const customerId = str(d.customerId) ?? str(d.billingCustomerId) ?? resolveCustomer(undefined, task);
        const driverId = str(d.driverId);
        return {
            id: s.id,
            status: String(d.status ?? ""),
            truckId: str(d.truckId) ?? str(task?.truckId),
            driverId,
            driverName: driverId ? driverNameByKey.get(driverId) : undefined,
            customerId,
            customerName: customerId ? customerName.get(customerId) : undefined,
            revenueThb: num(d.billingEstimateThb),
            startedAt: toDate(d.startedAt),
            endedAt: toDate(d.endedAt),
            createdAt: toDate(d.createdAt),
            taskRoute: taskRoute(task),
            helperDriverIds: Array.isArray(task?.helperDriverIds) ? (task!.helperDriverIds as string[]) : [],
            taskCheckInAt: toDate(task?.checkInAt),
            taskDate: toDate(task?.date),
        };
    });

    const helperTasks: PnlHelperTaskInput[] = [...taskDocs.entries()].map(([id, d]) => ({
        id,
        driverId: str(d.driverId),
        helperDriverIds: Array.isArray(d.helperDriverIds) ? (d.helperDriverIds as string[]) : [],
        checkInAt: toDate(d.checkInAt),
        date: toDate(d.date),
    }));

    const trucks: PnlTruckInput[] = truckSnap.docs.map((s) => {
        const d = s.data() as Doc;
        return {
            id: s.id,
            plate: str(d.licensePlate) ?? s.id,
            ownershipType: str(d.ownershipType),
            subcontractorId: str(d.subcontractorId),
            truckStatus: str(d.truckStatus),
            insurancePremium: num(d.insurancePremium),
            taxExpense: num(d.taxExpense),
        };
    });

    const distances: PnlDistanceRow[] = [
        ...hubSocSnap.docs.map((s) => ({ table: "hub_soc" as const, ...distanceFields(s.data()) })),
        ...socHubSnap.docs.map((s) => ({ table: "soc_hub" as const, ...distanceFields(s.data()) })),
    ];

    const fuelRows: PnlFuelRowInput[] = [];
    const otherExpenses: PnlOtherExpenseInput[] = [];
    expenseSnap.docs.forEach((s) => {
        const d = s.data() as Doc;
        const date = toDate(d.date);
        if (!date) return;
        const base = {
            id: s.id,
            truckId: str(d.truckId),
            plate: str(d.truckLicensePlate) ?? str(d.licensePlate),
            date,
            amount: num(d.amount) ?? 0,
            status: String(d.status ?? "PENDING"),
        };
        if (d.type === "fuel") fuelRows.push({ ...base, volumeLiters: num(d.volumeLiters), odometer: num(d.odometer) });
        else if (d.type === "other") otherExpenses.push({ ...base, category: str(d.category) });
    });

    const maintenance: PnlMaintenanceInput[] = maintSnap.docs.map((s) => {
        const d = s.data() as Doc;
        return {
            id: s.id,
            truckId: str(d.truckId),
            type: String(d.type ?? ""),
            status: String(d.status ?? ""),
            endDate: str(d.endDate),
            checkOutAt: toDate(d.checkOutAt),
            startDate: str(d.startDate),
            amountThb: maintenanceDisplayCost({
                totalCost: num(d.totalCost),
                costLabor: num(d.costLabor),
                costParts: num(d.costParts),
                invoiceAmount: num(d.invoiceAmount),
            }),
            paymentMethod: str(d.paymentMethod),
            driverSubmitted: d.driverSubmitted === true,
        };
    });

    const renewals: PnlRenewalInput[] = [];
    txSnap.docs.forEach((s) => {
        const d = s.data() as Doc;
        if ((d.type !== "insurance" && d.type !== "tax") || !str(d.truckId)) return;
        const rawDate = str(d.date);
        const asDate = rawDate ? undefined : toDate(d.date);
        const date = rawDate ?? (asDate ? bangkokDateKey(asDate) : undefined);
        if (!date) return;
        renewals.push({ id: s.id, truckId: str(d.truckId)!, type: d.type, amount: num(d.amount) ?? 0, date });
    });

    return {
        inputs: {
            year,
            month,
            trips,
            standbys,
            helperTasks,
            trucks,
            distances,
            fuelRows,
            otherExpenses,
            maintenance,
            renewals,
            rates,
            fixedCosts,
            driverIdentity,
            planBasisCustomerIds,
        },
        hubLabels,
        trucks,
    };
}

function distanceFields(d: DocumentData): Omit<PnlDistanceRow, "table"> {
    return {
        hubId: String(d.hubId ?? ""),
        socId: String(d.socId ?? ""),
        distanceKm: num(d.distanceKm) ?? 0,
    };
}
