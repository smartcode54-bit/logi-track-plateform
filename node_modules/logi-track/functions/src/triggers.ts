import * as admin from "firebase-admin";
import * as functions from "firebase-functions/v1";

// asia-southeast3 (Firestore region) does not support 1st Gen Cloud Functions.
// FCM for first_mile_tasks is triggered by the web app via callable notifyFirstMileTaskUpdate.


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

/**
 * Trigger: specific logic when a Driver is created in Firestore (Gen2)
 * Creates a corresponding Firebase Auth user.
 * Uses Gen2 to support Firestore in asia-southeast3.
 */
// Region mismatch: Firestore in asia-southeast3, Functions in asia-southeast1.
// Eventarc (Gen 2 triggers) does not support asia-southeast3 for Firestore yet.
// Switching to a Callable Function to handle driver creation safely.

// export const onDriverCreated = onDocumentCreated(
//     {
//         document: "drivers/{driverId}",
//         region: "asia-southeast1",
//     },
//     async (event) => {
//         const snap = event.data;
//         if (!snap) {
//             console.log("[onDriverCreated] No data in event.");
//             return;
//         }
//         const driverId = event.params.driverId;
//         const driverData = snap.data() as any;
//         const { email, mobile, firstName, lastName } = driverData;

//         if (!email || !mobile) {
//             console.log(`[onDriverCreated] Missing email or mobile for driver ${driverId}, skipping Auth creation.`);
//             return;
//         }

//         try {
//             // 1. Check if user exists
//             let userRecord;
//             try {
//                 userRecord = await admin.auth().getUserByEmail(email);
//                 console.log(`[onDriverCreated] User already exists: ${userRecord.uid}`);
//             } catch (error: any) {
//                 if (error.code === "auth/user-not-found") {
//                     // 2. Create new Auth user
//                     // Default password: mobile number (secure enough for initial, user should change)
//                     // OR generate a random one. Using mobile for simplicity as requested often.
//                     const password = mobile.replace(/[^0-9]/g, ""); // stored mobile as password

//                     userRecord = await admin.auth().createUser({
//                         email: email,
//                         password: password,
//                         displayName: `${firstName} ${lastName}`,
//                         phoneNumber: mobile.startsWith('+') ? mobile : undefined, // Auth requires E.164, strict
//                         disabled: false,
//                     });
//                     console.log(`[onDriverCreated] Created new user: ${userRecord.uid}`);
//                 } else {
//                     throw error;
//                 }
//             }

//             // 3. Set Custom Claims (role: driver)
//             await admin.auth().setCustomUserClaims(userRecord.uid, {
//                 role: "driver",
//                 driverId: driverId, 
//             });

//             // 4. Update Firestore with Auth UID
//             await admin.firestore().collection("drivers").doc(driverId).update({
//                 authId: userRecord.uid,
//                 updatedAt: admin.firestore.FieldValue.serverTimestamp(),
//             });

//             console.log(`[onDriverCreated] Successfully linked driver ${driverId} to user ${userRecord.uid}`);

//         } catch (error) {
//             console.error(`[onDriverCreated] Error processing driver ${driverId}:`, error);
//             if (error.code === 'auth/email-already-exists') {
//                 console.log(`[onDriverCreated] User ${email} already exists in Auth.`);
//                 try {
//                     const user = await admin.auth().getUserByEmail(email);
//                     await admin.auth().setCustomUserClaims(user.uid, {
//                         role: 'driver',
//                         driverId: driverId
//                     });
//                     await snap.ref.update({ authUid: user.uid });
//                 } catch (e) {
//                     console.error("Error linking existing user", e);
//                 }
//             }
//         }
//     }
// );

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

/** Payload from web app after creating/updating a first_mile_task */
interface NotifyFirstMileTaskPayload {
    taskId: string;
    oldDriverId?: string;
    newDriverId?: string;
    status?: string;
    sourceHub?: string;
    destination?: string;
    date?: string;
    time?: string;
}

/**
 * Callable: send FCM when a first_mile_task is created/updated (assign, cancel, reassign).
 * Called by the web app after Firestore write; avoids Firestore trigger region mismatch (DB in asia-southeast3, 1st Gen not supported there).
 */
export const notifyFirstMileTaskUpdate = onCall(
    { region: "asia-southeast1" },
    async (request): Promise<{ ok: boolean }> => {
        const data = request.data as NotifyFirstMileTaskPayload;
        const { taskId, oldDriverId, newDriverId, status, sourceHub, destination, date, time } = data;
        if (!taskId) {
            throw new HttpsError("invalid-argument", "taskId is required");
        }
        try {
            if (status === "Cancelled" && newDriverId) {
                await sendFcmToDriver(
                    newDriverId,
                    "Task cancelled",
                    "This first mile task has been cancelled.",
                    { type: "first_mile_task_cancelled", taskId }
                );
                return { ok: true };
            }
            if (oldDriverId && oldDriverId !== newDriverId) {
                await sendFcmToDriver(
                    oldDriverId,
                    "Assignment cancelled",
                    "You have been unassigned from this task.",
                    { type: "first_mile_task_unassigned", taskId }
                );
            }
            if (newDriverId && oldDriverId !== newDriverId) {
                const dateStr = date ?? "";
                const timeStr = time ?? "";
                await sendFcmToDriver(
                    newDriverId,
                    "New task assigned",
                    `${sourceHub ?? ""} → ${destination ?? ""}${dateStr ? ` (${dateStr} ${timeStr})` : ""}`.trim() || "You have a new first mile task.",
                    { type: "first_mile_task_assigned", taskId }
                );
            }
            return { ok: true };
        } catch (err) {
            console.error("[notifyFirstMileTaskUpdate] FCM error:", err);
            throw new HttpsError("internal", (err as Error).message);
        }
    }
);
