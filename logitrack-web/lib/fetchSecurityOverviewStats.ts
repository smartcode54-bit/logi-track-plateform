import {
    collection,
    collectionGroup,
    getCountFromServer,
    query,
    where,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

export type UserRoleCounts = {
    admins: number;
    managers: number;
    operationStaff: number;
    operators: number;
    drivers: number;
    subcontractors: number;
    customers: number;
};

/** Requires Firestore rules: `users` readable by admin token only for cross-user queries. */
export async function fetchUserRoleCounts(): Promise<UserRoleCounts> {
    const [
        adminSnap,
        managerSnap,
        opStaffSnap,
        operatorSnap,
        driversSnap,
        partnerSnap,
        customerSnap,
    ] = await Promise.all([
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "admin"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "manager"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "operation_staff"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "operator"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "driver"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "partner"))),
        getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "customer"))),
    ]);
    return {
        admins: adminSnap.data().count,
        managers: managerSnap.data().count,
        operationStaff: opStaffSnap.data().count,
        operators: operatorSnap.data().count,
        drivers: driversSnap.data().count,
        subcontractors: partnerSnap.data().count,
        customers: customerSnap.data().count,
    };
}

export type MobileInstallCountParams = {
    isAdmin: boolean;
    role: string;
    partnerScopeId: string;
};

/**
 * Count `mobile_installations` docs visible to the caller (admin: all; partner: scoped).
 * Returns null when the caller cannot read installs (no scope / wrong role).
 */
export async function fetchMobileInstallationCount(params: MobileInstallCountParams): Promise<number | null> {
    if (params.isAdmin) {
        const snap = await getCountFromServer(query(collectionGroup(db, "mobile_installations")));
        return snap.data().count;
    }
    if (params.role === "partner" && params.partnerScopeId) {
        const snap = await getCountFromServer(
            query(collectionGroup(db, "mobile_installations"), where("partnerId", "==", params.partnerScopeId)),
        );
        return snap.data().count;
    }
    return null;
}
