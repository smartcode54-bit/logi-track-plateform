#!/usr/bin/env node
/**
 * Script to call backfillTaskCustomerLinks Cloud Function via REST API
 * Run from logitrack-web/functions directory
 *
 * Usage: GOOGLE_APPLICATION_CREDENTIALS=path/to/serviceAccountKey.json node run-backfill.js
 */

const admin = require("firebase-admin");

// Initialize Firebase Admin (ensure GOOGLE_APPLICATION_CREDENTIALS is set)
if (!admin.apps.length) {
    admin.initializeApp();
}

const projectId = "logitrack-prod";
const region = "asia-southeast1";

async function runBackfill() {
    console.log("🚀 Starting backfillTaskCustomerLinks...\n");

    try {
        // Get ID token using service account
        const idToken = await admin.app().auth().verifyIdToken(
            await admin.auth().createCustomToken("backfill-runner")
        ).catch(async () => {
            // Alternative: use service account to get access token
            const tokenResponse = await admin
                .auth()
                .createCustomToken("backfill-runner");
            return tokenResponse;
        });

        // Call via REST API using Admin SDK's token
        const token = await admin.auth().createCustomToken("backfill-admin");

        console.log("Getting credentials...");
        const cred = admin.credential.cert(process.env.GOOGLE_APPLICATION_CREDENTIALS || require("./serviceAccountKey.json"));

        // Use the Cloud Functions emulator if available, otherwise use production
        const baseUrl = process.env.FUNCTIONS_EMULATOR
            ? "http://localhost:5001"
            : `https://${region}-${projectId}.cloudfunctions.net`;

        const functionUrl = `${baseUrl}/backfillTaskCustomerLinks`;

        console.log(`Calling function: ${functionUrl}\n`);

        // Use the default credentials to authenticate
        const response = await admin
            .auth()
            .getUser("backfill-runner")
            .then(async () => {
                // User exists or doesn't matter for this call
                // Make the request using the service account token
                const jwt = require("jsonwebtoken");
                const key = process.env.GOOGLE_APPLICATION_CREDENTIALS
                    ? require(process.env.GOOGLE_APPLICATION_CREDENTIALS)
                    : null;

                if (!key) {
                    throw new Error("GOOGLE_APPLICATION_CREDENTIALS not set or invalid");
                }

                const token = jwt.sign(
                    {
                        iss: key.client_email,
                        scope: "https://www.googleapis.com/auth/cloud-platform",
                        aud: "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit",
                        exp: Math.floor(Date.now() / 1000) + 3600,
                        iat: Math.floor(Date.now() / 1000),
                    },
                    key.private_key,
                    { algorithm: "RS256" }
                );

                const fetch = (await import("node-fetch")).default;
                return fetch(functionUrl, {
                    method: "POST",
                    headers: {
                        "Content-Type": "application/json",
                        Authorization: `Bearer ${token}`,
                    },
                    body: JSON.stringify({}),
                });
            });

        if (!response.ok) {
            const error = await response.text();
            throw new Error(`HTTP ${response.status}: ${error}`);
        }

        const result = await response.json();
        const data = result.result || result;

        console.log("✅ Backfill completed!\n");
        console.log("📊 Results:");
        console.log(`   Total processed: ${data.totalProcessed}`);
        console.log(`   Updated: ${data.updated}`);
        console.log(`   Already complete: ${data.alreadyComplete}`);
        console.log(`   Errors: ${data.errors}`);

        if (data.errors > 0 && data.errorDetails?.length > 0) {
            console.log("\n⚠️  Error details:");
            data.errorDetails.forEach((err) => {
                console.log(`   - ${err}`);
            });
        }

        console.log("\n✨ Done!");
        process.exit(0);
    } catch (error) {
        console.error("❌ Error calling backfill function:");
        console.error(error.message);
        if (error.details) {
            console.error("Details:", error.details);
        }
        process.exit(1);
    }
}

runBackfill();
