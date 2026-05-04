#!/usr/bin/env node
/**
 * Diagnostic script to check which fields are missing in old task and trip_records
 * Run from Firebase project root with service account credentials
 *
 * Usage: node check-backfill-needed.js
 */

const admin = require("firebase-admin");
const path = require("path");

// Initialize Firebase Admin (ensure GOOGLE_APPLICATION_CREDENTIALS is set)
if (!admin.apps.length) {
    admin.initializeApp();
}

const db = admin.firestore();

async function checkBackfillNeeded() {
    console.log("🔍 Checking backfill requirements...\n");

    // Check old tasks
    console.log("1️⃣  Checking TASKS collection...");
    const tasksSnapshot = await db
        .collection("tasks")
        .limit(10)
        .get();

    if (tasksSnapshot.empty) {
        console.log("   ⚠️  No tasks found");
    } else {
        const firstTask = tasksSnapshot.docs[0].data();
        console.log(`   ✓ Sample task ID: ${tasksSnapshot.docs[0].id}`);
        console.log(`   - Has sourceHubLinkedCustomerId: ${!!firstTask.sourceHubLinkedCustomerId}`);
        console.log(`   - Has destinationLinkedCustomerId: ${!!firstTask.destinationLinkedCustomerId}`);
        console.log(`   - Has sourceHub: ${!!firstTask.sourceHub}`);
        console.log(`   - Has destination: ${!!firstTask.destination}`);

        // Count tasks missing customer link fields
        let missingSourceLink = 0;
        let missingDestLink = 0;

        for (const doc of tasksSnapshot.docs) {
            const data = doc.data();
            if (!data.sourceHubLinkedCustomerId) missingSourceLink++;
            if (!data.destinationLinkedCustomerId) missingDestLink++;
        }

        console.log(`\n   📊 In first 10 tasks:`);
        console.log(`      - Missing sourceHubLinkedCustomerId: ${missingSourceLink}`);
        console.log(`      - Missing destinationLinkedCustomerId: ${missingDestLink}`);
    }

    // Check old trip_records
    console.log("\n2️⃣  Checking TRIP_RECORDS collection...");
    const tripsSnapshot = await db
        .collection("trip_records")
        .where("status", "==", "Completed")
        .limit(10)
        .get();

    if (tripsSnapshot.empty) {
        console.log("   ⚠️  No completed trips found");
    } else {
        const firstTrip = tripsSnapshot.docs[0].data();
        console.log(`   ✓ Sample trip ID: ${tripsSnapshot.docs[0].id}`);
        console.log(`   - Has sourceHubLinkedCustomerId: ${!!firstTrip.sourceHubLinkedCustomerId}`);
        console.log(`   - Has destinationLinkedCustomerId: ${!!firstTrip.destinationLinkedCustomerId}`);
        console.log(`   - Has sourceHub: ${!!firstTrip.sourceHub}`);
        console.log(`   - Has destination: ${!!firstTrip.destination}`);
        console.log(`   - Has taskId: ${!!firstTrip.taskId}`);

        // Count trips missing customer link fields
        let missingSourceLink = 0;
        let missingDestLink = 0;

        for (const doc of tripsSnapshot.docs) {
            const data = doc.data();
            if (!data.sourceHubLinkedCustomerId) missingSourceLink++;
            if (!data.destinationLinkedCustomerId) missingDestLink++;
        }

        console.log(`\n   📊 In first 10 completed trips:`);
        console.log(`      - Missing sourceHubLinkedCustomerId: ${missingSourceLink}`);
        console.log(`      - Missing destinationLinkedCustomerId: ${missingDestLink}`);
    }

    // Check customer
    console.log("\n3️⃣  Checking CUSTOMERS collection...");
    const customerDoc = await db.collection("customers").doc("7gbnX0Tv9xNQgTKrgp0F").get();
    if (customerDoc.exists) {
        const customerData = customerDoc.data();
        console.log(`   ✓ Found customer TTP`);
        console.log(`   - code: ${customerData.code}`);
        console.log(`   - name: ${customerData.name || customerData.customerName || "N/A"}`);
    } else {
        console.log(`   ⚠️  Customer 7gbnX0Tv9xNQgTKrgp0F not found!`);
    }

    console.log("\n✅ Diagnostic complete!\n");
    process.exit(0);
}

checkBackfillNeeded().catch((err) => {
    console.error("❌ Error:", err);
    process.exit(1);
});
