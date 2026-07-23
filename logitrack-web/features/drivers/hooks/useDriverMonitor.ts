import { useState, useEffect, useMemo, useCallback } from "react";
import {
    collection,
    query,
    orderBy,
    onSnapshot,
    where,
    limit,
    getDocs,
    startAfter,
    Timestamp,
    type QueryDocumentSnapshot,
    type DocumentData,
    type QueryConstraint,
    doc,
    writeBatch,
    serverTimestamp,
    documentId,
} from "firebase/firestore";
import { startOfDay, endOfDay, subDays, differenceInCalendarDays } from "date-fns";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { TripRecord } from "@/validate/tripRecordSchema";
import { Driver } from "@/validate/driverSchema";
import { buildHubCodeToDisplayMapFromEntries, resolveHubOrSocDisplay } from "@/lib/hubDisplay";
import { Task } from "@/validate/taskSchema";
import {
    computeTripBilling,
    fetchFuelAdjustmentsForCustomers,
    fetchRateEntriesForCustomers,
    type TripBillingComputed,
    resolveTaskCustomerId,
    extractHubId,
    normalizeDestinationCode,
} from "@/lib/billingRates";
import { normalizeVehicleClass } from "@/lib/billingCompute";
import {
    PLATE_FILTER_ALL,
    buildPlateFilterOptions,
    resolveTripPlate,
    rowMatchesPlateFilter,
    type PlateFilterOption,
} from "@/lib/truckPlate";
import {
    VEHICLE_CLASS_FILTER_ALL,
    buildVehicleClassOptions,
    rowMatchesVehicleClass,
    type VehicleClassOption,
} from "@/lib/vehicleClass";
import {
    PLACE_FILTER_ALL,
    buildPlaceFilterOptions,
    rowMatchesPlaceFilter,
    valueMatchesPlaceFilter,
    type PlaceFilterOption,
    type PlaceMaps,
} from "@/lib/placeFilter";

export interface BillingDebugInfo {
    taskFound: boolean;
    customerId: string;
    hubId: string;
    destinationCode: string;
    vehicleClass: string;
    rateEntriesForCustomer: number;
    failReason: "no_task" | "no_customer" | "no_rate_match" | "ok";
}
import { useCustomerScope } from "@/hooks/useCustomerScope";

/**
 * Driver monitor trip_records loading:
 * - Default date range is rolling last DRIVER_MONITOR_DEFAULT_RANGE_DAYS (inclusive); max span is DRIVER_MONITOR_MAX_RANGE_DAYS (clampDateRange).
 * - Realtime listener loads all docs in [createdAt] range (no limit). Very wide ranges or many docs can stress memory and UI; UI shows a soft warning when trip count is high.
 * - Documents missing or with non-Timestamp createdAt will not match the range query; writers should set createdAt (e.g. serverTimestamp); reads coerce string/number to Date where possible.
 */

/** Default = last 30 calendar days inclusive (today + 29 prior days). */
export const DRIVER_MONITOR_DEFAULT_RANGE_DAYS = 30;

/** Max span users may select (read / memory guard). */
export const DRIVER_MONITOR_MAX_RANGE_DAYS = 180;

/** Select value for trips with no partner / channel code (not a real partner code). */
export const DRIVER_MONITOR_PARTNER_NONE = "__none__";

const FETCH_PAGE_SIZE = 500;

export function effectivePartnerCode(trip: TripRecord): string {
    const top = trip.partnerCode?.trim();
    if (top) return top;
    const ocr = trip.ocrData?.partnerCode?.trim();
    return ocr || "";
}

/**
 * The filters applied to a trip, on screen and in the export dialog alike.
 *
 * Passed as one object rather than as positional arguments: the list has grown past the point where
 * call sites can be read safely, and every filter added since has to reach two call sites.
 */
export type ClientFilterCriteria = {
    driverFilter: string;
    statusFilter: string;
    jobTypeFilter: string;
    partnerFilter: string;
    /** PLATE_FILTER_ALL | `id:<truckId>` | `plate:<raw>` | PLATE_FILTER_NONE */
    plateFilter: string;
    /** VEHICLE_CLASS_FILTER_ALL | normalised class code | VEHICLE_CLASS_FILTER_NONE */
    vehicleClassFilter: string;
    /** PLACE_FILTER_ALL | `code:<CODE>` | `raw:<string>` | PLACE_FILTER_NONE */
    originFilter: string;
    /** Same shape as originFilter; matches ANY delivery stop of a multi-drop trip (ADR 0006 §5). */
    destinationFilter: string;
    searchQuery: string;
};

/** Criteria for export dialog — independent from on-screen filters */
export type ExportFilterCriteria = ClientFilterCriteria & {
    dateFrom: Date | null;
    dateTo: Date | null;
};

/** Everything the predicate needs that is not a user-chosen filter value. */
type FilterDeps = {
    incidentReportsByTripId: Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>;
    getDriver: (driverId?: string) => Driver | null;
    getTripTruck: (trip: TripRecord) => TripTruck;
    placeMaps: PlaceMaps;
    customerScopeId?: string | null;
};

