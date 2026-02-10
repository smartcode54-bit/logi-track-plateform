import * as admin from "firebase-admin";
import * as functions from "firebase-functions/v1";

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
 * Trigger: specific logic when a Driver is created in Firestore
 * Creates a corresponding Firebase Auth user
 */
export const onDriverCreated = functions.region("asia-southeast1").firestore.document("drivers/{driverId}").onCreate(async (snap, context) => {
    const driverData = snap.data();
    const { email, mobile, firstName, lastName, role } = driverData;

    if (!email || !mobile) {
        console.log(`[onDriverCreated] Missing email or mobile for driver ${context.params.driverId}, skipping Auth creation.`);
        return;
    }

    try {
        // Sanitize mobile number for password (digits only)
        const password = mobile.replace(/\D/g, '');

        if (password.length < 6) {
            console.warn(`[onDriverCreated] Mobile number ${mobile} is too short for password. Using default or original.`);
            // You might handle this differently, e.g. failing or using a default
        }

        // Create Auth User
        // Using sanitized mobile number as initial password
        const userRecord = await admin.auth().createUser({
            email: email,
            password: password.length >= 6 ? password : mobile, // Fallback to original if sanitized is too short (unlikely for valid phone)
            displayName: `${firstName} ${lastName}`,
            disabled: false,
        });

        // Set Custom Claims
        await admin.auth().setCustomUserClaims(userRecord.uid, {
            role: 'driver',
            driverId: context.params.driverId
        });

        // Update Driver doc with Auth UID
        await snap.ref.update({
            authUid: userRecord.uid,
            authCreatedAt: admin.firestore.FieldValue.serverTimestamp()
        });

        console.log(`[onDriverCreated] Successfully created Auth user for driver ${email}`);

    } catch (error: any) {
        console.error(`[onDriverCreated] Error creating Auth user for driver ${email}:`, error);
        // If user already exists, we might want to link or ignore
        if (error.code === 'auth/email-already-exists') {
            console.log(`[onDriverCreated] User ${email} already exists in Auth.`);
            try {
                const user = await admin.auth().getUserByEmail(email);
                await admin.auth().setCustomUserClaims(user.uid, {
                    role: 'driver',
                    driverId: context.params.driverId
                });
                await snap.ref.update({ authUid: user.uid });
            } catch (e) {
                console.error("Error linking existing user", e);
            }
        }
    }
});
