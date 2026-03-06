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

import * as admin from "firebase-admin";
import { onSchedule } from "firebase-functions/v2/scheduler";
import { defineString } from "firebase-functions/params";
import { logger } from "firebase-functions";

// ─── Env Config ──────────────────────────────────────────────────────────────
const cartrackUsername = defineString("CARTRACK_API_USERNAME", {
    description: "Cartrack API username for Basic Auth",
});
const cartrackPassword = defineString("CARTRACK_API_PASSWORD", {
    description: "Cartrack API password (hashed or plain) for Basic Auth",
});

// ─── Constants ───────────────────────────────────────────────────────────────
const CARTRACK_API_URL = "https://fleetapi-th.cartrack.com/rest/vehicles/status";
const COLLECTION_TRUCKS = "trucks";
const COLLECTION_VEHICLE_LOCATIONS = "vehicle_locations";

// ─── Cartrack API Response Type ──────────────────────────────────────────────
interface CartrackVehicle {
    vehicle_id: number;
    registration: string;
    speed: number;
    bearing: number;
    ignition: boolean;
    event_ts: string; // e.g. "2026-03-06 10:18:26+07"
    location: {
        latitude: number;
        longitude: number;
        position_description?: string;
    };
    driver?: {
        first_name?: string;
        last_name?: string;
    };
    [key: string]: unknown;
}

// ─── Helper: Fetch from Cartrack API ─────────────────────────────────────────
async function fetchCartrackVehicles(
    username: string,
    password: string
): Promise<CartrackVehicle[]> {
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
export const syncVehicleLocations = onSchedule(
    {
        schedule: "every 3 minutes",
        region: "asia-southeast1",
        timeoutSeconds: 60,
        memory: "256MiB",
    },
    async () => {
        const db = admin.firestore();
        const username = cartrackUsername.value();
        const password = cartrackPassword.value();

        if (!username || !password) {
            logger.error(
                "[syncVehicleLocations] Missing CARTRACK_API_USERNAME or CARTRACK_API_PASSWORD. " +
                "Set them in functions/.env.{project-id}"
            );
            return;
        }

        // 1. Query trucks that have a GPSVehicleId set
        const trucksSnap = await db
            .collection(COLLECTION_TRUCKS)
            .where("GPSVehicleId", "!=", "")
            .get();

        if (trucksSnap.empty) {
            logger.info("[syncVehicleLocations] No trucks with GPSVehicleId found. Skipping.");
            return;
        }

        // Build lookup map: GPSVehicleId (string) → { truckId, licensePlate }
        const truckMap = new Map<
            string,
            { truckId: string; licensePlate: string }
        >();
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

        logger.info(`[syncVehicleLocations] Found ${truckMap.size} truck(s) with GPSVehicleId.`);

        // 2. Fetch vehicle positions from Cartrack
        let vehicles: CartrackVehicle[];
        try {
            vehicles = await fetchCartrackVehicles(username, password);
        } catch (error: any) {
            logger.error("[syncVehicleLocations] Cartrack API error:", error.message);
            return;
        }

        logger.info(`[syncVehicleLocations] Cartrack API returned ${vehicles.length} vehicle(s).`);

        // 3. Match & write to vehicle_locations
        const now = admin.firestore.Timestamp.now();
        const BATCH_LIMIT = 500;
        let batch = db.batch();
        let batchCount = 0;
        let matchCount = 0;

        for (const v of vehicles) {
            const gpsId = String(v.vehicle_id);
            const truck = truckMap.get(gpsId);
            if (!truck) continue; // Not one of our trucks

            const lat = v.location?.latitude;
            const lng = v.location?.longitude;
            if (typeof lat !== "number" || typeof lng !== "number") continue;

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

        logger.info(
            `[syncVehicleLocations] Synced ${matchCount} vehicle location(s) to Firestore.`
        );
    }
);