/**
 * The truck a trip ran on, for display and filtering.
 *
 * Resolved from the trip's own snapshot then its task — never from `drivers.activeTruck`, which is
 * live state and would restamp historical rows with today's truck (ADR 0005 §6). `vehicleClass` is
 * the RAW class string (trip.truckType ‖ task.truckType); the filter normalises it (lib/vehicleClass).
 */
export type TripTruck = { truckId?: string | null; plate?: string | null; vehicleClass?: string | null };

export function defaultDateRange(): { from: Date; to: Date } {
    const to = endOfDay(new Date());
    const from = startOfDay(subDays(new Date(), DRIVER_MONITOR_DEFAULT_RANGE_DAYS - 1));
    return { from, to };
}

/** Ensures from <= to and span does not exceed DRIVER_MONITOR_MAX_RANGE_DAYS. */
export function clampDateRange(from: Date, to: Date): { from: Date; to: Date } {
    let f = startOfDay(from);
    let t = endOfDay(to);
    if (f > t) {
        const tmp = f;
        f = startOfDay(t);
        t = endOfDay(tmp);
    }
    const spanDays = differenceInCalendarDays(t, f) + 1;
    if (spanDays > DRIVER_MONITOR_MAX_RANGE_DAYS) {
        t = endOfDay(f);
        f = startOfDay(subDays(t, DRIVER_MONITOR_MAX_RANGE_DAYS - 1));
    }
    return { from: f, to: t };
}

function mapTripDoc(d: QueryDocumentSnapshot<DocumentData>): TripRecord {
    const data = d.data();
    return {
        id: d.id,
        ...data,
        createdAt: toDate(data.createdAt),
        updatedAt: toDate(data.updatedAt),
        deliveredTimestamp: toDate(data.deliveredTimestamp),
        std: toDate(data.std) ?? undefined,
        sta: toDate(data.sta) ?? undefined,
        ata: toDate(data.ata) ?? undefined,
    } as TripRecord;
}

function toDate(val: any): Date | null {
    if (val == null) return null;
    if (val instanceof Date) return val;
    if (typeof val?.toDate === "function") return val.toDate();
    if (typeof val === "string") return new Date(val);
    if (typeof val === "number" && Number.isFinite(val)) return new Date(val);
    return null;
}

function tripInDateRange(trip: TripRecord, from: Date, to: Date): boolean {
    const tripDate = toDate(trip.createdAt);
    if (!tripDate) return false;
    return tripDate >= startOfDay(from) && tripDate <= endOfDay(to);
}

/** The destinations a trip can be matched by: every delivery stop, or its single planned destination. */
export function tripDestinations(trip: TripRecord): Array<string | null | undefined> {
    const stops = trip.deliveryStopsProgress ?? [];
    if (stops.length > 0) return stops.map((s) => s.destination);
    return [trip.destination];
}

function tripMatchesClientFilters(
    trip: TripRecord,
    filters: ClientFilterCriteria,
    deps: FilterDeps
): boolean {
    const {
        driverFilter,
        statusFilter,
        jobTypeFilter,
        partnerFilter,
        plateFilter,
        vehicleClassFilter,
        originFilter,
        destinationFilter,
        searchQuery,
    } = filters;
    const { incidentReportsByTripId, getDriver, getTripTruck, placeMaps, customerScopeId } = deps;

    // Customer scope: only show trips for their customer
    if (customerScopeId && trip.billingCustomerId !== customerScopeId) {
        return false;
    }

    if (statusFilter !== "all") {
        if (statusFilter === "incident") {
            if ((!trip.id || !incidentReportsByTripId[trip.id]) && trip.status !== "incident") return false;
        } else if (trip.status !== statusFilter) {
            return false;
        }
    }

    if (jobTypeFilter !== "all" && trip.jobType !== jobTypeFilter) return false;

    if (partnerFilter !== "all") {
        const pc = effectivePartnerCode(trip);
        if (partnerFilter === DRIVER_MONITOR_PARTNER_NONE) {
            if (pc !== "") return false;
        } else if (pc !== partnerFilter) {
            return false;
        }
    }

    if (plateFilter !== PLATE_FILTER_ALL && !rowMatchesPlateFilter(getTripTruck(trip), plateFilter)) {
        return false;
    }

    if (vehicleClassFilter !== VEHICLE_CLASS_FILTER_ALL && !rowMatchesVehicleClass(getTripTruck(trip).vehicleClass, vehicleClassFilter)) {
        return false;
    }

    if (originFilter !== PLACE_FILTER_ALL && !rowMatchesPlaceFilter([trip.origin], originFilter, placeMaps)) {
        return false;
    }

    // A multi-drop trip matches if ANY stop matches — matching only the planned destination would
    // make it unfindable by the stops it actually served (ADR 0006 §5).
    if (
        destinationFilter !== PLACE_FILTER_ALL &&
        !rowMatchesPlaceFilter(tripDestinations(trip), destinationFilter, placeMaps)
    ) {
        return false;
    }

    if (driverFilter !== "all") {
        const tid = trip.driverId;
        if (!tid) return false;
        if (tid !== driverFilter) {
            const dr = getDriver(tid);
            if (!dr) return false;
            if (
                (dr.authId ?? dr.id) !== driverFilter &&
                dr.id !== driverFilter &&
                dr.authId !== driverFilter
            )
                return false;
        }
    }

    if (searchQuery.trim()) {
        const qv = searchQuery.toLowerCase();
        const driver = getDriver(trip.driverId);
        const driverName = driver ? `${driver.firstName} ${driver.lastName}`.toLowerCase() : "";
        const tripId = (trip.id || "").toLowerCase();
        const spxId = (trip.spxTripId || "").toLowerCase();
        if (!driverName.includes(qv) && !tripId.includes(qv) && !spxId.includes(qv)) return false;
    }

    return true;
}

