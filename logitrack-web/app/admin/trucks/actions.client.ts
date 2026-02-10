import { db } from "@/firebase/client";
import { collection, doc, getDoc, getDocs, query, orderBy, addDoc, serverTimestamp } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";

export interface TruckData {
    id: string;
    // Ownership
    ownershipType: "own" | "subcontractor";
    subcontractorId?: string;

    licensePlate: string;
    province: string;
    vin: string;
    engineNumber: string;
    truckStatus: string;
    brand: string;
    model: string;
    year: string;
    color: string;
    type: string;
    seats?: string;
    fuelType: string;
    engineCapacity?: number;
    fuelCapacity?: number;
    maxLoadWeight?: number;
    registrationDate: string;
    buyingDate: string;
    notes?: string;

    // Assignment - Denormalized
    // Assignment - Denormalized
    currentAssignments?: {
        driverId: string;
        driverName: string;
        assignedAt: Date;
        assignmentId: string;
    }[];
    // Deprecated single assignment for backward compatibility if needed, but we migrate fully.
    currentAssignment?: never; // Explicitly ensure we don't use it.

    // Images
    imageFrontRight?: string;
    imageFrontLeft?: string;
    imageBackRight?: string;
    imageBackLeft?: string;
    // Documents
    documentTax?: string;
    documentRegister?: string;

    // Legacy images array (keep for backward compatibility if needed, or remove)
    images?: string[];

    // Insurance
    insurancePolicyId?: string;
    insurancePolicyNumber?: string;
    insuranceCompany?: string;
    insuranceType?: string;
    insuranceStartDate?: string;
    insuranceExpiryDate?: string;
    insurancePremium?: number;
    insuranceDocuments?: string[];
    insuranceNotes?: string;

    // Compliance Fields
    taxExpiryDate?: string;
    lastServiceDate?: string;
    nextServiceDate?: string;
    nextServiceMileage?: number;
    currentMileage?: number;

    // Responsibility
    taxResponsible?: string;
    maintenanceResponsible?: string;

    // Renewal Fields
    taxRenewalStatus?: "pending" | "in_progress" | "completed";
    insuranceRenewalStatus?: "pending" | "in_progress" | "completed";
    taxExpense?: number;
    taxReceipt?: string;
    paymentMethod?: string;
    insuranceReceipt?: string;

    statusHistory?: {
        status: string;
        date: string;
        changedBy: string;
        notes?: string;
    }[];

    createdBy: string;
    createdAt: Date | null;
    updatedAt: Date | null;
}

// Convert Firestore Timestamp to Date or keep as is
const formatTimestamp = (timestamp: any): Date | null => {
    if (!timestamp) return null;
    if (timestamp.toDate) {
        return timestamp.toDate();
    }
    if (timestamp.toMillis) {
        return new Date(timestamp.toMillis());
    }
    if (timestamp.seconds) {
        return new Date(timestamp.seconds * 1000);
    }
    return timestamp;
};

export async function logTransaction(data: any) {
    try {
        await addDoc(collection(db, COLLECTIONS.TRANSACTIONS), {
            ...data,
            createdAt: serverTimestamp(),
        });
    } catch (error) {
        console.error("Error logging transaction:", error);
        throw error;
    }
}

