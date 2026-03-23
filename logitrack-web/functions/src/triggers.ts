import * as admin from "firebase-admin";
import * as functions from "firebase-functions/v1";
import { isMaintenanceDue } from "./core/maintenance";

// asia-southeast3 (Firestore region) does not support 1st Gen Cloud Functions.
// FCM for tasks is triggered by the app via callable notifyTaskUpdate.


/**
 * Trigger: Sync new Auth user to Firestore
 */
export const onUserCreated = functions.region("asia-southeast1").auth.user().onCreate(async (user) => {
    console.log(`[onUserCreated] Syncing new user: ${user.email} (${user.uid})`);
    try {
        const { uid, email, displayName, photoURL, metadata } = user;
        await admin.firestore().collection("users").doc(uid).set({
            uid,
            email: email || "",
            displayName: displayName || "",
            photoURL: photoURL || "",
            role: "user",
            createdAt: admin.firestore.FieldValue.serverTimestamp(),
            authCreationTime: metadata.creationTime || null,
            lastLogin: metadata.lastSignInTime || null,
        }, { merge: true });
        console.log(`[onUserCreated] Synced user ${uid}`);
    } catch (error) {
        console.error(`[onUserCreated] Error syncing user:`, error);
    }
});

/**
 * Trigger: Delete user from Firestore when Auth user is deleted
 */
export const onUserDeleted = functions.region("asia-southeast1").auth.user().onDelete(async (user) => {
    console.log(`[onUserDeleted] Deleting user from Firestore: ${user.email} (${user.uid})`);
    try {
        await admin.firestore().collection("users").doc(user.uid).delete();
        console.log(`[onUserDeleted] Deleted user ${user.uid}`);
    } catch (error) {
        console.error(`[onUserDeleted] Error deleting user:`, error);
    }
});


import { onCall, HttpsError } from "firebase-functions/v2/https";

export const createDriverAccount = onCall(
    {
        region: "asia-southeast1",
    },
    async (request) => {
        // Ensure the user calling this is an admin (optional but recommended)
        // if (!request.auth || request.auth.token.role !== 'admin') {
        //     throw new HttpsError('permission-denied', 'Only admins can create drivers.');
        // }

        const data = request.data;
        const { email, mobile, firstName, lastName } = data;

        if (!email || !mobile || !firstName || !lastName) {
            throw new HttpsError("invalid-argument", "Missing required fields: email, mobile, firstName, lastName");
        }

        try {
            // 1. Create Firestore Document first (to get an ID, or use auto-id)
            // or we can create Auth first. 
            // Let's create Auth first to ensure we can.

            let userRecord;
            let password = mobile.replace(/[^0-9]/g, ""); // Default password
            if (password.length < 6) password = "password123"; // Fallback

            try {
                userRecord = await admin.auth().getUserByEmail(email);
                console.log(`[createDriverAccount] User already exists: ${userRecord.uid}`);
                // If user exists, we might want to fail OR just link. 
                // Creating a duplicate driver for an existing auth user might be weird.
                // For now, let's allow it but warn.
            } catch (error: any) {
                if (error.code === "auth/user-not-found") {
                    try {
                        userRecord = await admin.auth().createUser({
                            email: email,
                            password: password,
                            displayName: `${firstName} ${lastName}`,
                            // phoneNumber: mobile, // Optional, can cause issues if format is wrong
                            disabled: false,
                        });
                        console.log(`[createDriverAccount] Created new Auth user: ${userRecord.uid}`);
                    } catch (createError: any) {
                        console.error("Error creating auth user:", createError);
                        throw new HttpsError("aborted", "Failed to create Auth user: " + createError.message);
                    }
                } else {
                    throw new HttpsError("internal", "Error checking for existing user: " + error.message);
                }
            }

            // 2. Create Driver Document in Firestore
            // We use the data passed from client.
            // Client should pass the full driver object ready for Firestore (except timestamps).

            const initialStatusHistory = [{
                status: data.status || "Active",
                changedAt: admin.firestore.Timestamp.now(),
                changedBy: "system",
                changedByName: "System",
                reason: "Initial Registration"
            }];

            const driverData = {
                ...data,
                statusHistory: initialStatusHistory,
                authId: userRecord.uid,
                createdAt: admin.firestore.FieldValue.serverTimestamp(),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            };

            // Clean up undefined values if any (Firestore doesn't like them)
            // But 'data' from onCall is JSON, so no undefineds usually, just nulls or missing.

            const docRef = await admin.firestore().collection("drivers").add(driverData);
            const driverId = docRef.id;

            // 3. Set Custom Claims
            await admin.auth().setCustomUserClaims(userRecord.uid, {
                role: "driver",
                driverId: driverId,
            });

            return { success: true, driverId: driverId, authId: userRecord.uid };

        } catch (error: any) {
            console.error("[createDriverAccount] Error:", error);
            throw new HttpsError("internal", error.message);
        }
    }
);

