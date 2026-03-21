import { useState, useEffect, useMemo } from "react";
import { collection, query, orderBy, onSnapshot, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { TruckData, formatTimestamp } from "../services/truckService";

export function useTrucksList() {
    const [trucks, setTrucks] = useState<TruckData[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [refreshKey, setRefreshKey] = useState(0);
    const [isRefreshing, setIsRefreshing] = useState(false);

    // Filters
    const [searchQuery, setSearchQuery] = useState("");
    const [typeFilter, setTypeFilter] = useState("all");
    const [statusFilter, setStatusFilter] = useState("all");
    const [groupFilter, setGroupFilter] = useState("all");

    // Pagination
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

    useEffect(() => {
        setLoading(true);
        const trucksRef = collection(db, COLLECTIONS.TRUCKS);
        const q = query(trucksRef, orderBy("createdAt", "desc"), limit(100));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const trucksData: TruckData[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                trucksData.push({
                    id: doc.id,
                    ownershipType: (data.ownershipType as "own" | "subcontractor") || "own",
                    subcontractorId: data.subcontractorId || undefined,
                    licensePlate: data.licensePlate || "",
                    province: data.province || "",
                    vin: data.vin || "",
                    engineNumber: data.engineNumber || "",
                    truckStatus: data.truckStatus || "",
                    brand: data.brand || "",
                    model: data.model || "",
                    year: data.year || "",
                    color: data.color || "",
                    type: data.type || "",
                    seats: data.seats || "",
                    fuelType: data.fuelType || "",
                    engineCapacity: data.engineCapacity,
                    fuelCapacity: data.fuelCapacity,
                    maxLoadWeight: data.maxLoadWeight,
                    registrationDate: data.registrationDate || "",
                    buyingDate: data.buyingDate || "",
                    notes: data.notes || "",
                    images: data.images || [],
                    taxExpiryDate: data.taxExpiryDate,
                    insuranceExpiryDate: data.insuranceExpiryDate,
                    lastServiceDate: data.lastServiceDate,
                    nextServiceDate: data.nextServiceDate,
                    nextServiceMileage: data.nextServiceMileage,
                    currentMileage: data.currentMileage,
                    createdBy: data.createdBy || "",
                    createdAt: formatTimestamp(data.createdAt),
                    updatedAt: formatTimestamp(data.updatedAt),
                    currentAssignments: data.currentAssignments ? (data.currentAssignments as any[]).map(assignment => ({
                        driverId: assignment.driverId,
                        driverName: assignment.driverName,
                        assignedAt: formatTimestamp(assignment.assignedAt) as Date,
                        assignmentId: assignment.assignmentId
                    })) : (data.currentAssignment ? [{
                        driverId: data.currentAssignment.driverId,
                        driverName: data.currentAssignment.driverName,
                        assignedAt: formatTimestamp(data.currentAssignment.assignedAt) as Date,
                        assignmentId: data.currentAssignment.assignmentId
                    }] : []),
                } as TruckData);
            });
            setTrucks(trucksData);
            setLoading(false);
            setError(null);
        }, (err) => {
            console.error("Error fetching trucks:", err);
            setError("Failed to load trucks");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    const uniqueTypes = useMemo(() => {
        const types = new Set(trucks.map(t => t.type).filter(Boolean));
        return Array.from(types);
    }, [trucks]);

    const filteredTrucks = useMemo(() => {
        return trucks.filter(truck => {
            const matchSearch =
                truck.licensePlate.toLowerCase().includes(searchQuery.toLowerCase()) ||
                truck.model.toLowerCase().includes(searchQuery.toLowerCase()) ||
                truck.id.toLowerCase().includes(searchQuery.toLowerCase());

            const matchType = typeFilter === "all" || truck.type === typeFilter;
            const matchStatus = statusFilter === "all" || truck.truckStatus === statusFilter;
            const matchGroup = groupFilter === "all" ||
                (groupFilter === "own" && truck.ownershipType === "own") ||
                (groupFilter === "subcontractor" && truck.ownershipType === "subcontractor");

            return matchSearch && matchType && matchStatus && matchGroup;
        });
    }, [trucks, searchQuery, typeFilter, statusFilter, groupFilter]);

    const totalPages = Math.ceil(filteredTrucks.length / itemsPerPage);

    const handleCardFilterChange = ({ type, status }: { type: string | null; status: string | null }) => {
        if (!type && !status) {
            setGroupFilter("all");
            setStatusFilter("all");
            return;
        }
        else if (type === "sub") {
            setGroupFilter("subcontractor");
            if (status === "active") setStatusFilter("active");
            else if (status === "available") setStatusFilter("inactive");
            else if (status === "corrective") setStatusFilter("maintenance");
            else if (status === "pm") setStatusFilter("maintenance");
            else setStatusFilter("all");
        }
        else if (type === "own") {
            setGroupFilter("own");
            if (status === "active") setStatusFilter("active");
            else if (status === "available") setStatusFilter("inactive");
            else if (status === "corrective") setStatusFilter("maintenance");
            else if (status === "pm") setStatusFilter("maintenance");
            else setStatusFilter("all");
        }
        else if (type === "maintenance") {
            setGroupFilter("own");
            setStatusFilter("all");
        }
    };

    const clearFilters = () => {
        setSearchQuery("");
        setTypeFilter("all");
        setStatusFilter("all");
        setGroupFilter("all");
        setCurrentPage(1);
    };

    return {
        trucks,
        loading,
        error,
        refreshKey,
        setRefreshKey,
        isRefreshing,
        setIsRefreshing,
        searchQuery,
        setSearchQuery,
        typeFilter,
        setTypeFilter,
        statusFilter,
        setStatusFilter,
        groupFilter,
        setGroupFilter,
        currentPage,
        setCurrentPage,
        itemsPerPage,
        uniqueTypes,
        filteredTrucks,
        totalPages,
        handleCardFilterChange,
        clearFilters
    };
}
