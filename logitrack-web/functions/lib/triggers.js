"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.checkMaintenanceAlert = exports.getNextTaskId = exports.notifyMaintenanceReminder = exports.notifyTaskUpdate = exports.updateDriverAccount = exports.createDriverAccount = exports.onUserDeleted = exports.onUserCreated = void 0;
const admin = __importStar(require("firebase-admin"));
const functions = __importStar(require("firebase-functions/v1"));
const maintenance_1 = require("./core/maintenance");
// asia-southeast3 (Firestore region) does not support 1st Gen Cloud Functions.
// FCM for tasks is triggered by the app via callable notifyTaskUpdate.
/**
 * Trigger: Sync new Auth user to Firestore
 */
exports.onUserCreated = functions.region("asia-southeast1").auth.user().onCreate(async (user) => {
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
    }
    catch (error) {
        console.error(`[onUserCreated] Error syncing user:`, error);
    }
});
/**
 * Trigger: Delete user from Firestore when Auth user is deleted
 */
exports.onUserDeleted = functions.region("asia-southeast1").auth.user().onDelete(async (user) => {
    console.log(`[onUserDeleted] Deleting user from Firestore: ${user.email} (${user.uid})`);
    try {
        await admin.firestore().collection("users").doc(user.uid).delete();
        console.log(`[onUserDeleted] Deleted user ${user.uid}`);
    }
    catch (error) {
        console.error(`[onUserDeleted] Error deleting user:`, error);
    }
});
const https_1 = require("firebase-functions/v2/https");
exports.createDriverAccount = (0, https_1.onCall)({
    region: "asia-southeast1",
}, async (request) => {
    const data = request.data;
    const { email, mobile, firstName, lastName, loginPassword, skipAuthCreation } = data;
    if (!mobile || !firstName || !lastName) {
        throw new https_1.HttpsError("invalid-argument", "Missing required fields: mobile, firstName, lastName");
    }
    try {
        let authUid;
        // Create Auth user only when email is provided and creation is not explicitly skipped
        if (email && !skipAuthCreation) {
            let userRecord;
            const rawPassword = loginPassword && String(loginPassword).trim().length >= 6
                ? String(loginPassword).trim()
                : mobile.replace(/[^0-9]/g, "");
            const password = rawPassword.length >= 6 ? rawPassword : "password123";
            try {
                userRecord = await admin.auth().getUserByEmail(email);
                console.log(`[createDriverAccount] User already exists: ${userRecord.uid}`);
            }
            catch (error) {
                if (error.code === "auth/user-not-found") {
                    try {
                        userRecord = await admin.auth().createUser({
                            email,
                            password,
                            displayName: `${firstName} ${lastName}`,
                            disabled: false,
                        });
                        console.log(`[createDriverAccount] Created new Auth user: ${userRecord.uid}`);
                    }
                    catch (createError) {
                        console.error("Error creating auth user:", createError);
                        throw new https_1.HttpsError("aborted", "Failed to create Auth user: " + createError.message);
                    }
                }
                else {
                    throw new https_1.HttpsError("internal", "Error checking for existing user: " + error.message);
                }
            }
            authUid = userRecord.uid;
        }
        const initialStatusHistory = [{
                status: data.status || "Active",
                changedAt: admin.firestore.Timestamp.now(),
                changedBy: "system",
                changedByName: "System",
                reason: "Initial Registration"
            }];
        // Strip CF-only params so they don't land in Firestore
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { loginPassword: _lp, skipAuthCreation: _skip, ...driverFields } = data;
        const driverData = {
            ...driverFields,
            statusHistory: initialStatusHistory,
            createdAt: admin.firestore.FieldValue.serverTimestamp(),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        };
        if (authUid) {
            driverData.authId = authUid;
        }
        const docRef = await admin.firestore().collection("drivers").add(driverData);
        const driverId = docRef.id;
        if (authUid) {
            await admin.auth().setCustomUserClaims(authUid, {
                role: "driver",
                driverId,
            });
        }
        return { success: true, driverId, ...(authUid ? { authId: authUid } : {}) };
    }
    catch (error) {
        console.error("[createDriverAccount] Error:", error);
        throw new https_1.HttpsError("internal", error.message);
    }
});
exports.updateDriverAccount = (0, https_1.onCall)({
    region: "asia-southeast1",
}, async (request) => {
    // Ensure the user calling this is an admin
    // if (!request.auth || request.auth.token.role !== 'admin') {
    //     throw new HttpsError('permission-denied', 'Only admins can update drivers.');
    // }
    const data = request.data;
    const { id, updates } = data;
    if (!id || !updates) {
        throw new https_1.HttpsError("invalid-argument", "Missing required fields: id, updates");
    }
    try {
        // 1. Fetch current driver to get authId
        const driverRef = admin.firestore().collection("drivers").doc(id);
        const driverSnap = await driverRef.get();
        if (!driverSnap.exists) {
            throw new https_1.HttpsError("not-found", "Driver not found in Firestore");
        }
        const driverData = driverSnap.data();
        const authId = driverData.authId || driverData.authUid;
        const wantEmail = typeof updates.email === 'string' ? updates.email.trim() : '';
        // 2a. Driver already has an Auth account → sync email/password changes
        if (authId) {
            const authUpdates = {};
            if (wantEmail) {
                try {
                    // Always verify against actual Auth record to fix any unsynced states
                    const userRecord = await admin.auth().getUser(authId);
                    if (userRecord.email !== wantEmail) {
                        authUpdates.email = wantEmail;
                    }
                }
                catch (error) {
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
                }
                catch (authError) {
                    console.error("[updateDriverAccount] Error updating Auth user:", authError);
                    // Abort the whole update rather than let drivers.email drift from the Auth
                    // record — the driver logs in with the Auth email, so a divergence locks
                    // them out silently.
                    if (authError.code === "auth/email-already-exists") {
                        throw new https_1.HttpsError("already-exists", `Email ${wantEmail} is already used by another account. No changes were saved.`);
                    }
                    throw new https_1.HttpsError("aborted", "Failed to update Auth user credentials: " + authError.message);
                }
            }
        }
        // 2b. Driver has NO Auth account yet but an email is being set → provision/link
        // one, write authId back to the doc, and set claims (role:driver + driverId).
        // Without this, adding an email later only stored a plain field — the driver
        // could never log in and the maintenance rule (needs driverId claim) failed.
        else if (wantEmail) {
            const mobile = String(updates.mobile ?? driverData.mobile ?? '');
            const rawPassword = typeof updates.password === 'string' && updates.password.trim().length >= 6
                ? updates.password.trim()
                : mobile.replace(/[^0-9]/g, "");
            const password = rawPassword.length >= 6 ? rawPassword : "password123";
            let userRecord;
            try {
                userRecord = await admin.auth().getUserByEmail(wantEmail);
                console.log(`[updateDriverAccount] Linking existing Auth user ${userRecord.uid} to driver ${id}`);
            }
            catch (error) {
                if (error.code === "auth/user-not-found") {
                    const displayName = `${updates.firstName ?? driverData.firstName ?? ''} ${updates.lastName ?? driverData.lastName ?? ''}`.trim();
                    userRecord = await admin.auth().createUser({
                        email: wantEmail,
                        password,
                        displayName: displayName || undefined,
                        disabled: false,
                    });
                    console.log(`[updateDriverAccount] Created Auth user ${userRecord.uid} for driver ${id}`);
                }
                else {
                    throw new https_1.HttpsError("internal", "Error checking for existing user: " + error.message);
                }
            }
            updates.authId = userRecord.uid;
            await admin.auth().setCustomUserClaims(userRecord.uid, {
                role: "driver",
                driverId: id,
            });
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
    }
    catch (error) {
        console.error("[updateDriverAccount] Error:", error);
        if (error instanceof https_1.HttpsError)
            throw error;
        throw new https_1.HttpsError("internal", error.message);
    }
});
async function sendFcmToDriver(driverId, title, body, data) {
    const driverSnap = await admin.firestore().collection("drivers").doc(driverId).get();
    const fcmToken = driverSnap.data()?.fcmToken;
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
/**
 * Callable: send FCM when a task is created/updated (assign, cancel, reassign).
 * Avoids Firestore trigger region mismatch (DB in asia-southeast3, 1st Gen not supported there).
 */
exports.notifyTaskUpdate = (0, https_1.onCall)({ region: "asia-southeast1" }, async (request) => {
    const data = request.data;
    const { taskId, taskType, oldDriverId, newDriverId, status, sourceHub, destination, date, time } = data;
    if (!taskId) {
        throw new https_1.HttpsError("invalid-argument", "taskId is required");
    }
    const typePrefix = taskType === "LINE_HAUL" ? "line_haul" : "first_mile";
    const taskLabel = taskType === "LINE_HAUL" ? "Line Haul" : "First Mile";
    try {
        if (status === "Cancelled" && newDriverId) {
            await sendFcmToDriver(newDriverId, "Task cancelled", `This ${taskLabel.toLowerCase()} task has been cancelled.`, { type: `${typePrefix}_task_cancelled`, taskId });
            return { ok: true };
        }
        if (oldDriverId && oldDriverId !== newDriverId) {
            await sendFcmToDriver(oldDriverId, "Assignment cancelled", "You have been unassigned from this task.", { type: `${typePrefix}_task_unassigned`, taskId });
        }
        if (newDriverId && oldDriverId !== newDriverId) {
            const dateStr = date ?? "";
            const timeStr = time ?? "";
            await sendFcmToDriver(newDriverId, "New task assigned", `${sourceHub ?? ""} → ${destination ?? ""}${dateStr ? ` (${dateStr} ${timeStr})` : ""}`.trim() || `You have a new ${taskLabel.toLowerCase()} task.`, { type: `${typePrefix}_task_assigned`, taskId });
        }
        return { ok: true };
    }
    catch (err) {
        console.error("[notifyTaskUpdate] FCM error:", err);
        throw new https_1.HttpsError("internal", err.message);
    }
});
/**
 * Callable: notify assigned driver when PM booking moves to in-shop (PM Booking → in_progress).
 */
exports.notifyMaintenanceReminder = (0, https_1.onCall)({ region: "asia-southeast1" }, async (request) => {
    if (!request.auth?.uid) {
        throw new https_1.HttpsError("unauthenticated", "Authentication required");
    }
    const payload = request.data;
    const { driverId, maintenanceId, title, body } = payload;
    if (!driverId) {
        throw new https_1.HttpsError("invalid-argument", "driverId is required");
    }
    try {
        await sendFcmToDriver(driverId, title || "นัดเช็คระยะ", body || "มีงานเช็คระยะ — กรุณาเข้าอู่ตามนัด (ดูรายละเอียดในแอป)", { type: "maintenance_scheduled", maintenanceId: maintenanceId || "" });
        return { ok: true };
    }
    catch (err) {
        console.error("[notifyMaintenanceReminder] Error:", err);
        throw new https_1.HttpsError("internal", err.message || "FCM failed");
    }
});
/**
 * Callable: Get the next sequential shipment ID for a given date and task type.
 * Format: FM-[Date]-[NUM] or LH-[Date]-[NUM]
 */
exports.getNextTaskId = (0, https_1.onCall)({ region: "asia-southeast1" }, async (request) => {
    const data = request.data;
    const { date, taskType } = data;
    if (!date || !taskType) {
        throw new https_1.HttpsError("invalid-argument", "date and taskType are required");
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
    }
    catch (err) {
        console.error("[getNextTaskId] Error:", err);
        throw new https_1.HttpsError("internal", err.message);
    }
});
/**
 * Callable: Check if truck ODO is within PM threshold or overdue; create PM Booking. Triggered after fuel log approval.
 */
exports.checkMaintenanceAlert = (0, https_1.onCall)({ region: "asia-southeast1" }, async (request) => {
    const data = request.data;
    const { truckId } = data;
    const mileage = Number(data.mileage);
    if (!truckId || !Number.isFinite(mileage)) {
        throw new https_1.HttpsError("invalid-argument", "truckId and valid mileage are required");
    }
    try {
        const logExit = (r) => {
            console.log("[checkMaintenanceAlert] result", JSON.stringify({ truckId, mileage, ...r }));
            return r;
        };
        const truckRef = admin.firestore().collection("trucks").doc(truckId);
        let truckSnap = await truckRef.get();
        if (!truckSnap.exists)
            return logExit({ success: false, message: "Truck not found" });
        let truckData = truckSnap.data();
        const nextServiceMileageRaw = truckData.nextServiceMileage;
        const nextServiceMileage = Number(nextServiceMileageRaw);
        if (!Number.isFinite(nextServiceMileage) || nextServiceMileage <= 0) {
            return logExit({ success: false, message: "No nextServiceMileage defined for this truck" });
        }
        if (truckData.activeMaintenanceId) {
            const mid = truckData.activeMaintenanceId;
            const mSnap = await admin.firestore().collection("maintenance").doc(mid).get();
            const st = mSnap.exists ? String(mSnap.data()?.status ?? "") : "";
            const terminal = !mSnap.exists || st === "completed" || st === "cancelled";
            if (!terminal) {
                return logExit({ success: true, message: "Truck already has an active maintenance task", created: false });
            }
            await truckRef.update({
                activeMaintenanceId: admin.firestore.FieldValue.delete(),
                lastAlertMileage: admin.firestore.FieldValue.delete(),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
            truckSnap = await truckRef.get();
            truckData = truckSnap.data();
        }
        const lastAlertMileage = Number(truckData.lastAlertMileage) || 0;
        if (mileage === lastAlertMileage)
            return logExit({ success: true, message: "Already alert processed for this mileage" });
        if ((0, maintenance_1.isMaintenanceDue)(mileage, nextServiceMileage)) {
            const existingOpenSnap = await admin.firestore().collection("maintenance")
                .where("truckId", "==", truckId)
                .where("status", "in", ["PM Booking", "Scheduled"])
                .get();
            if (!existingOpenSnap.empty) {
                return logExit({ success: true, message: "Open PM or scheduled maintenance exists for this truck", created: false });
            }
            const distanceLeft = nextServiceMileage - mileage;
            const notes = distanceLeft < 0
                ? `ระบบอัตโนมัติ: เลยกำหนดเช็คระยะ (${mileage.toLocaleString()} กม. / รอบ ${nextServiceMileage.toLocaleString()} กม.)`
                : `ระบบอัตโนมัติ: จากรายการเติมน้ำมัน (${mileage.toLocaleString()} / ${nextServiceMileage.toLocaleString()} กม. เหลือ ${distanceLeft.toLocaleString()} กม. ≤ ${maintenance_1.PM_ALERT_THRESHOLD_KM.toLocaleString()} กม.)`;
            const maintenanceData = {
                truckId,
                truckLicensePlate: truckData.licensePlate || "",
                truckBrand: truckData.brand || "",
                status: "PM Booking",
                type: "PM",
                serviceType: "เช็คระยะตามรอบ",
                startDate: new Date().toISOString().slice(0, 10),
                currentMileage: mileage,
                nextServiceMileage: nextServiceMileage,
                notes,
                createdAt: admin.firestore.FieldValue.serverTimestamp(),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            };
            const maintenanceRef = await admin.firestore().collection("maintenance").add(maintenanceData);
            await truckRef.update({
                lastAlertMileage: mileage,
                activeMaintenanceId: maintenanceRef.id,
                currentMileage: mileage,
                updatedAt: admin.firestore.FieldValue.serverTimestamp()
            });
            return logExit({ success: true, message: "Created PM Booking task successfully", created: true });
        }
        await truckRef.update({
            currentMileage: mileage,
            updatedAt: admin.firestore.FieldValue.serverTimestamp()
        });
        return logExit({ success: true, message: "Within safe threshold range. Mileage updated.", created: false });
    }
    catch (err) {
        console.error("[checkMaintenanceAlert] Error:", err);
        throw new https_1.HttpsError("internal", err.message);
    }
});
//# sourceMappingURL=triggers.js.map