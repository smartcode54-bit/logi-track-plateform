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

    /** Customer-specific billing rate rows (Hub -> destination -> vehicle class) */
    CUSTOMER_RATE_ENTRIES: "customer_rate_entries",

    /** Customer fuel-linked billing adjustments by effective date */
    CUSTOMER_FUEL_RATE_ADJUSTMENTS: "customer_fuel_rate_adjustments",

    /** Bangchak retail fuel snapshots (doc id yyyy-MM Bangkok; refreshed daily + manual sync) */
    FUEL_MONTHLY_SNAPSHOTS: "fuel_monthly_snapshots",

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

    /**
     * Broadcast history (admin sends; drivers read).
     * Field list: `lib/broadcastFirestore.ts` (`BroadcastFirestoreDoc`).
     */
    BROADCASTS: "broadcasts",

    /** System metadata (e.g. distances_last_calculated) */
    METADATA: "metadata",

    /** Role-permission overrides (doc ID = roleId) */
    PERMISSIONS_CONFIG: "permissions_config",

    /** GPS vehicle locations synced from Cartrack */
    VEHICLE_LOCATIONS: "vehicle_locations",

    /** Holiday calendar (admin-defined) */
    HOLIDAYS: "holidays",

    /** Driver leave requests (sick, annual, etc.) */
    LEAVE_REQUESTS: "leave_requests",

    /** Payroll records (net pay, earnings, deductions) */
    PAYROLL: "payroll",

    /** Security / audit events (append via Cloud Functions only; client read with audit capability) */
    SECURITY_EVENTS: "security_events",
} as const;

/** Type for collection names */
export type CollectionName = typeof COLLECTIONS[keyof typeof COLLECTIONS];