export const updateDriverAccount = onCall(
    {
        region: "asia-southeast1",
    },
    async (request) => {
        // Ensure the user calling this is an admin
        // if (!request.auth || request.auth.token.role !== 'admin') {
        //     throw new HttpsError('permission-denied', 'Only admins can update drivers.');
        // }

        const data = request.data;
        const { id, updates } = data;

        if (!id || !updates) {
            throw new HttpsError("invalid-argument", "Missing required fields: id, updates");
        }

        try {
            // 1. Fetch current driver to get authId
            const driverRef = admin.firestore().collection("drivers").doc(id);
            const driverSnap = await driverRef.get();

            if (!driverSnap.exists) {
                throw new HttpsError("not-found", "Driver not found in Firestore");
            }

            const driverData = driverSnap.data() as any;
            const authId = driverData.authId || driverData.authUid;

            // 2. If Auth updating is needed (email change or password logic)
            if (authId) {
                const authUpdates: any = {};

                if (typeof updates.email === 'string' && updates.email.trim() !== '') {
                    const newEmail = updates.email.trim();
                    try {
                        // Always verify against actual Auth record to fix any unsynced states
                        const userRecord = await admin.auth().getUser(authId);
                        if (userRecord.email !== newEmail) {
                            authUpdates.email = newEmail;
                        }
                    } catch (error) {
                        console.error("[updateDriverAccount] Error fetching Auth user:", error);
                    }
                }

                if (typeof updates.password === 'string' && updates.password.trim() !== '') {
                    authUpdates.password = updates.password.trim();
                }

                if (Object.keys(authUpdates).length > 0) {
                    try {
                        console.log(`[updateDriverAccount] Syncing Auth user ${authId} with payload`, authUpdates);
                        await admin.auth().updateUser(authId, authUpdates);
                    } catch (authError: any) {
                        console.error("[updateDriverAccount] Error updating Auth user:", authError);
                        throw new HttpsError("aborted", "Failed to update Auth user credentials: " + authError.message);
                    }
                }
            }

            // Always delete password from updates before saving to firestore
            if ('password' in updates) {
                delete updates.password;
            }

            // 3. Update Firestore Document
            // Append an update to history if status is changing
            if (updates.status && updates.status !== driverData.status) {
                const newHistoryEntry = {
                    status: updates.status,
                    changedAt: admin.firestore.Timestamp.now(),
                    changedBy: request.auth?.uid || "system",
                    changedByName: request.auth?.token.email || "System",
                    reason: "Status update via web portal"
                };

                updates.statusHistory = admin.firestore.FieldValue.arrayUnion(newHistoryEntry);
            }

            const finalUpdates = {
                ...updates,
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            };

            await driverRef.update(finalUpdates);

            return { success: true, driverId: id };

        } catch (error: any) {
            console.error("[updateDriverAccount] Error:", error);
            if (error instanceof HttpsError) throw error;
            throw new HttpsError("internal", error.message);
        }
    }
);

