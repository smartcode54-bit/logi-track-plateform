"use strict";
/**
 * Cartrack GPS Vehicle Location Sync
 *
 * Scheduled Cloud Function that runs every 3 minutes to fetch vehicle positions
 * from the Cartrack Fleet API and store them in Firestore `vehicle_locations`.
 *
 * Env config (set in functions/.env.{project}):
 *   CARTRACK_API_USERNAME  — Cartrack API username for Basic Auth
 *   CARTRACK_API_PASSWORD  — Cartrack API password for Basic Auth
 */
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.syncVehicleLocations = void 0;
const admin = __importStar(require("firebase-admin"));
const scheduler_1 = require("firebase-functions/v2/scheduler");
const params_1 = require("firebase-functions/params");
const firebase_functions_1 = require("firebase-functions");
// ─── Env Config ──────────────────────────────────────────────────────────────
const cartrackUsername = (0, params_1.defineString)("CARTRACK_API_USERNAME", {
    description: "Cartrack API username for Basic Auth",
});
const cartrackPassword = (0, params_1.defineString)("CARTRACK_API_PASSWORD", {
    description: "Cartrack API password (hashed or plain) for Basic Auth",
});
// ─── Constants ───────────────────────────────────────────────────────────────
const CARTRACK_API_URL = "https://fleetapi-th.cartrack.com/rest/vehicles/status";
const COLLECTION_TRUCKS = "trucks";
const COLLECTION_VEHICLE_LOCATIONS = "vehicle_locations";
// ─── Helper: Fetch from Cartrack API ─────────────────────────────────────────
async function fetchCartrackVehicles(username, password) {
    const authString = Buffer.from(`${username}:${password}`).toString("base64");
    const res = await fetch(CARTRACK_API_URL, {
        method: "GET",
        headers: {
            Authorization: `Basic ${authString}`,
            Accept: "application/json",
        },
    });
    if (!res.ok) {
        throw new Error(`Cartrack API HTTP ${res.status}: ${res.statusText}`);
    }
    const json = await res.json();
    // API may return array directly or { data: [...] }
    return Array.isArray(json) ? json : (json.data || []);
}
// ─── Scheduled Function: Sync every 3 minutes ───────────────────────────────
exports.syncVehicleLocations = (0, scheduler_1.onSchedule)({
    schedule: "every 3 minutes",
    region: "asia-southeast1",
    timeoutSeconds: 60,
    memory: "256MiB",
}, async () => {
    const db = admin.firestore();
    const username = cartrackUsername.value();
    const password = cartrackPassword.value();
    if (!username || !password) {
        firebase_functions_1.logger.error("[syncVehicleLocations] Missing CARTRACK_API_USERNAME or CARTRACK_API_PASSWORD. " +
            "Set them in functions/.env.{project-id}");
        return;
    }
    // 1. Query trucks that have a GPSVehicleId set
    const trucksSnap = await db
        .collection(COLLECTION_TRUCKS)
        .where("GPSVehicleId", "!=", "")
        .get();
    if (trucksSnap.empty) {
        firebase_functions_1.logger.info("[syncVehicleLocations] No trucks with GPSVehicleId found. Skipping.");
        return;
    }
    // Build lookup map: GPSVehicleId (string) → { truckId, licensePlate }
    const truckMap = new Map();
    for (const doc of trucksSnap.docs) {
        const data = doc.data();
        const gpsId = String(data.GPSVehicleId).trim();
        if (gpsId) {
            truckMap.set(gpsId, {
                truckId: doc.id,
                licensePlate: data.licensePlate || "",
            });
        }
    }
    firebase_functions_1.logger.info(`[syncVehicleLocations] Found ${truckMap.size} truck(s) with GPSVehicleId.`, { lookingFor: Array.from(truckMap.keys()) });
    // 2. Fetch vehicle positions from Cartrack
    let vehicles;
    try {
        vehicles = await fetchCartrackVehicles(username, password);
    }
    catch (error) {
        firebase_functions_1.logger.error("[syncVehicleLocations] Cartrack API error:", error.message);
        return;
    }
    firebase_functions_1.logger.info(`[syncVehicleLocations] Cartrack API returned ${vehicles.length} vehicle(s).`, { apiVehicleIds: vehicles.slice(0, 10).map(v => ({ id: v.vehicle_id, reg: v.registration })) });
    // DEBUG: Log first raw vehicle to see actual field names
    if (vehicles.length > 0) {
        firebase_functions_1.logger.info("[syncVehicleLocations] Raw first vehicle:", JSON.stringify(vehicles[0]));
    }
    // 3. Match & write to vehicle_locations
    const now = admin.firestore.Timestamp.now();
    const BATCH_LIMIT = 500;
    let batch = db.batch();
    let batchCount = 0;
    let matchCount = 0;
    for (const v of vehicles) {
        const gpsId = String(v.vehicle_id);
        const truck = truckMap.get(gpsId);
        if (!truck)
            continue; // Not one of our trucks
        const lat = v.location?.latitude;
        const lng = v.location?.longitude;
        if (typeof lat !== "number" || typeof lng !== "number")
            continue;
        const docRef = db
            .collection(COLLECTION_VEHICLE_LOCATIONS)
            .doc(gpsId);
        batch.set(docRef, {
            truckId: truck.truckId,
            GPSVehicleId: gpsId,
            licensePlate: truck.licensePlate,
            lat,
            lng,
            speed: v.speed ?? 0,
            heading: v.bearing ?? 0,
            engineOn: v.ignition ?? false,
            positionDescription: v.location?.position_description ?? "",
            driverName: [v.driver?.first_name, v.driver?.last_name].filter(Boolean).join(" ") || "",
            lastGPSTime: v.event_ts
                ? admin.firestore.Timestamp.fromDate(new Date(v.event_ts))
                : now,
            updatedAt: now,
        });
        matchCount++;
        batchCount++;
        if (batchCount >= BATCH_LIMIT) {
            await batch.commit();
            batch = db.batch();
            batchCount = 0;
        }
    }
    if (batchCount > 0) {
        await batch.commit();
    }
    firebase_functions_1.logger.info(`[syncVehicleLocations] Synced ${matchCount} vehicle location(s) to Firestore.`);
});
//# sourceMappingURL=cartrack.js.map