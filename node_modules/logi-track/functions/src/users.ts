import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

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

    const { targetUid, role, isAdmin } = request.data;

    if (!targetUid) {
        throw new HttpsError("invalid-argument", "Target UID is required");
    }

    try {
        const user = await admin.auth().getUser(targetUid);
        const currentClaims = user.customClaims || {};

        let newRole = role;
        let newIsAdmin = false;

        if (role) {
            newRole = role;
            newIsAdmin = role === 'admin';
        } else if (typeof isAdmin === 'boolean') {
            newRole = isAdmin ? 'admin' : 'user';
            newIsAdmin = isAdmin;
        }

        await admin.auth().setCustomUserClaims(targetUid, {
            ...currentClaims,
            role: newRole,
            admin: newIsAdmin,
        });

        // Sync to Firestore
        try {
            await admin.firestore().collection("users").doc(targetUid).set({
                role: newRole
            }, { merge: true });
        } catch (dbError) {
            console.error(`[updateUserRole] Failed to sync role to Firestore:`, dbError);
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

    const { email, password, displayName, role } = request.data;

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

        await admin.auth().setCustomUserClaims(userRecord.uid, {
            role: userRole,
            admin: isAdmin,
        });

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
