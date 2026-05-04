#!/usr/bin/env node
/**
 * Script to call backfillTaskCustomerLinks Cloud Function
 * Run from project root with Firebase credentials set
 *
 * Usage: node run-backfill.js
 */

const admin = require("firebase-admin");

// Initialize Firebase Admin (ensure GOOGLE_APPLICATION_CREDENTIALS is set)
if (!admin.apps.length) {
    admin.initializeApp();
}

const functions = admin.functions("asia-southeast1");

async function runBackfill() {
    console.log("🚀 Starting backfillTaskCustomerLinks...\n");

    try {
        // Call the Cloud Function
        const backfillTaskCustomerLinks = functions.httpsCallable("backfillTaskCustomerLinks");
        const result = await backfillTaskCustomerLinks({});

        const data = result.data;

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
