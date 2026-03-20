const username = "WANP00003";
const password = "8affdfbd1ca68699be1c42a85b1e78093ccaa2e5adbd795ba4a028719435fd37";

async function getVehicles() {
    const url = "https://fleetapi-th.cartrack.com/rest/vehicles/status";
    const authString = Buffer.from(`${username}:${password}`).toString("base64");

    try {
        const res = await fetch(url, {
            method: "GET",
            headers: {
                "Authorization": `Basic ${authString}`,
                "Accept": "application/json",
            }
        });

        if (!res.ok) {
            console.error(`❌ HTTP Error: ${res.status}`);
            return;
        }

        const json = await res.json();
        const data = Array.isArray(json) ? json : (json.data || []);

        if (data.length > 0) {
            console.log("\n--- Full JSON for First Vehicle ---");
            console.log(JSON.stringify(data[0], null, 2));
            console.log("\n--- Fields List ---");
            console.log(Object.keys(data[0]).join(", "));
        } else {
            console.log("No vehicles found.");
        }

    } catch (error) {
        console.error("❌ Request Error:", error.message);
    }
}

getVehicles();
