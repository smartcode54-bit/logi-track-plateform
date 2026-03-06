const username = "WANP00003";
const passPlain = "Wanpen@2024";
const passHash = "8affdfbd1ca68699be1c42a85b1e78093ccaa2e5adbd795ba4a028719435fd37";

async function test(label, password) {
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
        console.log(`[${label}] Status: ${res.status} ${res.statusText}`);
    } catch (e) {
        console.log(`[${label}] Error: ${e.message}`);
    }
}

async function run() {
    await test("Plaintext", passPlain);
    await test("Hash", passHash);
}

run();
