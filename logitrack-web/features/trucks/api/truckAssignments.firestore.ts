/**
 * Firestore source of the truck assignments while the `masterdata` flag is off (developer-spec.md
 * §10.6 "Domain adapter"): the legacy functions of `app/app/truck-assignment/actions.client.ts`
 * (still used by the chat driver sidebar, P5) mapped to the Go DTO. Loaded only through `import()`.
 */
import {
    createAssignment,
    getActiveAssignments,
    getDriverAssignmentHistory,
    getTruckAssignmentHistory,
    terminateAssignment,
    type AssignmentData,
} from "@/app/app/truck-assignment/actions.client";
import { collection, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { AssignInput, TruckAssignmentDTO } from "./truckAssignments";

const iso = (d: Date | undefined): string | null => (d instanceof Date && !Number.isNaN(d.getTime()) ? d.toISOString() : null);

export function assignmentDTO(a: AssignmentData): TruckAssignmentDTO {
    return {
        id: a.id,
        tenantId: null,
        truckId: a.truckId,
        truckPlate: a.truckPlate ?? null,
        truckModel: a.truckModel ?? null,
        driverId: a.driverId,
        driverName: a.driverName ?? null,
        status: a.status,
        adminUserId: null,
        adminLabel: a.adminName ?? null,
        createdAt: iso(a.createdAt),
        revokedAt: iso(a.revokedAt),
    };
}

export async function listActiveAssignmentsFirestore(): Promise<TruckAssignmentDTO[]> {
    return (await getActiveAssignments()).map(assignmentDTO);
}

/** Every assignment, newest first (the legacy page showed 20; the list now pages them all). */
export async function listAssignmentHistoryFirestore(): Promise<TruckAssignmentDTO[]> {
    const snap = await getDocs(collection(db, COLLECTIONS.ASSIGNMENTS));
    const toDate = (v: unknown): Date | undefined =>
        v && typeof (v as { toDate?: () => Date }).toDate === "function" ? (v as { toDate: () => Date }).toDate() : typeof v === "string" ? new Date(v) : undefined;
    return snap.docs
        .map((d) => {
            const data = d.data();
            return assignmentDTO({ ...(data as AssignmentData), id: d.id, createdAt: toDate(data.createdAt) ?? new Date(0), revokedAt: toDate(data.revokedAt) });
        })
        .sort((a, b) => String(b.createdAt).localeCompare(String(a.createdAt)));
}

export async function listAssignmentsOfFirestore(of: { truckId: string } | { driverId: string }): Promise<TruckAssignmentDTO[]> {
    const rows = "truckId" in of ? await getTruckAssignmentHistory(of.truckId) : await getDriverAssignmentHistory(of.driverId);
    return rows.map(assignmentDTO);
}

export async function createAssignmentFirestore(input: AssignInput): Promise<void> {
    await createAssignment(input);
}

export async function revokeAssignmentFirestore(a: Pick<TruckAssignmentDTO, "id" | "truckId" | "driverId">): Promise<void> {
    await terminateAssignment(a.id, a.truckId, a.driverId);
}
