import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { appendSecurityEvent } from "./securityEvents";

/**
 * Cloud Function to get all users (Admin only)
 */
export const getUsers = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can view all users");
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
    } catch (error: any) {
        console.error(`[getUsers] Error listing users:`, error);
        throw new HttpsError("internal", `Failed to list users: ${error.message || error}`);
    }
});

/**
 * Cloud Function to update user role (Admin only)
 */
export const updateUserRole = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can modify user roles");
    }

    const { targetUid, role, isAdmin, partnerScopeId, customerScopeId } = request.data as {
        targetUid?: string;
        role?: string;
        isAdmin?: boolean;
        partnerScopeId?: string;
        customerScopeId?: string;
    };

    if (!targetUid) {
        throw new HttpsError("invalid-argument", "Target UID is required");
    }

    try {
        const user = await admin.auth().getUser(targetUid);
        const currentClaims = (user.customClaims || {}) as Record<string, unknown>;

        let newRole = role;
        let newIsAdmin = false;

        if (role) {
            newRole = role;
            newIsAdmin = role === 'admin';
        } else if (typeof isAdmin === 'boolean') {
            newRole = isAdmin ? 'admin' : 'user';
            newIsAdmin = isAdmin;
        }

        const nextClaims: Record<string, unknown> = { ...currentClaims };
        nextClaims.role = newRole;
        nextClaims.admin = newIsAdmin;

        if (newRole === 'partner') {
            if (typeof partnerScopeId === 'string' && partnerScopeId.trim() !== '') {
                nextClaims.partnerScopeId = partnerScopeId.trim();
            } else if (typeof currentClaims.partnerScopeId === 'string' && String(currentClaims.partnerScopeId).trim() !== '') {
                nextClaims.partnerScopeId = currentClaims.partnerScopeId;
            } else {
                delete nextClaims.partnerScopeId;
            }
        } else {
            delete nextClaims.partnerScopeId;
        }

        if (newRole === 'customer') {
            if (typeof customerScopeId === 'string' && customerScopeId.trim() !== '') {
                nextClaims.customerScopeId = customerScopeId.trim();
            } else if (typeof currentClaims.customerScopeId === 'string' && String(currentClaims.customerScopeId).trim() !== '') {
                nextClaims.customerScopeId = currentClaims.customerScopeId;
            } else {
                delete nextClaims.customerScopeId;
            }
        } else {
            delete nextClaims.customerScopeId;
        }

        console.log(`[updateUserRole] Setting custom claims for ${targetUid}:`, JSON.stringify(nextClaims));
        try {
            await admin.auth().setCustomUserClaims(targetUid, nextClaims as { [key: string]: unknown });
            console.log(`[updateUserRole] Custom claims set successfully for ${targetUid}`);

            // Force user to re-login by revoking refresh tokens
            // This ensures the new role takes effect immediately on next login
            await admin.auth().revokeRefreshTokens(targetUid);
            console.log(`[updateUserRole] Revoked refresh tokens for ${targetUid} - user must re-login`);
        } catch (authError: any) {
            console.error(`[updateUserRole] FAILED to set custom claims for ${targetUid}:`, {
                message: authError?.message,
                code: authError?.code,
                fullError: String(authError)
            });
            throw new HttpsError("internal", `Failed to set custom claims: ${authError?.message || authError}`);
        }

        // Sync to Firestore
        try {
            const userDoc: Record<string, unknown> = {
                role: newRole,
                // Set forceLogoutAt so the client's Firestore listener logs the user out immediately
                forceLogoutAt: admin.firestore.FieldValue.serverTimestamp(),
            };
            if (newRole === 'partner' && typeof nextClaims.partnerScopeId === 'string' && String(nextClaims.partnerScopeId).trim() !== '') {
                userDoc.partnerScopeId = nextClaims.partnerScopeId;
            } else if (newRole !== 'partner') {
                userDoc.partnerScopeId = admin.firestore.FieldValue.delete();
            }
            if (newRole === 'customer' && typeof nextClaims.customerScopeId === 'string' && String(nextClaims.customerScopeId).trim() !== '') {
                userDoc.customerScopeId = nextClaims.customerScopeId;
            } else if (newRole !== 'customer') {
                userDoc.customerScopeId = admin.firestore.FieldValue.delete();
            }
            await admin.firestore().collection("users").doc(targetUid).set(userDoc, { merge: true });
        } catch (dbError) {
            console.error(`[updateUserRole] Failed to sync role to Firestore:`, dbError);
        }

        try {
            const actorEmail = request.auth.token.email;
            await appendSecurityEvent({
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
        } catch (logErr) {
            console.error("[updateUserRole] security_events append:", logErr);
        }

        return {
            success: true,
            message: `User role updated successfully to ${newRole}`,
        };
    } catch (error) {
        console.error(`[updateUserRole] Error updating user role:`, error);
        throw new HttpsError("internal", "Failed to update user role");
    }
});

/**
 * Cloud Function to create a new user (Admin only)
 */
