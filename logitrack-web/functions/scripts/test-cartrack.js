// test-cartrack-api.js

/**
 * Script for fetching Vehicle IDs from Cartrack API
 * Usage: 
 *   node test-cartrack-api.js
 * 
 * NOTE: Replace the username and password with your actual credentials
 */

const username = "WANP00003";
const password = "8affdfbd1ca68699be1c42a85b1e78093ccaa2e5adbd795ba4a028719435fd37";

async function getVehicles() {
    // To list all vehicles, we usually hit the /vehicles or /vehicles/status endpoint.
    // Using /vehicles/status as it's the one we use for tracking and contains the IDs.
    const url = "https://fleetapi-th.cartrack.com/rest/vehicles/status";
    const authString = Buffer.from(`${username}:${password}`).toString("base64");

    console.log("Fetching vehicles from Cartrack API...");

    try {
        const res = await fetch(url, {
            method: "GET",
            headers: {
                "Authorization": `Basic ${authString}`,
                "Accept": "application/json",
            }
        });

        if (!res.ok) {
            if (res.status === 401) {
                console.error("❌ Authentication Failed (401). Check your username and password.");
            } else {
                console.error(`❌ HTTP Error: ${res.status} ${res.statusText}`);
            }
            process.exit(1);
        }

        const json = await res.json();
        const data = Array.isArray(json) ? json : (json.data || []);

        console.log(`\n✅ Successfully fetched ${data.length} vehicles:\n`);

        // Create a nicely formatted table in the console
        const formattedData = data.map(v => ({
            Vehicle_ID: v.vehicle_id,
            License_Plate: v.registration,
            Status: v.engine_on ? "🟢 ENGINE ON" : "⚪ ENGINE OFF"
        }));

        console.table(formattedData);

        console.log("\n💡 To use these in your Logitrack .env file:");
        console.log(`CARTRACK_PILOT_VEHICLE_IDS=${data.slice(0, 2).map(v => v.vehicle_id).join(',')}\n`);

    } catch (error) {
        console.error("❌ Request Error:", error.message);
    }
}

getVehicles();
