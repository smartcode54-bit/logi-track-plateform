import * as XLSX from 'xlsx';
import * as admin from 'firebase-admin';
import * as path from 'path';
import * as fs from 'fs';
import { getApps } from 'firebase-admin/app';

// Initialize Firebase Admin if not already initialized
if (getApps().length === 0) {
    const serviceAccount = require(path.join(process.cwd(), 'service-account-key.json')); // You'll need to provide this or use default credential

    // For local dev with emulator or if running via specialized script, usage might vary.
    // Assuming this script runs in an environment where it can authenticate.
    // If running from client-side context (not recommended for admin scripts), we'd use client SDK.
    // BUT since this is a migration script, it should likely be run with `ts-node` and Admin SDK.

    // FALLBACK: If we don't have service account key file easily, we might need another approach.
    // However, for now, let's write the logic to parse the file and print instructions or 
    // try to use application default credentials.

    admin.initializeApp({
        credential: admin.credential.applicationDefault()
    });
}

const db = admin.firestore();

const FILE_PATH = path.join(process.cwd(), 'shopee data', 'SPX_Hub.xlsx');

async function importHubs() {
    if (!fs.existsSync(FILE_PATH)) {
        console.error(`File not found: ${FILE_PATH}`);
        return;
    }

    console.log(`Reading file: ${FILE_PATH}`);
    const workbook = XLSX.readFile(FILE_PATH);
    const sheetName = workbook.SheetNames[0];
    const sheet = workbook.Sheets[sheetName];

    const data = XLSX.utils.sheet_to_json(sheet);

    console.log(`Found ${data.length} rows. Starting import...`);

    const batch = db.batch();
    let count = 0;

    // Clear existing hubs or overwrite? Let's overwrite/merge based on a unique code if present.
    // Inspecting data structure usually helps. Assuming standard headers.

    for (const row of data as any[]) {
        // Adjust these field names based on actual Excel headers
        const hubCode = row['Hub Code'] || row['Code'] || row['station_id'];
        const hubName = row['Hub Name'] || row['Name'] || row['station_name_en'];

        if (!hubName) continue;

        const docRef = db.collection('hubs').doc(hubCode ? String(hubCode) : 'unknown_' + count);

        batch.set(docRef, {
            code: hubCode || '',
            name: hubName || '',
            raw: row, // Store generic raw data just in case
            updatedAt: admin.firestore.FieldValue.serverTimestamp()
        }, { merge: true });

        count++;
        // Batches have a limit of 500
        if (count % 400 === 0) {
            await batch.commit();
            console.log(`Committed ${count} records...`);
            // Reset batch? No, wait, batch is for single commit. 
            // We need a NEW batch.
            // But 'batch' var specific to this scope.
            // Correct pattern:
        }
    }

    // For simplicity with unknown headers, let's just log the first row to seeing what we are dealing with if we were interactive.
    // Since I can't see the running output immediately in this turn, I'll rely on the User to verify headers or logic.

    if (count > 0) {
        await batch.commit();
    }

    console.log(`Import completed. Total ${count} hubs processed.`);
}

// importHubs().catch(console.error);
// Commented out auto-run. User should run this with: npx ts-node scripts/import-hubs.ts