function tripMatchesExportCriteria(
    trip: TripRecord,
    criteria: ExportFilterCriteria,
    deps: FilterDeps
): boolean {
    if (criteria.dateFrom && criteria.dateTo && !tripInDateRange(trip, criteria.dateFrom, criteria.dateTo)) {
        return false;
    }
    // Criteria persisted by an older session can lack the newer filters — default them to "all"
    // rather than letting `undefined` fall through as a non-matching selection.
    return tripMatchesClientFilters(
        trip,
        {
            ...criteria,
            plateFilter: criteria.plateFilter ?? PLATE_FILTER_ALL,
            vehicleClassFilter: criteria.vehicleClassFilter ?? VEHICLE_CLASS_FILTER_ALL,
            originFilter: criteria.originFilter ?? PLACE_FILTER_ALL,
            destinationFilter: criteria.destinationFilter ?? PLACE_FILTER_ALL,
        },
        deps
    );
}

/** True when export range fits inside [loadFrom, loadTo] at day precision. */
export function isExportRangeCoveredByLoaded(
    exportFrom: Date | null,
    exportTo: Date | null,
    loadFrom: Date,
    loadTo: Date
): boolean {
    if (!exportFrom || !exportTo) return true;
    return (
        startOfDay(exportFrom) >= startOfDay(loadFrom) && endOfDay(exportTo) <= endOfDay(loadTo)
    );
}

/**
 * Fetch all trip_records in [from, to] via paginated getDocs (for export when range exceeds loaded snapshot).
 */
export async function fetchTripsForDateRange(from: Date, to: Date): Promise<TripRecord[]> {
    const startTs = Timestamp.fromDate(startOfDay(from));
    const endTs = Timestamp.fromDate(endOfDay(to));
    const col = collection(db, COLLECTIONS.TRIP_RECORDS);
    const all: TripRecord[] = [];
    let lastDoc: QueryDocumentSnapshot<DocumentData> | undefined;

    for (;;) {
        const parts: QueryConstraint[] = [
            where("createdAt", ">=", startTs),
            where("createdAt", "<=", endTs),
            orderBy("createdAt", "desc"),
            limit(FETCH_PAGE_SIZE),
        ];
        if (lastDoc) parts.push(startAfter(lastDoc));
        const q = query(col, ...parts);
        const snap = await getDocs(q);
        if (snap.empty) break;
        snap.docs.forEach((d) => all.push(mapTripDoc(d)));
        lastDoc = snap.docs[snap.docs.length - 1];
        if (snap.docs.length < FETCH_PAGE_SIZE) break;
    }
    return all;
}

/**
 * Resolves the truck per trip for a set of trips fetched OUTSIDE the loaded window (wide-range
 * export). The on-screen path gets this for free from the hook's `taskById` join; an export over a
 * wider range has no such map, and without it a plate-filtered export would silently drop every row
 * whose plate lives on its task rather than on the trip snapshot.
 */
async function fetchTruckByTripId(tripsToResolve: TripRecord[]): Promise<Record<string, TripTruck>> {
    const map: Record<string, TripTruck> = {};
    // Fetch the task when either the plate OR the vehicle class is missing from the trip snapshot
    // (both usually live on the task, not the trip).
    const needTask = tripsToResolve.filter((t) => t.id && t.taskId && (!t.truckLicensePlate || !t.truckType));
    const taskInfo = new Map<string, { truckId?: string; licensePlate?: string; truckType?: string }>();

    const taskIds = Array.from(new Set(needTask.map((t) => t.taskId as string)));
    for (let i = 0; i < taskIds.length; i += 30) {
        const chunk = taskIds.slice(i, i + 30);
        const snap = await getDocs(
            query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk))
        );
        snap.docs.forEach((d) => {
            const data = d.data();
            taskInfo.set(d.id, { truckId: data.truckId, licensePlate: data.licensePlate, truckType: data.truckType });
        });
    }

    tripsToResolve.forEach((trip) => {
        if (!trip.id) return;
        const info = trip.taskId ? taskInfo.get(trip.taskId) : undefined;
        map[trip.id] = {
            truckId: trip.truckId || info?.truckId || null,
            plate: resolveTripPlate({ tripPlate: trip.truckLicensePlate, taskPlate: info?.licensePlate }),
            vehicleClass: trip.truckType || info?.truckType || null,
        };
    });
    return map;
}