async function sendFcmToDriver(driverId: string, title: string, body: string, data: Record<string, string>) {
    const driverSnap = await admin.firestore().collection("drivers").doc(driverId).get();
    const fcmToken = driverSnap.data()?.fcmToken as string | undefined;
    if (!fcmToken) {
        console.log(`[FCM] No fcmToken for driver ${driverId}, skip`);
        return;
    }
    await admin.messaging().send({
        token: fcmToken,
        notification: { title, body },
        data: { ...data, driverId },
        android: { priority: "high", notification: { channelId: "task_assignments" } },
        apns: { payload: { aps: { sound: "default" } }, fcmOptions: {} },
    });
}

/** Payload from app after creating/updating a task */
interface NotifyTaskPayload {
    taskId: string;
    taskType: "FIRST_MILE" | "LINE_HAUL";
    oldDriverId?: string;
    newDriverId?: string;
    status?: string;
    sourceHub?: string;
    destination?: string;
    date?: string;
    time?: string;
}

/**
 * Callable: send FCM when a task is created/updated (assign, cancel, reassign).
 * Avoids Firestore trigger region mismatch (DB in asia-southeast3, 1st Gen not supported there).
 */
export const notifyTaskUpdate = onCall(
    { region: "asia-southeast1" },
    async (request): Promise<{ ok: boolean }> => {
        const data = request.data as NotifyTaskPayload;
        const { taskId, taskType, oldDriverId, newDriverId, status, sourceHub, destination, date, time } = data;
        if (!taskId) {
            throw new HttpsError("invalid-argument", "taskId is required");
        }
        const typePrefix = taskType === "LINE_HAUL" ? "line_haul" : "first_mile";
        const taskLabel = taskType === "LINE_HAUL" ? "Line Haul" : "First Mile";

        try {
            if (status === "Cancelled" && newDriverId) {
                await sendFcmToDriver(
                    newDriverId,
                    "Task cancelled",
                    `This ${taskLabel.toLowerCase()} task has been cancelled.`,
                    { type: `${typePrefix}_task_cancelled`, taskId }
                );
                return { ok: true };
            }
            if (oldDriverId && oldDriverId !== newDriverId) {
                await sendFcmToDriver(
                    oldDriverId,
                    "Assignment cancelled",
                    "You have been unassigned from this task.",
                    { type: `${typePrefix}_task_unassigned`, taskId }
                );
            }
            if (newDriverId && oldDriverId !== newDriverId) {
                const dateStr = date ?? "";
                const timeStr = time ?? "";
                await sendFcmToDriver(
                    newDriverId,
                    "New task assigned",
                    `${sourceHub ?? ""} → ${destination ?? ""}${dateStr ? ` (${dateStr} ${timeStr})` : ""}`.trim() || `You have a new ${taskLabel.toLowerCase()} task.`,
                    { type: `${typePrefix}_task_assigned`, taskId }
                );
            }
            return { ok: true };
        } catch (err) {
            console.error("[notifyTaskUpdate] FCM error:", err);
            throw new HttpsError("internal", (err as Error).message);
        }
    }
);

/**
 * Callable: Get the next sequential shipment ID for a given date and task type.
 * Format: FM-[Date]-[NUM] or LH-[Date]-[NUM]
 */
export const getNextTaskId = onCall(
    { region: "asia-southeast1" },
    async (request): Promise<{ taskId: string }> => {
        const data = request.data as { date: string; taskType: "FIRST_MILE" | "LINE_HAUL" };
        const { date, taskType } = data;

        if (!date || !taskType) {
            throw new HttpsError("invalid-argument", "date and taskType are required");
        }

        try {
            const prefix = taskType === "LINE_HAUL" ? "LH" : "FM";
            // date format expected: YYYY-MM-DD or DDMMYYYY etc. 
            // In Next.js we used ddmmyy. Let's stick to what the user requested or what's consistent.
            // The user requested: LH-[Date]-[NUM]

            const q = admin.firestore().collection("tasks")
                .where("taskType", "==", taskType)
                .where("dateStr", "==", date);

            const countSnap = await q.count().get();
            const nextNum = countSnap.data().count + 1;
            const taskId = `${prefix}-${date}-${nextNum}`;

            return { taskId };
        } catch (err) {
            console.error("[getNextTaskId] Error:", err);
            throw new HttpsError("internal", (err as Error).message);
        }
    }
);

