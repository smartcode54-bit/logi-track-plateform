import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

// Admin emails - hardcoded for now
const ADMIN_EMAILS = [
    "radchada67@gmail.com",
    "smartcode54@gmail.com",
    "admin-test@example.com",
];

/**
 * Cloud Function to set admin claims for authorized users
 */
export const setAdminClaims = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    const uid = request.auth.uid;
    const email = request.auth.token.email;

    console.log(`[setAdminClaims] Processing request for user: ${email} (${uid})`);

    if (email && ADMIN_EMAILS.includes(email.toLowerCase())) {
        try {
            const user = await admin.auth().getUser(uid);
            const currentClaims = user.customClaims || {};

            if (currentClaims.admin === true) {
                return {
                    success: true,
                    admin: true,
                    message: "User already has admin privileges",
                };
            }

            // BOOTSTRAP-ONLY: only auto-grant admin if the user has no role assigned yet.
            // If an admin has explicitly set a different role (e.g. customer, partner),
            // do NOT override it — the hardcoded email list is for first-time bootstrap only.
            if (currentClaims.role && currentClaims.role !== "admin") {
                console.log(`[setAdminClaims] User ${email} is in ADMIN_EMAILS but has explicit role "${currentClaims.role}". Skipping auto-admin.`);
                return {
                    success: true,
                    admin: false,
                    message: `User has explicit role: ${currentClaims.role}`,
                };
            }

            await admin.auth().setCustomUserClaims(uid, {
                ...currentClaims,
                role: "admin",
                admin: true,
            });

            return {
                success: true,
                admin: true,
                message: "Admin privileges granted (bootstrap)",
            };
        } catch (error) {
            console.error(`[setAdminClaims] Error setting admin claim:`, error);
            throw new HttpsError("internal", "Failed to set admin claim");
        }
    } else {
        return {
            success: true,
            admin: false,
            message: "User is not an admin",
        };
    }
});

/**
 * Cloud Function to set driver claims for users who login from mobile app.
 * If the user exists in the drivers collection, set role=driver and sync to users.
 */
export const setDriverClaims = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    const uid = request.auth.uid;
    const email = request.auth.token.email;

    console.log(`[setDriverClaims] Processing request for user: ${email} (${uid})`);

    try {
        const user = await admin.auth().getUser(uid);
        const currentClaims = user.customClaims || {};

        if (currentClaims.role === "driver") {
            return {
                success: true,
                role: "driver",
                message: "User already has driver privileges",
            };
        }

        const driversSnap = await admin.firestore()
            .collection("drivers")
            .where("authId", "==", uid)
            .limit(1)
            .get();

        if (driversSnap.empty) {
            return {
                success: true,
                role: null,
                message: "User is not a driver",
            };
        }

        const driverDoc = driversSnap.docs[0];
        const driverId = driverDoc.id;

        await admin.auth().setCustomUserClaims(uid, {
            ...currentClaims,
            role: "driver",
            driverId,
        });

        await admin.firestore().collection("users").doc(uid).set({
            role: "driver",
        }, { merge: true });

        console.log(`[setDriverClaims] Set driver claims for ${uid} -> driverId ${driverId}`);
        return {
            success: true,
            role: "driver",
            driverId,
            message: "Driver privileges granted",
        };
    } catch (error) {
        console.error(`[setDriverClaims] Error:`, error);
        throw new HttpsError("internal", "Failed to set driver claim");
    }
});

/**
 * Cloud Function to check if a user has admin privileges
 */
export const checkAdminStatus = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }

    const uid = request.auth.uid;
    const email = request.auth.token.email;

    try {
        const user = await admin.auth().getUser(uid);
        const claims = user.customClaims || {};

        return {
            email: email,
            isAdmin: claims.admin === true,
            claims: claims,
        };
    } catch (error) {
        console.error(`[checkAdminStatus] Error:`, error);
        throw new HttpsError("internal", "Failed to check admin status");
    }
});
