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
} from "@/lib/billingRates";

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

/** Criteria for export dialog — independent from on-screen filters */
export type ExportFilterCriteria = {
    dateFrom: Date | null;
    dateTo: Date | null;
    driverFilter: string;
    statusFilter: string;
    jobTypeFilter: string;
    partnerFilter: string;
    searchQuery: string;
};

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

function tripMatchesClientFilters(
    trip: TripRecord,
    driverFilter: string,
    statusFilter: string,
    jobTypeFilter: string,
    partnerFilter: string,
    searchQuery: string,
    incidentReportsByTripId: Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>,
    getDriver: (driverId?: string) => Driver | null
): boolean {
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
    incidentReportsByTripId: Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>,
    getDriver: (driverId?: string) => Driver | null
): boolean {
    if (criteria.dateFrom && criteria.dateTo && !tripInDateRange(trip, criteria.dateFrom, criteria.dateTo)) {
        return false;
    }
    return tripMatchesClientFilters(
        trip,
        criteria.driverFilter,
        criteria.statusFilter,
        criteria.jobTypeFilter,
        criteria.partnerFilter,
        criteria.searchQuery,
        incidentReportsByTripId,
        getDriver
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

export function useDriverMonitor() {
    const [trips, setTrips] = useState<TripRecord[]>([]);
    const [drivers, setDrivers] = useState<Record<string, Driver>>({});
    const [tasks, setTasks] = useState<Task[]>([]);
    const [hubs, setHubs] = useState<
        { source_id: string; source_name_en?: string; source_name_th?: string }[]
    >([]);
    const [loading, setLoading] = useState(true);
    const [incidentReportsByTripId, setIncidentReportsByTripId] = useState<
        Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>
    >({});
    const [tripBillingByTripId, setTripBillingByTripId] = useState<Record<string, TripBillingComputed>>({});

    const [dateFrom, setDateFrom] = useState<Date>(() => defaultDateRange().from);
    const [dateTo, setDateTo] = useState<Date>(() => defaultDateRange().to);

    const [driverFilter, setDriverFilter] = useState("all");
    const [statusFilter, setStatusFilter] = useState("all");
    const [jobTypeFilter, setJobTypeFilter] = useState<string>("all");
    const [partnerFilter, setPartnerFilter] = useState<string>("all");
    const [searchQuery, setSearchQuery] = useState("");

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

    const sourceIdToName = useMemo(() => buildHubCodeToDisplayMapFromEntries(hubs), [hubs]);

    const getSourceDisplayName = useCallback(
        (code?: string | null): string => resolveHubOrSocDisplay(code ?? null, sourceIdToName),
        [sourceIdToName]
    );

    useEffect(() => {
        const q = query(
            collection(db, COLLECTIONS.TASKS),
            orderBy("createdAt", "desc"),
            limit(200)
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
            trips.forEach((trip) => {
                if (!trip.id) return;
                const task = taskById.get(trip.taskId || "") ?? taskById.get(trip.id || "");
                const result = computeTripBilling(trip, task, rateEntries, fuelAdjustments);
                if (result) next[trip.id] = result;
            });
            setTripBillingByTripId(next);
        };
        void run();
        return () => {
            cancelled = true;
        };
    }, [trips, tasks]);

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
        const loadingCount = trips.filter((t) => t.status === "loading").length;

        const activeTasks = tasks.filter((t) =>
            ["Assigned", "Checked in", "In-Transit", "Completed"].includes(t.status)
        );
        const checkedInTasks = tasks.filter((t) => t.status === "Checked in");
        const checkInActual = checkedInTasks.length;
        const checkInTotal = activeTasks.length;

        return { total, inTransit, delivered, loading: loadingCount, checkInActual, checkInTotal };
    }, [trips, tasks]);

    const filteredTrips = useMemo(() => {
        return trips.filter((trip) =>
            tripMatchesClientFilters(
                trip,
                driverFilter,
                statusFilter,
                jobTypeFilter,
                partnerFilter,
                searchQuery,
                incidentReportsByTripId,
                getDriver
            )
        );
    }, [trips, driverFilter, statusFilter, jobTypeFilter, partnerFilter, searchQuery, incidentReportsByTripId, getDriver]);

    const getTripsForExport = useCallback(
        (criteria: ExportFilterCriteria): TripRecord[] => {
            return trips.filter((trip) =>
                tripMatchesExportCriteria(trip, criteria, incidentReportsByTripId, getDriver)
            );
        },
        [trips, incidentReportsByTripId, getDriver]
    );

    const getTripsForExportResolved = useCallback(
        async (criteria: ExportFilterCriteria): Promise<TripRecord[]> => {
            const { from: lf, to: lt } = clampDateRange(dateFrom, dateTo);
            let base: TripRecord[];
            if (!criteria.dateFrom || !criteria.dateTo) {
                base = trips;
            } else if (!isExportRangeCoveredByLoaded(criteria.dateFrom, criteria.dateTo, lf, lt)) {
                const { from, to } = clampDateRange(criteria.dateFrom, criteria.dateTo);
                base = await fetchTripsForDateRange(from, to);
            } else {
                base = trips;
            }
            return base.filter((trip) =>
                tripMatchesExportCriteria(trip, criteria, incidentReportsByTripId, getDriver)
            );
        },
        [trips, dateFrom, dateTo, incidentReportsByTripId, getDriver]
    );

    const paginatedTrips = useMemo(() => {
        return filteredTrips.slice((currentPage - 1) * itemsPerPage, currentPage * itemsPerPage);
    }, [filteredTrips, currentPage, itemsPerPage]);

    const totalPages = Math.ceil(filteredTrips.length / itemsPerPage) || 0;

    useEffect(() => {
        setCurrentPage(1);
    }, [dateFrom, dateTo, statusFilter, jobTypeFilter, partnerFilter, searchQuery, driverFilter]);

    useEffect(() => {
        setCurrentPage(1);
    }, [itemsPerPage]);

    const checkInAtByTaskId = useMemo(() => {
        const map: Record<string, Date | null> = {};
        tasks.forEach((t) => {
            const at = (t as Task & { checkInAt?: Date | null }).checkInAt;
            if (t.id && at) map[t.id] = at;
        });
        return map;
    }, [tasks]);

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
        itemsPerPage,
        setItemsPerPage,
        getTripsForExport,
        getTripsForExportResolved,
        getBillingForTrip,
    };
}
