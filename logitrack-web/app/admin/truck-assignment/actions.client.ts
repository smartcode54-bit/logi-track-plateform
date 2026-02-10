import { db } from "@/firebase/client";
import { collection, query, where, getDocs, addDoc, orderBy, limit, Timestamp, runTransaction, doc, serverTimestamp } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { z } from "zod";

// --- Schemas ---

export const AssignmentInputSchema = z.object({
    truckId: z.string().min(1, "Truck ID is required"),
    driverId: z.string().min(1, "Driver ID is required"),
    driverName: z.string().min(1, "Driver Name is required"),
    truckPlate: z.string().min(1, "Truck Plate is required"),
    truckModel: z.string().optional(),
    adminName: z.string().default("System"),
});

export type AssignmentInput = z.infer<typeof AssignmentInputSchema>;

export interface AssignmentData {
    id: string;
    truckId: string;
    driverId: string;
    driverName: string;
    truckPlate: string;
    truckModel: string;
    status: 'active' | 'revoked' | 'cancelled';
    adminName: string;
    createdAt: Date;
    revokedAt?: Date;
}

export interface DriverOption {
    id: string;
    name: string;
    licenseNumber?: string;
    status: 'available' | 'assigned';
    currentAssignment?: any;
}

export interface TruckOption {
    id: string;
    plate: string;
    model: string;
    status: 'available' | 'assigned' | 'maintenance' | 'Active'; // standardizing status
    currentAssignments?: any[];
    ownershipType?: string;
}

// --- Actions ---

export async function createAssignment(rawData: AssignmentInput) {
    // 1. Validate Input
    const validation = AssignmentInputSchema.safeParse(rawData);
    if (!validation.success) {
        throw new Error(`Validation failed: ${validation.error.message}`);
    }
    const data = validation.data;

    try {
        await runTransaction(db, async (transaction) => {
            // 2. Reads (Must come before writes)
            const truckRef = doc(db, COLLECTIONS.TRUCKS, data.truckId);
            const driverRef = doc(db, COLLECTIONS.DRIVERS, data.driverId);

            const truckDoc = await transaction.get(truckRef);
            const driverDoc = await transaction.get(driverRef);

            if (!truckDoc.exists()) throw new Error("Truck not found");
            if (!driverDoc.exists()) throw new Error("Driver not found");

            const truckData = truckDoc.data();
            const driverData = driverDoc.data();

            // 3. Guard Clauses
            // ALLOW multiple drivers per truck. Remove the truck check.
            // if (truckData.currentAssignment) { ... } 

            if (driverData.currentAssignment) {
                throw new Error(`Driver ${data.driverName} is already assigned to ${driverData.currentAssignment.truckPlate}`);
            }

            // Check if THIS driver is already assigned to THIS truck (prevent double assignment of same pair)
            const currentAssignments = truckData.currentAssignments || (truckData.currentAssignment ? [truckData.currentAssignment] : []);
            if (currentAssignments.some((a: any) => a.driverId === data.driverId)) {
                throw new Error(`Driver ${data.driverName} is already assigned to this truck.`);
            }

            // 4. Writes
            const assignmentRef = doc(collection(db, COLLECTIONS.ASSIGNMENTS));
            const assignmentId = assignmentRef.id;
            const now = new Date();
            const serverNow = serverTimestamp();

            // Create Assignment Record
            transaction.set(assignmentRef, {
                ...data,
                id: assignmentId,
                status: 'active',
                createdAt: serverNow,
                updatedAt: serverNow
            });

            // Update Truck - Append to currentAssignments
            const newAssignmentObj = {
                driverId: data.driverId,
                driverName: data.driverName,
                assignedAt: now,
                assignmentId: assignmentId
            };

            transaction.update(truckRef, {
                truckStatus: 'Active',
                currentAssignments: [...currentAssignments, newAssignmentObj],
                currentAssignment: null, // Clear legacy field
                updatedAt: serverNow
            });

            // Update Driver
            transaction.update(driverRef, {
                status: 'On-Duty',
                currentAssignment: {
                    truckId: data.truckId,
                    truckPlate: data.truckPlate,
                    truckModel: data.truckModel || "",
                    assignedAt: now,
                    assignmentId: assignmentId
                },
                updatedAt: serverNow
            });
        });

        return true;
    } catch (error) {
        console.error("Error creating assignment:", error);
        throw error;
    }
}

