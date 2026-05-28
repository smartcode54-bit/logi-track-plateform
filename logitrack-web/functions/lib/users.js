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
exports.syncExistingUsers = exports.linkDriverToUser = exports.setUserDisabled = exports.createUser = exports.updateUserRole = exports.getUsers = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const securityEvents_1 = require("./securityEvents");
/**
 * Cloud Function to get all users (Admin only)
 */
exports.getUsers = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can view all users");
    }
    try {
        const listUsersResult = await admin.auth().listUsers(1000);
        // Map existing users
        const users = listUsersResult.users.map((userRecord) => ({
            uid: userRecord.uid,
            email: userRecord.email,
            displayName: userRecord.displayName,
            photoURL: userRecord.photoURL,
            customClaims: userRecord.customClaims,
            metadata: userRecord.metadata,
            providerData: userRecord.providerData.map((p) => p.providerId),
            disabled: userRecord.disabled,
        }));
        return { users };
    }
    catch (error) {
        console.error(`[getUsers] Error listing users:`, error);
        throw new https_1.HttpsError("internal", `Failed to list users: ${error.message || error}`);
    }
});
/**
 * Cloud Function to update user role (Admin only)
 */
exports.updateUserRole = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can modify user roles");
    }
    const { targetUid, role, isAdmin, partnerScopeId, customerScopeId } = request.data;
    if (!targetUid) {
        throw new https_1.HttpsError("invalid-argument", "Target UID is required");
    }
    try {
        const user = await admin.auth().getUser(targetUid);
        const currentClaims = (user.customClaims || {});
        let newRole = role;
        let newIsAdmin = false;
        if (role) {
            newRole = role;
            newIsAdmin = role === 'admin';
        }
        else if (typeof isAdmin === 'boolean') {
            newRole = isAdmin ? 'admin' : 'user';
            newIsAdmin = isAdmin;
        }
        const nextClaims = { ...currentClaims };
        nextClaims.role = newRole;
        nextClaims.admin = newIsAdmin;
        if (newRole === 'partner') {
            if (typeof partnerScopeId === 'string' && partnerScopeId.trim() !== '') {
                nextClaims.partnerScopeId = partnerScopeId.trim();
            }
            else if (typeof currentClaims.partnerScopeId === 'string' && String(currentClaims.partnerScopeId).trim() !== '') {
                nextClaims.partnerScopeId = currentClaims.partnerScopeId;
            }
            else {
                delete nextClaims.partnerScopeId;
            }
        }
        else {
            delete nextClaims.partnerScopeId;
        }
        if (newRole === 'customer') {
            if (typeof customerScopeId === 'string' && customerScopeId.trim() !== '') {
                nextClaims.customerScopeId = customerScopeId.trim();
            }
            else if (typeof currentClaims.customerScopeId === 'string' && String(currentClaims.customerScopeId).trim() !== '') {
                nextClaims.customerScopeId = currentClaims.customerScopeId;
            }
            else {
                delete nextClaims.customerScopeId;
            }
        }
        else {
            delete nextClaims.customerScopeId;
        }
        console.log(`[updateUserRole] Setting custom claims for ${targetUid}:`, JSON.stringify(nextClaims));
        try {
            await admin.auth().setCustomUserClaims(targetUid, nextClaims);
            console.log(`[updateUserRole] Custom claims set successfully for ${targetUid}`);
            // Force user to re-login by revoking refresh tokens
            // This ensures the new role takes effect immediately on next login
            await admin.auth().revokeRefreshTokens(targetUid);
            console.log(`[updateUserRole] Revoked refresh tokens for ${targetUid} - user must re-login`);
        }
        catch (authError) {
            console.error(`[updateUserRole] FAILED to set custom claims for ${targetUid}:`, {
                message: authError?.message,
                code: authError?.code,
                fullError: String(authError)
            });
            throw new https_1.HttpsError("internal", `Failed to set custom claims: ${authError?.message || authError}`);
        }
        // Sync to Firestore
        try {
            const userDoc = {
                role: newRole,
                // Set forceLogoutAt so the client's Firestore listener logs the user out immediately
                forceLogoutAt: admin.firestore.FieldValue.serverTimestamp(),
            };
            if (newRole === 'partner' && typeof nextClaims.partnerScopeId === 'string' && String(nextClaims.partnerScopeId).trim() !== '') {
                userDoc.partnerScopeId = nextClaims.partnerScopeId;
            }
            else if (newRole !== 'partner') {
                userDoc.partnerScopeId = admin.firestore.FieldValue.delete();
            }
            if (newRole === 'customer' && typeof nextClaims.customerScopeId === 'string' && String(nextClaims.customerScopeId).trim() !== '') {
                userDoc.customerScopeId = nextClaims.customerScopeId;
            }
            else if (newRole !== 'customer') {
                userDoc.customerScopeId = admin.firestore.FieldValue.delete();
            }
            await admin.firestore().collection("users").doc(targetUid).set(userDoc, { merge: true });
        }
        catch (dbError) {
            console.error(`[updateUserRole] Failed to sync role to Firestore:`, dbError);
        }
        try {
            const actorEmail = request.auth.token.email;
            await (0, securityEvents_1.appendSecurityEvent)({
                type: "user_role_changed",
                severity: "info",
                summary: `User role updated to ${newRole}`,
                details: {
                    targetUid,
                    newRole,
                    previousRole: typeof currentClaims.role === "string" ? currentClaims.role : null,
                },
                actorUid: request.auth.uid,
                actorEmail: typeof actorEmail === "string" ? actorEmail : null,
            });
        }
        catch (logErr) {
            console.error("[updateUserRole] security_events append:", logErr);
        }
        return {
            success: true,
            message: `User role updated successfully to ${newRole}`,
        };
    }
    catch (error) {
        console.error(`[updateUserRole] Error updating user role:`, error);
        throw new https_1.HttpsError("internal", "Failed to update user role");
    }
});
/**
 * Cloud Function to create a new user (Admin only)
 */