export const createUser = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can create users");
    }

    const { email, password, displayName, role, partnerScopeId, customerScopeId, driverDocId } = request.data as {
        email?: string;
        password?: string;
        displayName?: string;
        role?: string;
        partnerScopeId?: string;
        customerScopeId?: string;
        driverDocId?: string;
    };

    if (!email || !password || !displayName) {
        throw new HttpsError("invalid-argument", "Email, password, and display name are required");
    }

    try {
        const userRecord = await admin.auth().createUser({
            email,
            password,
            displayName,
        });

        const userRole = role || 'user';
        const isAdmin = userRole === 'admin';

        const claims: Record<string, unknown> = {
            role: userRole,
            admin: isAdmin,
        };
        if (userRole === 'partner' && typeof partnerScopeId === 'string' && partnerScopeId.trim() !== '') {
            claims.partnerScopeId = partnerScopeId.trim();
        }
        if (userRole === 'customer' && typeof customerScopeId === 'string' && customerScopeId.trim() !== '') {
            claims.customerScopeId = customerScopeId.trim();
        }

        await admin.auth().setCustomUserClaims(userRecord.uid, claims as { [key: string]: unknown });

        try {
            const userDoc: Record<string, unknown> = { role: userRole };
            if (userRole === 'partner' && typeof claims.partnerScopeId === 'string') {
                userDoc.partnerScopeId = claims.partnerScopeId;
            }
            if (userRole === 'customer' && typeof claims.customerScopeId === 'string') {
                userDoc.customerScopeId = claims.customerScopeId;
            }
            await admin.firestore().collection("users").doc(userRecord.uid).set(userDoc, { merge: true });
        } catch (dbErr) {
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
            } catch (linkErr) {
                console.error("[createUser] Failed to link driver doc:", linkErr);
            }
        }

        try {
            const actorEmail = request.auth.token.email;
            await appendSecurityEvent({
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
        } catch (logErr) {
            console.error("[createUser] security_events append:", logErr);
        }

        return {
            success: true,
            uid: userRecord.uid,
            message: `User created successfully`,
        };
    } catch (error: any) {
        console.error(`[createUser] Error creating user:`, error);
        throw new HttpsError("internal", `Failed to create user: ${error.message || error}`);
    }
});

/**
 * Cloud Function to enable/disable a user (Admin only).
 * Disabling: blocks future sign-ins, revokes refresh tokens, and sets forceLogoutAt
 * so the client kicks the user out of any active session immediately.
 */
export const setUserDisabled = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can disable users");
    }

    const { targetUid, disabled } = request.data as {
        targetUid?: string;
        disabled?: boolean;
    };

    if (!targetUid) {
        throw new HttpsError("invalid-argument", "Target UID is required");
    }
    if (typeof disabled !== "boolean") {
        throw new HttpsError("invalid-argument", "disabled must be a boolean");
    }
    if (targetUid === request.auth.uid) {
        throw new HttpsError("failed-precondition", "Cannot disable your own account");
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
            const userDoc: Record<string, unknown> = {
                disabled,
            };
            if (disabled) {
                userDoc.forceLogoutAt = admin.firestore.FieldValue.serverTimestamp();
            }
            await admin.firestore().collection("users").doc(targetUid).set(userDoc, { merge: true });
        } catch (dbError) {
            console.error(`[setUserDisabled] Failed to sync to Firestore:`, dbError);
        }

        try {
            const actorEmail = request.auth.token.email;
            await appendSecurityEvent({
                type: disabled ? "user_disabled" : "user_enabled",
                severity: "info",
                summary: disabled ? `User disabled` : `User enabled`,
                details: { targetUid, disabled },
                actorUid: request.auth.uid,
                actorEmail: typeof actorEmail === "string" ? actorEmail : null,
            });
        } catch (logErr) {
            console.error("[setUserDisabled] security_events append:", logErr);
        }

        return { success: true, disabled };
    } catch (error: any) {
        console.error(`[setUserDisabled] Error:`, error);
        throw new HttpsError("internal", `Failed to update disabled state: ${error?.message || error}`);
    }
});

/**
 * Callable: Link (or unlink) a Firebase Auth user to a drivers/{driverDocId} document
 * by writing authId into the driver doc. This lets the mobile app find assigned tasks.
 * If driverDocId is empty, the existing link is cleared.
 */
export const linkDriverToUser = onCall(async (request) => {
    if (!request.auth || request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Admin only");
    }

    const { targetUid, driverDocId } = request.data as { targetUid?: string; driverDocId?: string };

    if (!targetUid) throw new HttpsError("invalid-argument", "targetUid is required");

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
    } catch (err) {
        console.error("[linkDriverToUser] Failed to clear old link:", err);
    }

    if (typeof driverDocId === "string" && driverDocId.trim()) {
        await firestore.collection("drivers").doc(driverDocId.trim()).update({
            authId: targetUid,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        console.log(`[linkDriverToUser] Linked driver doc ${driverDocId} → uid ${targetUid}`);
    }

    return { success: true };
});

/**
 * Callable: Utility to sync existing users to Firestore (Admin only)
 */
export const syncExistingUsers = onCall(async (request) => {
    if (request.auth?.token.admin !== true) {
        throw new HttpsError("permission-denied", "Admin only");
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
    } catch (error: any) {
        throw new HttpsError("internal", error.message);
    }
});