export async function getTrucksClient(): Promise<TruckData[]> {
    try {
        // Get trucks from Firestore using client SDK
        const trucksRef = collection(db, COLLECTIONS.TRUCKS);
        const q = query(trucksRef, orderBy("createdAt", "desc"));
        const snapshot = await getDocs(q);

        const trucks: TruckData[] = [];

        snapshot.forEach((doc) => {
            const data = doc.data();

            trucks.push({
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

                // Assignment - Denormalized
                // Assignment - Denormalized
                currentAssignments: data.currentAssignments ? (data.currentAssignments as any[]).map(assignment => ({
                    driverId: assignment.driverId,
                    driverName: assignment.driverName,
                    assignedAt: formatTimestamp(assignment.assignedAt) as Date,
                    assignmentId: assignment.assignmentId
                })) : (data.currentAssignment ? [{
                    // Fallback for legacy single assignment data during migration
                    driverId: data.currentAssignment.driverId,
                    driverName: data.currentAssignment.driverName,
                    assignedAt: formatTimestamp(data.currentAssignment.assignedAt) as Date,
                    assignmentId: data.currentAssignment.assignmentId
                }] : []),

                // New Fields
                imageFrontRight: data.imageFrontRight || "",
                imageFrontLeft: data.imageFrontLeft || "",
                imageBackRight: data.imageBackRight || "",
                imageBackLeft: data.imageBackLeft || "",
                documentTax: data.documentTax || "",
                documentRegister: data.documentRegister || "",

                images: data.images || [],

                // Insurance
                insurancePolicyId: data.insurancePolicyId || "",
                insurancePolicyNumber: data.insurancePolicyNumber || "",
                insuranceCompany: data.insuranceCompany || "",
                insuranceType: data.insuranceType || "",
                insuranceStartDate: data.insuranceStartDate || "",
                insuranceExpiryDate: data.insuranceExpiryDate || "",
                insurancePremium: data.insurancePremium,
                insuranceDocuments: data.insuranceDocuments || [],
                insuranceNotes: data.insuranceNotes || "",

                // Compliance Fields
                taxExpiryDate: data.taxExpiryDate || "",
                lastServiceDate: data.lastServiceDate || "",
                nextServiceDate: data.nextServiceDate || "",
                nextServiceMileage: data.nextServiceMileage,
                currentMileage: data.currentMileage,

                // Renewal Fields
                taxRenewalStatus: data.taxRenewalStatus,
                insuranceRenewalStatus: data.insuranceRenewalStatus,
                taxExpense: data.taxExpense,
                taxReceipt: data.taxReceipt,
                paymentMethod: data.paymentMethod || "",
                insuranceReceipt: data.insuranceReceipt,
                statusHistory: data.statusHistory || [],

                createdBy: data.createdBy || "",
                createdAt: formatTimestamp(data.createdAt),
                updatedAt: formatTimestamp(data.updatedAt),
            });
        });

        return trucks;
    } catch (error) {
        console.error("Error fetching trucks:", error);
        throw error;
    }
}

export async function getTruckByIdClient(id: string): Promise<TruckData | null> {
    try {
        // Get truck from Firestore using client SDK
        const truckRef = doc(db, COLLECTIONS.TRUCKS, id);
        const docSnap = await getDoc(truckRef);

        if (!docSnap.exists()) {
            return null;
        }

        const data = docSnap.data();

        return {
            id: docSnap.id,
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

            // Assignment - Denormalized
            // Assignment - Denormalized
            currentAssignments: data.currentAssignments ? (data.currentAssignments as any[]).map(assignment => ({
                driverId: assignment.driverId,
                driverName: assignment.driverName,
                assignedAt: formatTimestamp(assignment.assignedAt) as Date,
                assignmentId: assignment.assignmentId
            })) : (data.currentAssignment ? [{
                // Fallback for legacy single assignment data
                driverId: data.currentAssignment.driverId,
                driverName: data.currentAssignment.driverName,
                assignedAt: formatTimestamp(data.currentAssignment.assignedAt) as Date,
                assignmentId: data.currentAssignment.assignmentId
            }] : []),

            // New Fields
            imageFrontRight: data.imageFrontRight || "",
            imageFrontLeft: data.imageFrontLeft || "",
            imageBackRight: data.imageBackRight || "",
            imageBackLeft: data.imageBackLeft || "",
            documentTax: data.documentTax || "",
            documentRegister: data.documentRegister || "",

            images: data.images || [],

            // Insurance
            insurancePolicyId: data.insurancePolicyId || "",
            insurancePolicyNumber: data.insurancePolicyNumber || "",
            insuranceCompany: data.insuranceCompany || "",
            insuranceType: data.insuranceType || "",
            insuranceStartDate: data.insuranceStartDate || "",
            insuranceExpiryDate: data.insuranceExpiryDate || "",
            insurancePremium: data.insurancePremium,
            insuranceDocuments: data.insuranceDocuments || [],
            insuranceNotes: data.insuranceNotes || "",

            // Compliance Fields
            taxExpiryDate: data.taxExpiryDate || "",
            lastServiceDate: data.lastServiceDate || "",
            nextServiceDate: data.nextServiceDate || "",
            nextServiceMileage: data.nextServiceMileage,
            currentMileage: data.currentMileage,

            // Responsibility
            taxResponsible: data.taxResponsible || "Operation Admin",
            maintenanceResponsible: data.maintenanceResponsible || "Driver",

            // Renewal Fields
            taxRenewalStatus: data.taxRenewalStatus,
            insuranceRenewalStatus: data.insuranceRenewalStatus,
            taxExpense: data.taxExpense,
            taxReceipt: data.taxReceipt,
            paymentMethod: data.paymentMethod || "",
            insuranceReceipt: data.insuranceReceipt,
            statusHistory: data.statusHistory || [],

            createdBy: data.createdBy || "",
            createdAt: formatTimestamp(data.createdAt),
            updatedAt: formatTimestamp(data.updatedAt),
        };
    } catch (error) {
        console.error("Error fetching truck:", error);
        throw error;
    }
}