exports.createUser = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can create users");
    }
    const { email, password, displayName, role, partnerScopeId, customerScopeId, driverDocId } = request.data;
    if (!email || !password || !displayName) {
        throw new https_1.HttpsError("invalid-argument", "Email, password, and display name are required");
    }
    try {
        let userRecord;
        let isExisting = false;
        try {
            userRecord = await admin.auth().createUser({
                email,
                password,
                displayName,
            });
        }
        catch (createErr) {
            if (createErr.code === "auth/email-already-exists" && typeof driverDocId === "string" && driverDocId.trim()) {
                userRecord = await admin.auth().getUserByEmail(email);
                isExisting = true;
            }
            else {
                throw createErr;
            }
        }
        const userRole = role || 'user';
        const isAdmin = userRole === 'admin';
        const claims = {
            role: userRole,
            admin: isAdmin,
        };
        if (userRole === 'partner' && typeof partnerScopeId === 'string' && partnerScopeId.trim() !== '') {
            claims.partnerScopeId = partnerScopeId.trim();
        }
        if (userRole === 'customer' && typeof customerScopeId === 'string' && customerScopeId.trim() !== '') {
            claims.customerScopeId = customerScopeId.trim();
        }
        // When creating a driver account linked to an existing driver doc, embed driverId in claims
        // so the mobile app can resolve assigned tasks without an extra Firestore lookup.
        if (userRole === 'driver' && typeof driverDocId === 'string' && driverDocId.trim() !== '') {
            claims.driverId = driverDocId.trim();
        }
        await admin.auth().setCustomUserClaims(userRecord.uid, claims);
        try {
            const userDoc = { role: userRole };
            if (userRole === 'partner' && typeof claims.partnerScopeId === 'string') {
                userDoc.partnerScopeId = claims.partnerScopeId;
            }
            if (userRole === 'customer' && typeof claims.customerScopeId === 'string') {
                userDoc.customerScopeId = claims.customerScopeId;
            }
            await admin.firestore().collection("users").doc(userRecord.uid).set(userDoc, { merge: true });
        }
        catch (dbErr) {
            console.error("[createUser] Firestore sync:", dbErr);
        }
        // Link to driver document if provided — writes authId so the mobile app can find tasks
        if (typeof driverDocId === "string" && driverDocId.trim()) {
            try {
                await admin.firestore().collection("drivers").doc(driverDocId.trim()).update({
                    authId: userRecord.uid,
                    updatedAt: admin.firestore.FieldValue.serverTimestamp(),
                });
                console.log(`[createUser] Linked driver doc ${driverDocId} → uid ${userRecord.uid}`);
            }
            catch (linkErr) {
                console.error("[createUser] Failed to link driver doc:", linkErr);
            }
        }
        try {
            const actorEmail = request.auth.token.email;
            await (0, securityEvents_1.appendSecurityEvent)({
                type: "user_created",
                severity: "info",
                summary: `User created: ${email}`,
                details: {
                    targetUid: userRecord.uid,
                    email,
                    role: userRole,
                },
                actorUid: request.auth.uid,
                actorEmail: typeof actorEmail === "string" ? actorEmail : null,
            });
        }
        catch (logErr) {
            console.error("[createUser] security_events append:", logErr);
        }
        return {
            success: true,
            uid: userRecord.uid,
            message: isExisting ? `Existing account linked to driver` : `User created successfully`,
        };
    }
    catch (error) {
        console.error(`[createUser] Error creating user:`, error);
        throw new https_1.HttpsError("internal", `Failed to create user: ${error.message || error}`);
    }
});
/**
 * Cloud Function to enable/disable a user (Admin only).
 * Disabling: blocks future sign-ins, revokes refresh tokens, and sets forceLogoutAt
 * so the client kicks the user out of any active session immediately.
 */
