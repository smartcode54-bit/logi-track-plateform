/**
 * Firebase Firestore Collection Names
 * 
 * IMPORTANT: Always use these constants instead of hardcoded strings
 * to prevent typos and enable easy refactoring.
 * 
 * @example
 * import { COLLECTIONS } from "@/lib/collections";
 * const docRef = doc(db, COLLECTIONS.USERS, id);
 */

export const COLLECTIONS = {
    /** User accounts and profiles */
    USERS: "users",

    /** Collection for both First Mile and Line Haul tasks */
    TASKS: "tasks",

    /** Truck/vehicle inventory */
    TRUCKS: "trucks",

    /** Subcontractor companies/individuals */
    SUBCONTRACTORS: "subcontractors",

    /** Customers / LSPs (for billing, driver IDs – e.g. SPX) */
    CUSTOMERS: "customers",

    /** Driver-to-truck assignments */
    ASSIGNMENTS: "truckAssignment",

    /** Drivers personnel */
    DRIVERS: "drivers",

    /** Waitlist signups */
    WAITLIST: "waitlist",

    /** Financial transactions (Tax, Insurance, Maintenance) */
    TRANSACTIONS: "transactions",

    /** Maintenance history records */
    MAINTENANCE: "maintenance",

    /** Trip records (driver jobs - first_mile / line_haul) */
    TRIP_RECORDS: "trip_records",

    /** Hub and SOC locations (pickup/delivery points) */
    HUBS: "hubs",

    /** Cached driving distance/duration from each Hub to each SOC (Google Distance Matrix) */
    HUB_SOC_DISTANCES: "hub_soc_distances",

    /** Cached driving distance/duration from each SOC to each Hub (Google Distance Matrix) — แยกจาก hub_soc_distances */
    SOC_HUB_DISTANCES: "soc_hub_distances",

    /** Vehicle expenses (fuel refills, other expenses from mobile) */
    VEHICLE_EXPENSES: "vehicle_expenses",

    /** Admin–driver chat rooms */
    CHATS: "chats",

    /** Incident reports (delivery delay / problem reports from driver or admin) */
    INCIDENT_REPORTS: "incidentReport",

    /** Broadcast history (admin sends; for listing past broadcasts) */
    BROADCASTS: "broadcasts",

    /** System metadata (e.g. distances_last_calculated) */
    METADATA: "metadata",

    /** Role-permission overrides (doc ID = roleId) */
    PERMISSIONS_CONFIG: "permissions_config",
} as const;

/** Type for collection names */
export type CollectionName = typeof COLLECTIONS[keyof typeof COLLECTIONS];