export async function terminateAssignment(assignmentId: string, truckId: string, driverId: string) {
    try {
        await runTransaction(db, async (transaction) => {
            const assignmentRef = doc(db, COLLECTIONS.ASSIGNMENTS, assignmentId);
            const truckRef = doc(db, COLLECTIONS.TRUCKS, truckId);
            const driverRef = doc(db, COLLECTIONS.DRIVERS, driverId);

            // 1. Reads (Must come before writes)
            const assignmentDoc = await transaction.get(assignmentRef);
            const truckDoc = await transaction.get(truckRef);

            // Verify assignment
            if (!assignmentDoc.exists()) throw new Error("Assignment record not found");
            if (assignmentDoc.data().status !== 'active') throw new Error("Assignment is not active");

            const serverNow = serverTimestamp();

            // 2. Writes

            // Close Assignment with "revoked" status
            transaction.update(assignmentRef, {
                status: 'revoked',
                revokedAt: serverNow,
                updatedAt: serverNow
            });

            // Free Truck - Remove specific assignment
            if (truckDoc.exists()) {
                const truckData = truckDoc.data();
                const currentAssignments = truckData.currentAssignments || (truckData.currentAssignment ? [truckData.currentAssignment] : []);

                // Filter out the specific assignment
                const updatedAssignments = currentAssignments.filter((a: any) => a.assignmentId !== assignmentId);

                const newStatus = updatedAssignments.length === 0 ? 'Available' : 'Active';

                transaction.update(truckRef, {
                    truckStatus: newStatus,
                    currentAssignments: updatedAssignments,
                    currentAssignment: null, // Ensure legacy is clear
                    updatedAt: serverNow
                });
            }

            // Free Driver
            transaction.update(driverRef, {
                status: 'Active', // Return to 'Active' (meaning available), not 'On-Duty'
                currentAssignment: null,
                updatedAt: serverNow
            });
        });
        return true;
    } catch (error) {
        console.error("Error terminating assignment:", error);
        throw error;
    }
}

// --- Queries ---

export async function getActiveAssignments(): Promise<AssignmentData[]> {
    try {
        const q = query(
            collection(db, COLLECTIONS.ASSIGNMENTS),
            where("status", "==", "active")
        );
        const snapshot = await getDocs(q);
        const assignments = snapshot.docs.map(doc => ({
            id: doc.id,
            ...doc.data(),
            createdAt: (doc.data().createdAt as Timestamp).toDate(),
            revokedAt: doc.data().revokedAt ? (doc.data().revokedAt as Timestamp).toDate() : undefined
        })) as AssignmentData[];

        // Client-side sort to avoid index requirement
        return assignments.sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    } catch (error) {
        console.error("Error fetching active assignments:", error);
        return [];
    }
}

export async function getRecentHistory(): Promise<AssignmentData[]> {
    try {
        // Fetch all assignments first (or a larger limit if needed)
        // Note: Without orderBy, limit might return arbitrary docs, but Firestore usually returns in insertion order (effectively createdAt).
        // However, safely we should fetch enough to sort. For now, we fetch all (assuming volume isn't massive yet)
        const q = query(
            collection(db, COLLECTIONS.ASSIGNMENTS)
        );
        const snapshot = await getDocs(q);
        const assignments = snapshot.docs.map(doc => ({
            id: doc.id,
            ...doc.data(),
            createdAt: (doc.data().createdAt as Timestamp).toDate(),
            revokedAt: doc.data().revokedAt ? (doc.data().revokedAt as Timestamp).toDate() : undefined
        })) as AssignmentData[];

        // Client-side sort and limit
        return assignments
            .sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime())
            .slice(0, 20);
    } catch (error) {
        console.error("Error fetching history:", error);
        return [];
    }
}

