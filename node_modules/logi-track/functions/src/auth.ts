import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

// Admin emails - hardcoded for now
const ADMIN_EMAILS = [
    "radchada67@gmail.com",
    "smartcode54@gmail.com",
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

            await admin.auth().setCustomUserClaims(uid, {
                ...currentClaims,
                admin: true,
            });

            return {
                success: true,
                admin: true,
                message: "Admin privileges granted",
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