export function useDriverMonitor() {
    const { customerScopeId, isCustomer } = useCustomerScope();

    const [trips, setTrips] = useState<TripRecord[]>([]);
    const [drivers, setDrivers] = useState<Record<string, Driver>>({});
    const [tasks, setTasks] = useState<Task[]>([]);
    const [hubs, setHubs] = useState<
        {
            source_id: string;
            source_name_en?: string;
            source_name_th?: string;
            station_type?: "HUB" | "SOC";
            linkedCustomerId?: string;
            linkedCustomerName?: string;
            customerLinkKind?: string;
        }[]
    >([]);
    const [loading, setLoading] = useState(true);
    const [incidentReportsByTripId, setIncidentReportsByTripId] = useState<
        Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>
    >({});
    const [tripBillingByTripId, setTripBillingByTripId] = useState<Record<string, TripBillingComputed>>({});
    const [billingDebugByTripId, setBillingDebugByTripId] = useState<Record<string, BillingDebugInfo>>({});
    // Check-in time per task, keyed by BOTH the task doc id and task.taskId so a trip resolves via
    // trip.taskId (its task doc id) — see the Driver Monitor "Check-in" column. Built from the same
    // full-coverage taskById the billing effect assembles (realtime tasks + older fetched tasks), so
    // every loaded trip resolves, not just the most recent 500.
    const [checkInAtByTaskId, setCheckInAtByTaskId] = useState<Record<string, Date | null>>({});

    const [dateFrom, setDateFrom] = useState<Date>(() => defaultDateRange().from);
    const [dateTo, setDateTo] = useState<Date>(() => defaultDateRange().to);

    const [driverFilter, setDriverFilter] = useState("all");
    const [statusFilter, setStatusFilter] = useState("all");
    const [jobTypeFilter, setJobTypeFilter] = useState<string>("all");
    const [partnerFilter, setPartnerFilter] = useState<string>("all");
    const [plateFilter, setPlateFilter] = useState<string>(PLATE_FILTER_ALL);
    const [vehicleClassFilter, setVehicleClassFilter] = useState<string>(VEHICLE_CLASS_FILTER_ALL);
    const [originFilter, setOriginFilter] = useState<string>(PLACE_FILTER_ALL);
    const [destinationFilter, setDestinationFilter] = useState<string>(PLACE_FILTER_ALL);
    const [searchQuery, setSearchQuery] = useState("");

    /** trip doc id → the truck that trip ran on, resolved from the trip snapshot then its task. */
    const [truckByTripId, setTruckByTripId] = useState<Record<string, TripTruck>>({});

    const [detailTrip, setDetailTrip] = useState<TripRecord | null>(null);
    /** Index into [detailTrip.photos] when the photo preview dialog is open. */
    const [photoPreviewStartIndex, setPhotoPreviewStartIndex] = useState<number | null>(null);
    const [editTripDialogOpen, setEditTripDialogOpen] = useState(false);

    const [currentPage, setCurrentPage] = useState(1);
    const [itemsPerPage, setItemsPerPage] = useState(15);

    // ─── trip_records: range on createdAt (realtime). Docs without/wrong createdAt never match. ───
    useEffect(() => {
        setLoading(true);
        const { from, to } = clampDateRange(dateFrom, dateTo);
        const startTs = Timestamp.fromDate(from);
        const endTs = Timestamp.fromDate(to);
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            where("createdAt", ">=", startTs),
            where("createdAt", "<=", endTs),
            orderBy("createdAt", "desc")
        );
        const unsub = onSnapshot(
            q,
            (snap) => {
                const list = snap.docs.map(mapTripDoc);
                setTrips(list);
                setLoading(false);
            },
            (err) => {
                console.error("Error fetching trip_records:", err);
                setLoading(false);
            }
        );
        return () => unsub();
    }, [dateFrom, dateTo]);

    useEffect(() => {
        const q = query(collection(db, COLLECTIONS.DRIVERS), limit(300));
        const unsub = onSnapshot(q, (snap) => {
            const map: Record<string, Driver> = {};
            snap.docs.forEach((d) => {
                const data = d.data();
                map[d.id] = { id: d.id, ...data } as Driver;
            });
            setDrivers(map);
        });
        return () => unsub();
    }, []);

    useEffect(() => {
        const q = query(
            collection(db, COLLECTIONS.INCIDENT_REPORTS),
            orderBy("createdAt", "desc"),
            limit(500)
        );
        const unsub = onSnapshot(q, (snap) => {
            const map: Record<string, any> = {};
            snap.docs.forEach((d) => {
                const data = d.data();
                if (data.tripId && !map[data.tripId]) {
                    map[data.tripId] = {
                        description: data.description,
                        delayCause: data.delayCause,
                        createdAt: toDate(data.createdAt),
                    };
                }
            });
            setIncidentReportsByTripId(map);
        });
        return () => unsub();
    }, []);

    const fetchHubs = async () => {
        const snap = await getDocs(collection(db, COLLECTIONS.HUBS));
        const list = snap.docs.map((d) => {
            const data = d.data();
            return {
                source_id: (data.source_id ?? data.hubId ?? data.hubCode ?? "").toString(),
                source_name_en: (data.source_name_en ?? data.hubName ?? "").toString() || undefined,
                source_name_th:
                    (data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? "").toString().trim() ||
                    undefined,
                station_type: (data.station_type === "SOC" ? "SOC" : "HUB") as "SOC" | "HUB",
                linkedCustomerId: data.linkedCustomerId,
                linkedCustomerName: data.linkedCustomerName,
                customerLinkKind: data.customerLinkKind,
            };
        });
        setHubs(list);
    };

    useEffect(() => {
        fetchHubs();
    }, []);

    const driversByAuthId = useMemo(() => {
        const byAuth: Record<string, Driver> = {};
        Object.values(drivers).forEach((d) => {
            if (d.authId) byAuth[d.authId] = d;
        });
        return byAuth;
    }, [drivers]);

    const getDriver = useCallback(
        (driverId?: string): Driver | null => {
            if (!driverId) return null;
            return driversByAuthId[driverId] ?? drivers[driverId] ?? null;
        },
        [drivers, driversByAuthId]
    );

    const driverOptions = useMemo(() => {
        return Object.values(drivers)
            .map((d) => {
                const value = d.authId ?? d.id ?? "";
                if (!value) return null;
                const label = `${d.firstName ?? ""} ${d.lastName ?? ""}`.trim() || value;
                return { value, label };
            })
            .filter((o): o is { value: string; label: string } => o != null)
            .sort((a, b) => a.label.localeCompare(b.label));
    }, [drivers]);

    const partnerOptions = useMemo(() => {
        const set = new Set<string>();
        trips.forEach((t) => {
            const c = effectivePartnerCode(t);
            if (c) set.add(c);
        });
        return Array.from(set).sort((a, b) => a.localeCompare(b));
    }, [trips]);

    const getTripTruck = useCallback(
        (trip: TripRecord): TripTruck => {
            if (trip.id && truckByTripId[trip.id]) return truckByTripId[trip.id];
            // Before the task join resolves (or for a trip fetched outside the loaded window),
            // fall back to the trip's own snapshot only.
            return {
                truckId: trip.truckId ?? null,
                plate: resolveTripPlate({ tripPlate: trip.truckLicensePlate }),
                vehicleClass: trip.truckType ?? null,
            };
        },
        [truckByTripId]
    );

    /**
     * Plate + vehicle-class options come from the loaded rows (same approach as partnerOptions), so
     * every option matches at least one trip and orphan plates / the no-class bucket stay reachable.
     */
    const plateOptions = useMemo<PlateFilterOption[]>(
        () => buildPlateFilterOptions(trips.map(getTripTruck)),
        [trips, getTripTruck]
    );
    const vehicleClassOptions = useMemo<VehicleClassOption[]>(
        () => buildVehicleClassOptions(trips.map((t) => getTripTruck(t).vehicleClass)),
        [trips, getTripTruck]
    );

    const sourceIdToName = useMemo(() => buildHubCodeToDisplayMapFromEntries(hubs), [hubs]);

    const getSourceDisplayName = useCallback(
        (code?: string | null): string => resolveHubOrSocDisplay(code ?? null, sourceIdToName),
        [sourceIdToName]
    );

    // Map hub display name (TH/EN) → source_id for destination normalization
    // e.g. "ประเวศ18" → "SPK890146" when tasks store the display name instead of the code
    const hubDisplayNameToCode = useMemo(() => {
        const map = new Map<string, string>();
        hubs.forEach((h) => {
            const code = h.source_id.trim();
            if (!code) return;
            if (h.source_name_th?.trim()) map.set(h.source_name_th.trim(), code);
            if (h.source_name_en?.trim()) map.set(h.source_name_en.trim(), code);
        });
        return map;
    }, [hubs]);

    /**
     * The two maps the origin/destination resolver reads. Kept as SEPARATE directions on purpose:
     * merging code→name into name→code is what produced the "No rate" billing failure
     * (CLAUDE.md §39), where a value that was already a code got turned back into a display name.
     */
    const placeMaps = useMemo<PlaceMaps>(
        () => ({ codeToLabel: sourceIdToName, nameToCode: hubDisplayNameToCode }),
        [sourceIdToName, hubDisplayNameToCode]
    );

    /**
     * Origin/destination options, resolved to place identity so one hub is one option however it was
     * spelled. Built from the loaded trips (never the `hubs` master) so every option matches ≥1 trip
     * and unresolvable values stay reachable rather than being merged away (ADR 0006 §2-4).
     */
    const originOptions = useMemo<PlaceFilterOption[]>(
        () => buildPlaceFilterOptions(trips.map((t) => [t.origin]), placeMaps),
        [trips, placeMaps]
    );
    const destinationOptions = useMemo<PlaceFilterOption[]>(
        () => buildPlaceFilterOptions(trips.map(tripDestinations), placeMaps),
        [trips, placeMaps]
    );

    /** Per-stop predicate for the Excel export, so a filtered file totals exactly what was filtered. */
    const matchesDestinationFilter = useCallback(
        (rawDestination: string | null | undefined, selected: string): boolean =>
            valueMatchesPlaceFilter(rawDestination, selected, placeMaps),
        [placeMaps]
    );

    useEffect(() => {
        const q = query(
            collection(db, COLLECTIONS.TASKS),
            orderBy("createdAt", "desc"),
            limit(500)
        );
        const unsub = onSnapshot(q, (snap) => {
            const list = snap.docs.map((d) => {
                const data = d.data();
                return {
                    id: d.id,
                    ...data,
                    checkInAt: toDate(data.checkInAt),
                } as Task & { checkInAt?: Date | null };
            });
            setTasks(list);
        });
        return () => unsub();
    }, []);

    useEffect(() => {
        let cancelled = false;
        const run = async () => {
            const taskById = new Map<string, Task>();
            tasks.forEach((task) => {
                if (task.id) taskById.set(task.id, task);
                if (task.taskId) taskById.set(task.taskId, task);
            });

            // Fetch tasks not covered by the realtime listener (trips older than limit(500))
            const missingTaskDocIds = Array.from(
                new Set(
                    trips
                        .map((t) => t.taskId)
                        .filter((id): id is string => !!id && !taskById.has(id))
                )
            );
            if (missingTaskDocIds.length > 0) {
                const chunks: string[][] = [];
                for (let i = 0; i < missingTaskDocIds.length; i += 30) {
                    chunks.push(missingTaskDocIds.slice(i, i + 30));
                }
                for (const chunk of chunks) {
                    if (cancelled) return;
                    const snap = await getDocs(
                        query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk))
                    );
                    snap.docs.forEach((d) => {
                        const data = d.data();
                        const task = { id: d.id, ...data } as Task;
                        taskById.set(d.id, task);
                        if (data.taskId) taskById.set(String(data.taskId), task);
                    });
                }
            }

            // Build the check-in lookup from the full-coverage taskById (keyed by both id and taskId),
            // coercing whatever the task carries (raw Timestamp for older fetched tasks) to a Date.
            const checkInMap: Record<string, Date | null> = {};
            taskById.forEach((task, key) => {
                const at = toDate((task as Task & { checkInAt?: unknown }).checkInAt);
                if (at) checkInMap[key] = at;
            });
            if (!cancelled) setCheckInAtByTaskId(checkInMap);

            // Truck per trip, from the same full-coverage taskById: the trip's own snapshot wins,
            // else the plate/truckId stamped on its task at check-in. Never activeTruck — that is
            // live state and would relabel historical rows with today's truck (ADR 0005 §6).
            const truckMap: Record<string, TripTruck> = {};
            trips.forEach((trip) => {
                if (!trip.id) return;
                const task = taskById.get(trip.taskId || "") ?? taskById.get(trip.id || "");
                const taskTruck = task as (Task & { truckId?: string; licensePlate?: string; truckType?: string }) | undefined;
                truckMap[trip.id] = {
                    truckId: trip.truckId || taskTruck?.truckId || null,
                    plate: resolveTripPlate({
                        tripPlate: trip.truckLicensePlate,
                        taskPlate: taskTruck?.licensePlate,
                    }),
                    vehicleClass: trip.truckType || taskTruck?.truckType || null,
                };
            });
            if (!cancelled) setTruckByTripId(truckMap);

            // Normalize task destination: display name → PDP code
            // e.g. "ประเวศ18" → "SPK890146" when tasks store display name instead of code
            const resolveTask = (task: Task | null | undefined): Task | null => {
                if (!task?.destination) return task ?? null;
                const resolved = hubDisplayNameToCode.get(task.destination.trim());
                if (resolved && resolved !== task.destination) {
                    return { ...task, destination: resolved };
                }
                return task;
            };

            const customerIds = new Set<string>();
            trips.forEach((trip) => {
                const task = taskById.get(trip.taskId || "") ?? taskById.get(trip.id || "");
                const customerId = resolveTaskCustomerId(task);
                if (customerId) customerIds.add(customerId);
            });
            const customerIdList = Array.from(customerIds);
            const [rateEntries, fuelAdjustments] = await Promise.all([
                fetchRateEntriesForCustomers(db, customerIdList),
                fetchFuelAdjustmentsForCustomers(db, customerIdList),
            ]);
            if (cancelled) return;
            const next: Record<string, TripBillingComputed> = {};
            const debugNext: Record<string, BillingDebugInfo> = {};
            trips.forEach((trip) => {
                if (!trip.id) return;
                const rawTask = taskById.get(trip.taskId || "") ?? taskById.get(trip.id || "");
                const task = resolveTask(rawTask);
                const result = computeTripBilling(trip, task, rateEntries, fuelAdjustments);
                if (result) {
                    next[trip.id] = result;
                    debugNext[trip.id] = {
                        taskFound: true,
                        customerId: result.customerId,
                        hubId: result.lookupHubId,
                        destinationCode: result.lookupDestination,
                        vehicleClass: normalizeVehicleClass(task?.truckType),
                        rateEntriesForCustomer: rateEntries.filter((e) => e.customerId === result.customerId).length,
                        failReason: "ok",
                    };
                } else {
                    const customerId = resolveTaskCustomerId(task);
                    const hubId = extractHubId(task?.sourceHub);
                    const destinationCode = normalizeDestinationCode(task?.destination);
                    const vehicleClass = normalizeVehicleClass(task?.truckType);
                    const rateEntriesForCustomer = customerId
                        ? rateEntries.filter((e) => e.customerId === customerId).length
                        : 0;
                    const failReason: BillingDebugInfo["failReason"] = !rawTask
                        ? "no_task"
                        : !customerId
                          ? "no_customer"
                          : "no_rate_match";
                    debugNext[trip.id] = {
                        taskFound: !!rawTask,
                        customerId,
                        hubId,
                        destinationCode,
                        vehicleClass,
                        rateEntriesForCustomer,
                        failReason,
                    };
                }
            });
            setTripBillingByTripId(next);
            setBillingDebugByTripId(debugNext);
        };
        void run();
        return () => {
            cancelled = true;
        };
    }, [trips, tasks, hubDisplayNameToCode]);

    useEffect(() => {
        const deliveredWithoutSnapshot = trips.filter((trip) => {
            if (!trip.id) return false;
            if (trip.status !== "delivered") return false;
            if (typeof trip.billingEstimateThb === "number") return false;
            return !!tripBillingByTripId[trip.id];
        });
        if (!deliveredWithoutSnapshot.length) return;

        let cancelled = false;
        const run = async () => {
            for (let i = 0; i < deliveredWithoutSnapshot.length; i += 200) {
                if (cancelled) return;
                const chunk = deliveredWithoutSnapshot.slice(i, i + 200);
                const batch = writeBatch(db);
                chunk.forEach((trip) => {
                    if (!trip.id) return;
                    const computed = tripBillingByTripId[trip.id];
                    if (!computed) return;
                    batch.update(doc(db, COLLECTIONS.TRIP_RECORDS, trip.id), {
                        billingEstimateThb: computed.finalRateThb,
                        billingBaseRateThb: computed.baseRateThb,
                        billingRateImportId: computed.rateImportId,
                        billingLookupHubId: computed.lookupHubId,
                        billingLookupDestination: computed.lookupDestination,
                        billingFuelAdjustmentId: computed.fuelAdjustmentId || null,
                        billingRateMultiplier: computed.rateMultiplier,
                        billingAddThbPerTrip: computed.addThbPerTrip,
                        billingEffectiveFromDateStr: computed.effectiveFromDateStr || null,
                        billingCustomerId: computed.customerId,
                        updatedAt: serverTimestamp(),
                    });
                });
                await batch.commit();
            }
        };
        void run();
        return () => {
            cancelled = true;
        };
    }, [trips, tripBillingByTripId]);

    const stats = useMemo(() => {
        const total = trips.length;
        const inTransit = trips.filter((t) => t.status === "in_transit").length;
        const delivered = trips.filter((t) => t.status === "delivered").length;

        const activeTasks = tasks.filter((t) =>
            ["Assigned", "Checked in", "In-Transit", "Completed"].includes(t.status)
        );
        const checkedInTasks = tasks.filter((t) => t.status === "Checked in");
        const checkInActual = checkedInTasks.length;
        const checkInTotal = activeTasks.length;

        return { total, inTransit, delivered, checkInActual, checkInTotal };
    }, [trips, tasks]);

    const filteredTrips = useMemo(() => {
        return trips.filter((trip) =>
            tripMatchesClientFilters(
                trip,
                {
                    driverFilter,
                    statusFilter,
                    jobTypeFilter,
                    partnerFilter,
                    plateFilter,
                    vehicleClassFilter,
                    originFilter,
                    destinationFilter,
                    searchQuery,
                },
                { incidentReportsByTripId, getDriver, getTripTruck, placeMaps, customerScopeId }
            )
        );
    }, [trips, driverFilter, statusFilter, jobTypeFilter, partnerFilter, plateFilter, vehicleClassFilter, originFilter, destinationFilter, searchQuery, incidentReportsByTripId, getDriver, getTripTruck, placeMaps, customerScopeId]);

    /**
     * How many client-side filters are narrowing the table right now — drives the "clear all" button.
     *
     * The date range is NOT counted: it is the Firestore query window rather than a predicate over
     * loaded rows, so clearing it would refetch. It has its own presets next to it.
     */
    const activeClientFilterCount = useMemo(
        () =>
            [
                driverFilter !== "all",
                statusFilter !== "all",
                jobTypeFilter !== "all",
                partnerFilter !== "all",
                plateFilter !== PLATE_FILTER_ALL,
                vehicleClassFilter !== VEHICLE_CLASS_FILTER_ALL,
                originFilter !== PLACE_FILTER_ALL,
                destinationFilter !== PLACE_FILTER_ALL,
                searchQuery.trim() !== "",
            ].filter(Boolean).length,
        [
            driverFilter,
            statusFilter,
            jobTypeFilter,
            partnerFilter,
            plateFilter,
            vehicleClassFilter,
            originFilter,
            destinationFilter,
            searchQuery,
        ]
    );

    /** Resets every filter counted above. Lives here because the hook owns the "cleared" sentinels. */
    const clearClientFilters = useCallback(() => {
        setDriverFilter("all");
        setStatusFilter("all");
        setJobTypeFilter("all");
        setPartnerFilter("all");
        setPlateFilter(PLATE_FILTER_ALL);
        setVehicleClassFilter(VEHICLE_CLASS_FILTER_ALL);
        setOriginFilter(PLACE_FILTER_ALL);
        setDestinationFilter(PLACE_FILTER_ALL);
        setSearchQuery("");
    }, []);

    const getTripsForExport = useCallback(
        (criteria: ExportFilterCriteria): TripRecord[] => {
            return trips.filter((trip) =>
                tripMatchesExportCriteria(trip, criteria, {
                    incidentReportsByTripId,
                    getDriver,
                    getTripTruck,
                    placeMaps,
                    customerScopeId,
                })
            );
        },
        [trips, incidentReportsByTripId, getDriver, getTripTruck, placeMaps, customerScopeId]
    );

    const getTripsForExportResolved = useCallback(
        async (criteria: ExportFilterCriteria): Promise<TripRecord[]> => {
            const { from: lf, to: lt } = clampDateRange(dateFrom, dateTo);
            let base: TripRecord[];
            let outOfWindow = false;
            if (!criteria.dateFrom || !criteria.dateTo) {
                base = trips;
            } else if (!isExportRangeCoveredByLoaded(criteria.dateFrom, criteria.dateTo, lf, lt)) {
                const { from, to } = clampDateRange(criteria.dateFrom, criteria.dateTo);
                base = await fetchTripsForDateRange(from, to);
                outOfWindow = true;
            } else {
                base = trips;
            }

            // Trips fetched outside the loaded window have no entry in truckByTripId, so resolve
            // their tasks before filtering — otherwise a plate or vehicle-class filter would drop
            // every row whose plate/class lives on the task rather than on the trip snapshot.
            const truckFilterActive =
                (criteria.plateFilter ?? PLATE_FILTER_ALL) !== PLATE_FILTER_ALL ||
                (criteria.vehicleClassFilter ?? VEHICLE_CLASS_FILTER_ALL) !== VEHICLE_CLASS_FILTER_ALL;
            let resolveTruck = getTripTruck;
            if (outOfWindow && truckFilterActive) {
                const fetched = await fetchTruckByTripId(base);
                resolveTruck = (trip: TripRecord) =>
                    (trip.id ? fetched[trip.id] : undefined) ?? {
                        truckId: trip.truckId ?? null,
                        plate: resolveTripPlate({ tripPlate: trip.truckLicensePlate }),
                        vehicleClass: trip.truckType ?? null,
                    };
            }

            // Origin/destination need no such join — they live on the trip doc itself and resolve
            // against maps already in memory (ADR 0006 §Consequences).
            return base.filter((trip) =>
                tripMatchesExportCriteria(trip, criteria, {
                    incidentReportsByTripId,
                    getDriver,
                    getTripTruck: resolveTruck,
                    placeMaps,
                    customerScopeId,
                })
            );
        },
        [trips, dateFrom, dateTo, incidentReportsByTripId, getDriver, getTripTruck, placeMaps, customerScopeId]
    );

    const paginatedTrips = useMemo(() => {
        return filteredTrips.slice((currentPage - 1) * itemsPerPage, currentPage * itemsPerPage);
    }, [filteredTrips, currentPage, itemsPerPage]);

    const totalPages = Math.ceil(filteredTrips.length / itemsPerPage) || 0;

    useEffect(() => {
        setCurrentPage(1);
    }, [dateFrom, dateTo, statusFilter, jobTypeFilter, partnerFilter, plateFilter, vehicleClassFilter, originFilter, destinationFilter, searchQuery, driverFilter]);

    useEffect(() => {
        setCurrentPage(1);
    }, [itemsPerPage]);

    const setDateRange = useCallback((from: Date, to: Date) => {
        const c = clampDateRange(from, to);
        setDateFrom(c.from);
        setDateTo(c.to);
    }, []);

    const getBillingForTrip = useCallback(
        (tripId?: string) => {
            if (!tripId) return null;
            return tripBillingByTripId[tripId] ?? null;
        },
        [tripBillingByTripId]
    );

    const getBillingDebug = useCallback(
        (tripId?: string): BillingDebugInfo | null => {
            if (!tripId) return null;
            return billingDebugByTripId[tripId] ?? null;
        },
        [billingDebugByTripId]
    );

    return {
        trips,
        paginatedTrips,
        totalPages,
        currentPage,
        setCurrentPage,
        stats,
        filteredTrips,
        loading,
        dateFrom,
        dateTo,
        setDateFrom,
        setDateTo,
        setDateRange,
        driverFilter,
        setDriverFilter,
        driverOptions,
        statusFilter,
        setStatusFilter,
        jobTypeFilter,
        setJobTypeFilter,
        partnerFilter,
        setPartnerFilter,
        partnerOptions,
        plateFilter,
        setPlateFilter,
        plateOptions,
        vehicleClassFilter,
        setVehicleClassFilter,
        vehicleClassOptions,
        originFilter,
        setOriginFilter,
        originOptions,
        destinationFilter,
        setDestinationFilter,
        destinationOptions,
        matchesDestinationFilter,
        getTripTruck,
        activeClientFilterCount,
        clearClientFilters,
        searchQuery,
        setSearchQuery,
        detailTrip,
        setDetailTrip,
        photoPreviewStartIndex,
        setPhotoPreviewStartIndex,
        editTripDialogOpen,
        setEditTripDialogOpen,
        incidentReportsByTripId,
        checkInAtByTaskId,
        getDriver,
        getSourceDisplayName,
        fetchHubs,
        hubs,
        itemsPerPage,
        setItemsPerPage,
        getTripsForExport,
        getTripsForExportResolved,
        getBillingForTrip,
        getBillingDebug,
    };
}