export async function getTruckAssignmentHistory(truckId: string): Promise<AssignmentData[]> {
    try {
        const q = query(
            collection(db, COLLECTIONS.ASSIGNMENTS),
            where("truckId", "==", truckId)
        );
        const snapshot = await getDocs(q);
        const assignments = snapshot.docs.map(doc => ({
            id: doc.id,
            ...doc.data(),
            createdAt: (doc.data().createdAt as Timestamp).toDate(),
            revokedAt: doc.data().revokedAt ? (doc.data().revokedAt as Timestamp).toDate() : undefined
        })) as AssignmentData[];

        return assignments.sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    } catch (error) {
        console.error(`Error fetching assignment history for truck ${truckId}:`, error);
        return [];
    }
}


export async function getDriverAssignmentHistory(driverId: string): Promise<AssignmentData[]> {
    try {
        const q = query(
            collection(db, COLLECTIONS.ASSIGNMENTS),
            where("driverId", "==", driverId)
        );
        const snapshot = await getDocs(q);

        const assignments = snapshot.docs.map(doc => ({
            id: doc.id,
            ...doc.data(),
            createdAt: (doc.data().createdAt as Timestamp).toDate(),
            revokedAt: doc.data().revokedAt ? (doc.data().revokedAt as Timestamp).toDate() : undefined
        })) as AssignmentData[];

        // Sort client-side to avoid composite index requirement
        return assignments.sort((a, b) => b.createdAt.getTime() - a.createdAt.getTime());
    } catch (error) {
        console.error(`Error fetching assignment history for driver ${driverId}:`, error);
        return [];
    }
}

export async function getAvailableDrivers(): Promise<DriverOption[]> {
    try {
        // Fetch all drivers
        // Optimization: Create a compound index for status/currentAssignment if list grows huge
        // For now: Fetch all drivers (role check if using User collection, but we have DRIVERS collection)
        // Adjust: This was originally querying USERS collection with role='driver'.
        // BUT we have a separate DRIVERS collection as per the 'drivers' feature.
        // Let's use the DRIVERS collection which is the source of truth for 'Driver' entity.

        // Remove server-side filter to get all drivers, then filter client-side for "Active" or "Available"
        const q = query(collection(db, COLLECTIONS.DRIVERS));

        const snapshot = await getDocs(q);

        // Client-side filter for extra safety as complex queries might need index
        return snapshot.docs
            .map(doc => {
                const data = doc.data();
                return {
                    id: doc.id,
                    name: `${data.firstName} ${data.lastName}`,
                    licenseNumber: data.truckLicenseId,
                    status: data.status,
                    currentAssignment: data.currentAssignment
                } as unknown as DriverOption;
            })
            // Filter: Must not have current assignment AND must be some form of "Active" or "Available"
            .filter(d => {
                // Check if assignment object exists and is not empty
                const hasAssignment = !!d.currentAssignment && Object.keys(d.currentAssignment).length > 0;

                // Allow "Active", "Available", "On-Duty" (if for some reason they are consistent with no assignment, though unlikely)
                // We want to exclude "Inactive" or "Terminated"
                const status = d.status ? d.status.toLowerCase() : "";
                const isStatusOk = ['active', 'available', 'on-duty'].includes(status);

                return !hasAssignment && isStatusOk;
            });

    } catch (error) {
        console.error("Error fetching drivers:", error);
        return [];
    }
}

export async function getAvailableTrucks(): Promise<TruckOption[]> {
    try {
        // Query trucks where truckStatus is NOT 'Active' (On-Duty) or 'Maintenance'
        // If we want "Available" specifically:
        const q = query(collection(db, COLLECTIONS.TRUCKS));
        // We might want to filter by status, but "truckStatus" is a free text string in the schema interface above?
        // Let's fetch all and filter client side for safety for now to avoid missing "Available" vs "ready" etc variations
        // Or if we strictly enforced "Available" in the transaction, we can query it.

        const snapshot = await getDocs(q);

        return snapshot.docs
            .map(doc => {
                const data = doc.data();
                return {
                    id: doc.id,
                    plate: data.licensePlate,
                    model: data.model,
                    status: data.truckStatus,
                    ownershipType: data.ownershipType || 'own',
                    currentAssignments: data.currentAssignments || (data.currentAssignment ? [data.currentAssignment] : [])
                } as unknown as TruckOption;
            })
            .filter(t => t.status !== 'maintenance' && t.ownershipType === 'own'); // Only Own Fleet trucks

    } catch (error) {
        console.error("Error fetching trucks:", error);
        return [];
    }
}