exports.setUserDisabled = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can disable users");
    }
    const { targetUid, disabled } = request.data;
    if (!targetUid) {
        throw new https_1.HttpsError("invalid-argument", "Target UID is required");
    }
    if (typeof disabled !== "boolean") {
        throw new https_1.HttpsError("invalid-argument", "disabled must be a boolean");
    }
    if (targetUid === request.auth.uid) {
        throw new https_1.HttpsError("failed-precondition", "Cannot disable your own account");
    }
    try {
        await admin.auth().updateUser(targetUid, { disabled });
        console.log(`[setUserDisabled] ${targetUid} disabled=${disabled}`);
        if (disabled) {
            // Force the user out of any active session immediately
            await admin.auth().revokeRefreshTokens(targetUid);
        }
        // Sync to Firestore. forceLogoutAt makes the client-side Firestore listener
        // sign the user out instantly (works for both disable and re-enable triggering).
        try {
            const userDoc = {
                disabled,
            };
            if (disabled) {
                userDoc.forceLogoutAt = admin.firestore.FieldValue.serverTimestamp();
            }
            await admin.firestore().collection("users").doc(targetUid).set(userDoc, { merge: true });
        }
        catch (dbError) {
            console.error(`[setUserDisabled] Failed to sync to Firestore:`, dbError);
        }
        try {
            const actorEmail = request.auth.token.email;
            await (0, securityEvents_1.appendSecurityEvent)({
                type: disabled ? "user_disabled" : "user_enabled",
                severity: "info",
                summary: disabled ? `User disabled` : `User enabled`,
                details: { targetUid, disabled },
                actorUid: request.auth.uid,
                actorEmail: typeof actorEmail === "string" ? actorEmail : null,
            });
        }
        catch (logErr) {
            console.error("[setUserDisabled] security_events append:", logErr);
        }
        return { success: true, disabled };
    }
    catch (error) {
        console.error(`[setUserDisabled] Error:`, error);
        throw new https_1.HttpsError("internal", `Failed to update disabled state: ${error?.message || error}`);
    }
});
/**
 * Callable: Link (or unlink) a Firebase Auth user to a drivers/{driverDocId} document
 * by writing authId into the driver doc. This lets the mobile app find assigned tasks.
 * If driverDocId is empty, the existing link is cleared.
 */
exports.linkDriverToUser = (0, https_1.onCall)(async (request) => {
    if (!request.auth || request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const { targetUid, driverDocId } = request.data;
    if (!targetUid)
        throw new https_1.HttpsError("invalid-argument", "targetUid is required");
    const firestore = admin.firestore();
    // Remove authId from any driver doc currently linked to this UID
    try {
        const existing = await firestore.collection("drivers")
            .where("authId", "==", targetUid)
            .limit(1)
            .get();
        if (!existing.empty) {
            await existing.docs[0].ref.update({
                authId: admin.firestore.FieldValue.delete(),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
        }
    }
    catch (err) {
        console.error("[linkDriverToUser] Failed to clear old link:", err);
    }
    const did = typeof driverDocId === "string" ? driverDocId.trim() : "";
    if (did) {
        await firestore.collection("drivers").doc(did).update({
            authId: targetUid,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        console.log(`[linkDriverToUser] Linked driver doc ${did} → uid ${targetUid}`);
    }
    // Sync custom claims: the maintenance Firestore rule reads request.auth.token.driverId,
    // so linking must also write the driverId claim (not just authId on the doc). Revoke
    // tokens so the new claim takes effect on the driver's next login.
    try {
        const userRecord = await admin.auth().getUser(targetUid);
        const nextClaims = { ...(userRecord.customClaims || {}) };
        if (did) {
            nextClaims.role = "driver";
            nextClaims.admin = false;
            nextClaims.driverId = did;
        }
        else {
            delete nextClaims.driverId;
        }
        await admin.auth().setCustomUserClaims(targetUid, nextClaims);
        await admin.auth().revokeRefreshTokens(targetUid);
        console.log(`[linkDriverToUser] Claims synced for ${targetUid}:`, JSON.stringify(nextClaims));
    }
    catch (claimErr) {
        console.error("[linkDriverToUser] Failed to sync claims:", claimErr);
        throw new https_1.HttpsError("internal", "Linked driver doc but failed to set auth claims");
    }
    return { success: true };
});
/**
 * Callable: Utility to sync existing users to Firestore (Admin only)
 */
exports.syncExistingUsers = (0, https_1.onCall)(async (request) => {
    if (request.auth?.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    try {
        const listUsersResult = await admin.auth().listUsers(1000);
        const batch = admin.firestore().batch();
        const usersRef = admin.firestore().collection("users");
        let count = 0;
        for (const user of listUsersResult.users) {
            const role = user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user");
            const userDoc = usersRef.doc(user.uid);
            batch.set(userDoc, {
                uid: user.uid,
                email: user.email || "",
                displayName: user.displayName || "",
                photoURL: user.photoURL || "",
                role: role,
                authCreationTime: user.metadata.creationTime || null,
                lastLogin: user.metadata.lastSignInTime || null,
                providerData: user.providerData.map((p) => p.providerId),
            }, { merge: true });
            count++;
        }
        await batch.commit();
        return { success: true, message: `Synced ${count} users` };
    }
    catch (error) {
        throw new https_1.HttpsError("internal", error.message);
    }
});
//# sourceMappingURL=users.js.map