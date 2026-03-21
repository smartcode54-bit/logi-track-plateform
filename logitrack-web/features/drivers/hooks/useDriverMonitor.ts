import { useState, useEffect, useMemo } from "react";
import { format } from "date-fns";
import { collection, query, orderBy, onSnapshot, limit, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { TripRecord } from "@/validate/tripRecordSchema";
import { Driver } from "@/validate/driverSchema";
import { Task } from "@/validate/taskSchema";

function toDate(val: any): Date | null {
    if (!val) return null;
    if (val instanceof Date) return val;
    if (typeof val?.toDate === "function") return val.toDate();
    if (typeof val === "string") return new Date(val);
    return null;
}

export function useDriverMonitor() {
    // Data
    const [trips, setTrips] = useState<TripRecord[]>([]);
    const [drivers, setDrivers] = useState<Record<string, Driver>>({});
    const [tasks, setTasks] = useState<Task[]>([]);
    const [hubs, setHubs] = useState<{ source_id: string; source_name_en?: string }[]>([]);
    const [loading, setLoading] = useState(true);
    const [incidentReportsByTripId, setIncidentReportsByTripId] = useState<Record<string, { description: string; delayCause: string | null; createdAt: Date | null }>>({});

    // Filters
    const [date, setDate] = useState<Date | undefined>(undefined);
    const [statusFilter, setStatusFilter] = useState("all");
    const [jobTypeFilter, setJobTypeFilter] = useState<string>("all");
    const [searchQuery, setSearchQuery] = useState("");

    // Detail Dialogs
    const [detailTrip, setDetailTrip] = useState<TripRecord | null>(null);
    const [previewPhoto, setPreviewPhoto] = useState<{ url: string; type: string; address?: string } | null>(null);
    const [editTripDialogOpen, setEditTripDialogOpen] = useState(false);

    // Pagination
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 15;

    // ─── Fetch trip_records ─────────────────────────────────
    useEffect(() => {
        setLoading(true);
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            orderBy("createdAt", "desc"),
            limit(200)
        );
        const unsub = onSnapshot(
            q,
            (snap) => {
                const list: TripRecord[] = snap.docs.map((d) => {
                    const data = d.data();
                    return {
                        id: d.id,
                        ...data,
                        createdAt: toDate(data.createdAt),
                        updatedAt: toDate(data.updatedAt),
                        deliveredTimestamp: toDate(data.deliveredTimestamp),
                    } as TripRecord;
                });
                setTrips(list);
                setLoading(false);
            },
            (err) => {
                console.error("Error fetching trip_records:", err);
                setLoading(false);
            }
        );
        return () => unsub();
    }, []);

    // ─── Fetch drivers ──────────────────────────────────────
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

    // ─── Fetch incident reports ───────────────────────────────
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

    // ─── Fetch hubs ──────────────────────────────────────────
    const fetchHubs = async () => {
        const snap = await getDocs(collection(db, COLLECTIONS.HUBS));
        const list = snap.docs.map((d) => {
            const data = d.data();
            return {
                source_id: (data.source_id ?? data.hubId ?? data.hubCode ?? "").toString(),
                source_name_en: (data.source_name_en ?? data.hubName ?? "").toString() || undefined,
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

    const getDriver = (driverId?: string): Driver | null => {
        if (!driverId) return null;
        return driversByAuthId[driverId] ?? drivers[driverId] ?? null;
    };

    const sourceIdToName = useMemo(() => {
        const map: Record<string, string> = {};
        hubs.forEach((h) => {
            const id = (h.source_id ?? "").trim();
            if (id) map[id] = (h.source_name_en ?? id).trim() || id;
        });
        return map;
    }, [hubs]);

    const getSourceDisplayName = (code?: string | null): string => {
        if (code == null || String(code).trim() === "") return "-";
        const key = String(code).trim();
        return sourceIdToName[key] ?? key;
    };

    // ─── Fetch first_mile_tasks for check-in stats ──────────
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

    // ─── Stats ──────────────────────────────────────────────
    const stats = useMemo(() => {
        const total = trips.length;
        const inTransit = trips.filter((t) => t.status === "in_transit").length;
        const delivered = trips.filter((t) => t.status === "delivered").length;
        const loadingCount = trips.filter((t) => t.status === "loading").length;

        const activeTasks = tasks.filter((t) => ["Assigned", "Checked in", "In-Transit", "Completed"].includes(t.status));
        const checkedInTasks = tasks.filter((t) => t.status === "Checked in");
        const checkInActual = checkedInTasks.length;
        const checkInTotal = activeTasks.length;

        return { total, inTransit, delivered, loading: loadingCount, checkInActual, checkInTotal };
    }, [trips, tasks]);

    // ─── Filtering ──────────────────────────────────────────
    const filteredTrips = useMemo(() => {
        return trips.filter((trip) => {
            if (date) {
                const tripDate = toDate(trip.createdAt);
                if (tripDate) {
                    const filterStr = format(date, "dd/MM/yyyy");
                    const tripStr = format(tripDate, "dd/MM/yyyy");
                    if (filterStr !== tripStr) return false;
                } else {
                    return false;
                }
            }

            if (statusFilter !== "all") {
                if (statusFilter === "incident") {
                    if ((!trip.id || !incidentReportsByTripId[trip.id]) && trip.status !== "incident") return false;
                } else if (trip.status !== statusFilter) {
                    return false;
                }
            }

            if (jobTypeFilter !== "all" && trip.jobType !== jobTypeFilter) return false;

            if (searchQuery.trim()) {
                const q = searchQuery.toLowerCase();
                const driver = getDriver(trip.driverId);
                const driverName = driver
                    ? `${driver.firstName} ${driver.lastName}`.toLowerCase()
                    : "";
                const tripId = (trip.id || "").toLowerCase();
                const spxId = (trip.spxTripId || "").toLowerCase();
                if (
                    !driverName.includes(q) &&
                    !tripId.includes(q) &&
                    !spxId.includes(q)
                )
                    return false;
            }

            return true;
        });
    }, [trips, date, statusFilter, jobTypeFilter, searchQuery, drivers, driversByAuthId, incidentReportsByTripId]);

    // ─── Pagination ─────────────────────────────────────────
    const paginatedTrips = useMemo(() => {
         return filteredTrips.slice(
            (currentPage - 1) * itemsPerPage,
            currentPage * itemsPerPage
         );
    }, [filteredTrips, currentPage, itemsPerPage]);

    const totalPages = Math.ceil(filteredTrips.length / itemsPerPage);

    useEffect(() => {
        setCurrentPage(1);
    }, [date, statusFilter, jobTypeFilter, searchQuery]);

    const checkInAtByTaskId = useMemo(() => {
        const map: Record<string, Date | null> = {};
        tasks.forEach((t) => {
            const at = (t as Task & { checkInAt?: Date | null }).checkInAt;
            if (t.id && at) map[t.id] = at;
        });
        return map;
    }, [tasks]);

    return {
        trips,
        paginatedTrips,
        totalPages,
        currentPage,
        setCurrentPage,
        stats,
        filteredTrips,
        loading,
        date,
        setDate,
        statusFilter,
        setStatusFilter,
        jobTypeFilter,
        setJobTypeFilter,
        searchQuery,
        setSearchQuery,
        detailTrip,
        setDetailTrip,
        previewPhoto,
        setPreviewPhoto,
        editTripDialogOpen,
        setEditTripDialogOpen,
        incidentReportsByTripId,
        checkInAtByTaskId,
        getDriver,
        getSourceDisplayName,
        fetchHubs,
        itemsPerPage
    };
}
