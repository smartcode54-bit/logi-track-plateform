import { db, storage } from "@/firebase/client";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { collection, addDoc, serverTimestamp, Timestamp, doc, getDoc, updateDoc } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { Driver } from "@/validate/driverSchema";

export const uploadDriverFile = async (file: File, path: string): Promise<string> => {
    try {
        const storageRef = ref(storage, path);
        const snapshot = await uploadBytes(storageRef, file);
        return await getDownloadURL(snapshot.ref);
    } catch (error) {
        console.error("Error uploading file:", error);
        throw error;
    }
};

const removeUndefined = (obj: any): any => {
    if (obj instanceof Date) return obj;
    if (Array.isArray(obj)) return obj.map(removeUndefined);
    if (typeof obj === 'object' && obj !== null) {
        return Object.fromEntries(
            Object.entries(obj)
                .map(([k, v]) => [k, removeUndefined(v)])
                .filter(([_, v]) => v !== undefined)
        );
    }
    return obj;
};

const toSafeDate = (value: any): Date | undefined => {
    if (!value) return undefined;
    if (value.toDate) return value.toDate(); // Firestore Timestamp
    if (value instanceof Date) {
        return isNaN(value.getTime()) ? undefined : value;
    }
    if (typeof value === 'string') {
        const d = new Date(value);
        return isNaN(d.getTime()) ? undefined : d;
    }
    return undefined;
};

interface DriverFiles {
    profile: File | null;
    idCard: File | null;
    license: File | null;
}

export const createDriver = async (data: Driver, files: DriverFiles) => {
    try {
        // 1. Upload Files
        let profileUrl = undefined;
        let idCardUrl = undefined;
        let licenseUrl = undefined;

        const timestamp = Date.now();

        if (files.profile) {
            profileUrl = await uploadDriverFile(files.profile, `drivers/profile/${timestamp}_${files.profile.name}`);
        }
        if (files.idCard) {
            idCardUrl = await uploadDriverFile(files.idCard, `drivers/documents/${timestamp}_id_card_${files.idCard.name}`);
        }
        if (files.license) {
            licenseUrl = await uploadDriverFile(files.license, `drivers/documents/${timestamp}_license_${files.license.name}`);
        }

        // 2. Prepare Status History
        const initialStatusHistory = [{
            status: data.status,
            changedAt: Timestamp.now(), // Firestore Timestamp
            changedBy: "system", // Ideally createDriver should accept userId, defaulting to system for now
            changedByName: "System",
            reason: "Initial Registration"
        }];

        // 3. Construct Final Data
        const finalData = removeUndefined({
            ...data,
            profileImage: profileUrl,
            idCardImage: idCardUrl,
            truckLicenseImage: licenseUrl,
            statusHistory: initialStatusHistory,
            createdAt: serverTimestamp(),
            updatedAt: serverTimestamp(),
        });

        // 4. Save to Firestore
        await addDoc(collection(db, COLLECTIONS.DRIVERS), finalData);

        return true;
    } catch (error) {
        console.error("Error creating driver:", error);
        throw error;
    }
};

export const getDriverByIdClient = async (id: string): Promise<Driver | null> => {
    try {
        const docRef = doc(db, COLLECTIONS.DRIVERS, id);
        const docSnap = await getDoc(docRef);

        if (docSnap.exists()) {
            const data = docSnap.data();
            return {
                id: docSnap.id,
                ...data,
                // Ensure dates are converted from Firestore Timestamps
                // Ensure dates are converted from Firestore Timestamps or Strings
                birthDate: toSafeDate(data.birthDate),
                idCardExpiredDate: toSafeDate(data.idCardExpiredDate),
                truckLicenseExpiredDate: toSafeDate(data.truckLicenseExpiredDate),
                createdAt: toSafeDate(data.createdAt),
                updatedAt: toSafeDate(data.updatedAt),
                // Ensure statusHistory dates are converted
                statusHistory: Array.isArray(data.statusHistory) ? data.statusHistory.map((h: any) => ({
                    ...h,
                    changedAt: h.changedAt?.toDate ? h.changedAt.toDate() : h.changedAt
                })) : []
            } as Driver;
        } else {
            return null;
        }
    } catch (error) {
        console.error("Error fetching driver:", error);
        return null;
    }
};

export const updateDriver = async (id: string, data: Partial<Driver>, files?: DriverFiles) => {
    try {
        const updates: any = { ...data };
        const timestamp = Date.now();

        // Handle file uploads if new files are provided
        if (files) {
            if (files.profile) {
                updates.profileImage = await uploadDriverFile(files.profile, `drivers/profile/${timestamp}_${files.profile.name}`);
            }
            if (files.idCard) {
                updates.idCardImage = await uploadDriverFile(files.idCard, `drivers/documents/${timestamp}_id_card_${files.idCard.name}`);
            }
            if (files.license) {
                updates.truckLicenseImage = await uploadDriverFile(files.license, `drivers/documents/${timestamp}_license_${files.license.name}`);
            }
        }

        // Add history entry for simple updates (optional, but good for tracking)
        // We might want to append to statusHistory if status changed, but for now let's just update the main fields.
        // If status is changed, the UI should probably handle appending to history before calling this, or we do it here.
        // For simplicity, we'll assume the client passes the *complete* new driver object or specific fields.
        // If we want to track every update, we could add to a separate audit log, but statusHistory is specific to status.

        updates.updatedAt = serverTimestamp();

        const docRef = doc(db, COLLECTIONS.DRIVERS, id);
        await updateDoc(docRef, removeUndefined(updates));

        return true;
    } catch (error) {
        console.error("Error updating driver:", error);
        throw error;
    }
};
