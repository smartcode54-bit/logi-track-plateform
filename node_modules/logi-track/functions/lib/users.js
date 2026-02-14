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
exports.syncExistingUsers = exports.createUser = exports.updateUserRole = exports.getUsers = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
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
    const { targetUid, role, isAdmin } = request.data;
    if (!targetUid) {
        throw new https_1.HttpsError("invalid-argument", "Target UID is required");
    }
    try {
        const user = await admin.auth().getUser(targetUid);
        const currentClaims = user.customClaims || {};
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
        }
        catch (dbError) {
            console.error(`[updateUserRole] Failed to sync role to Firestore:`, dbError);
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
    const { email, password, displayName, role } = request.data;
    if (!email || !password || !displayName) {
        throw new https_1.HttpsError("invalid-argument", "Email, password, and display name are required");
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
    }
    catch (error) {
        console.error(`[createUser] Error creating user:`, error);
        throw new https_1.HttpsError("internal", `Failed to create user: ${error.message || error}`);
    }
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