/**
 * Callable: Check if truck ODO is near maintenance gap (<= 2000 km) and create PM Booking task suggestion.
 * Triggered manually from client after saving Fuel Log records.
 */
export const checkMaintenanceAlert = onCall(
    { region: "asia-southeast1" },
    async (request): Promise<{ success: boolean; message: string; created?: boolean }> => {
        const data = request.data as { truckId: string; mileage: number };
        const { truckId, mileage } = data;

        if (!truckId || mileage === undefined) {
            throw new HttpsError("invalid-argument", "truckId and mileage are required");
        }

        try {
            // 1. Get Truck document
            const truckRef = admin.firestore().collection("trucks").doc(truckId);
            const truckSnap = await truckRef.get();
            if (!truckSnap.exists) return { success: false, message: "Truck not found" };

            const truckData = truckSnap.data() as any;
            const nextServiceMileage = truckData.nextServiceMileage;
            if (!nextServiceMileage) return { success: false, message: "No nextServiceMileage defined for this truck" };

            // Lock Check: Skip if already in active maintenance loop
            if (truckData.activeMaintenanceId) {
                return { success: true, message: "Truck already has an active maintenance task", created: false };
            }

            const lastAlertMileage = truckData.lastAlertMileage || 0;
            if (mileage === lastAlertMileage) return { success: true, message: "Already alert processed for this mileage" };

            // 2. Check margin condition (Within 2,000 KM remaining) Pure Logic
            if (isMaintenanceDue(mileage, nextServiceMileage)) {
                // 3. Create Maintenance record { status: "PM Booking" }
                // Check if already created recently to avoid duplication (e.g. check for same truck pending maintenance)
                const existingTaskSnap = await admin.firestore().collection("maintenance")
                    .where("truckId", "==", truckId)
                    .where("status", "==", "PM Booking")
                    .get();
                if (!existingTaskSnap.empty) {
                    return { success: true, message: "PM Booking already exists for this truck", created: false };
                }

                const maintenanceData = {
                    truckId,
                    truckLicensePlate: truckData.licensePlate || "", // 🚗 เพิ่มเพื่อให้แสดงฟอนต์ Dashboard ได้ถูกต้อง
                    truckBrand: truckData.brand || "",
                    status: "PM Booking",
                    type: "PM",
                    serviceType: "เช็คระยะตามรอบ", // Thai translation
                    currentMileage: mileage,
                    nextServiceMileage: nextServiceMileage,
                    notes: `ระบบอัตโนมัติ: ดักตรวจกิโลเมตรจากรายการเติมน้ำมัน (${mileage.toLocaleString()} / ${nextServiceMileage.toLocaleString()} กม.)`,
                    createdAt: admin.firestore.FieldValue.serverTimestamp(),
                    updatedAt: admin.firestore.FieldValue.serverTimestamp(),
                };

                const maintenanceRef = await admin.firestore().collection("maintenance").add(maintenanceData);

                // Update truck to remember alert
                await truckRef.update({
                    lastAlertMileage: mileage,
                    activeMaintenanceId: maintenanceRef.id, // 🔒 Lock: Attach active item ID to truck
                    currentMileage: mileage, // Update current truck mileage with ground truth fuel log
                    updatedAt: admin.firestore.FieldValue.serverTimestamp()
                });

                return { success: true, message: "Created PM Booking task successfully", created: true };
            }

            // Just update truck mileage if not triggers alert
            await truckRef.update({
                currentMileage: mileage,
                updatedAt: admin.firestore.FieldValue.serverTimestamp()
            });

            return { success: true, message: "Within safe threshold range. Mileage updated.", created: false };
        } catch (err: any) {
            console.error("[checkMaintenanceAlert] Error:", err);
            throw new HttpsError("internal", err.message);
        }
    }
);
