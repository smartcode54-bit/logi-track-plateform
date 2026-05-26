import { db, storage, functions } from "@/firebase/client";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { collection, doc, getDoc, updateDoc } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
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

export interface LoginAccountConfig {
    createAccount: boolean;
    password?: string;
}

export const createDriver = async (
    data: Driver,
    files: DriverFiles,
    loginAccount?: LoginAccountConfig,
): Promise<{ driverId?: string; authId?: string }> => {
    try {
        const timestamp = Date.now();
        const profileUrl = files.profile
            ? await uploadDriverFile(files.profile, `drivers/profile/${timestamp}_${files.profile.name}`)
            : undefined;
        const idCardUrl = files.idCard
            ? await uploadDriverFile(files.idCard, `drivers/documents/${timestamp}_id_card_${files.idCard.name}`)
            : undefined;
        const licenseUrl = files.license
            ? await uploadDriverFile(files.license, `drivers/documents/${timestamp}_license_${files.license.name}`)
            : undefined;

        const finalData = removeUndefined({
            ...data,
            profileImage: profileUrl,
            idCardImage: idCardUrl,
            truckLicenseImage: licenseUrl,
        });

        const callData: Record<string, unknown> = { ...finalData };
        if (!loginAccount?.createAccount) {
            callData.skipAuthCreation = true;
        } else if (loginAccount.password) {
            callData.loginPassword = loginAccount.password;
        }

        const createDriverAccountFn = httpsCallable(functions, 'createDriverAccount');
        const result = await createDriverAccountFn(callData);
        return (result.data ?? {}) as { driverId?: string; authId?: string };
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
                birthDate: toSafeDate(data.birthDate),
                idCardExpiredDate: toSafeDate(data.idCardExpiredDate),
                truckLicenseExpiredDate: toSafeDate(data.truckLicenseExpiredDate),
                createdAt: toSafeDate(data.createdAt),
                updatedAt: toSafeDate(data.updatedAt),
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

        const updateDriverAccount = httpsCallable(functions, 'updateDriverAccount');
        await updateDriverAccount({
            id: id,
            updates: removeUndefined(updates)
        });

        return true;
    } catch (error) {
        console.error("Error updating driver:", error);
        throw error;
    }
};
