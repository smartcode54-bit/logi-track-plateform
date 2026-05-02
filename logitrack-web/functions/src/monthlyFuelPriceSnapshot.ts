/**
 * Runs daily 05:00 Asia/Bangkok: fetch Bangchak retail prices and upsert fuel_monthly_snapshots/{yyyy-MM}.
 * The calendar-month doc is refreshed on each run (manual sync uses the same writer).
 */

import * as admin from "firebase-admin";
import { onSchedule } from "firebase-functions/v2/scheduler";
import { defineString } from "firebase-functions/params";
import { logger } from "firebase-functions";
import { persistBangchakFuelMonthlySnapshot } from "./core/persistFuelMonthlySnapshot";

const scheduledFuelSnapshotEnabled = defineString("FUEL_MONTHLY_SNAPSHOT_ENABLED", {
    description:
        "Enable scheduled Bangchak fuel price snapshot job ('true'/'false'). Runs daily 05:00 Bangkok; env name kept for backward compatibility.",
    default: "true",
});

export const recordMonthlyBangchakFuelSnapshot = onSchedule(
    {
        schedule: "0 5 * * *",
        timeZone: "Asia/Bangkok",
        region: "asia-southeast1",
        timeoutSeconds: 120,
        memory: "256MiB",
    },
    async () => {
        const db = admin.firestore();

        if (scheduledFuelSnapshotEnabled.value() !== "true") {
            logger.info("[recordMonthlyBangchakFuelSnapshot] Disabled via FUEL_MONTHLY_SNAPSHOT_ENABLED.");
            return;
        }

        const result = await persistBangchakFuelMonthlySnapshot(db, new Date());

        if (result.ok) {
            logger.info("[recordMonthlyBangchakFuelSnapshot] Stored snapshot", {
                monthKey: result.monthKey,
                count: result.itemCount,
            });
        } else {
            logger.error("[recordMonthlyBangchakFuelSnapshot] Failed", {
                monthKey: result.monthKey,
                message: result.errorMessage,
            });
        }
    }
);
