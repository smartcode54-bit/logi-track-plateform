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

        await admin.auth().setCustomUserClaims(targetUid, nextClaims as { [key: string]: unknown });

        // Sync to Firestore
        try {
            const userDoc: Record<string, unknown> = { role: newRole };
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

    const { email, password, displayName, role, partnerScopeId, customerScopeId } = request.data as {
        email?: string;
        password?: string;
        displayName?: string;
        role?: string;
        partnerScopeId?: string;
        customerScopeId?: string;
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
