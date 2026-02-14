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

    /** Truck/vehicle inventory */
    TRUCKS: "trucks",

    /** Subcontractor companies/individuals */
    SUBCONTRACTORS: "subcontractors",

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
} as const;

/** Type for collection names */
export type CollectionName = typeof COLLECTIONS[keyof typeof COLLECTIONS];
