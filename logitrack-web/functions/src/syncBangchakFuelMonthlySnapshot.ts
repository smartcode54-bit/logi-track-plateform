/**
 * Admin callable: fetch current Bangchak prices and upsert this month's snapshot (same shape as scheduled job).
 */

import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { logger } from "firebase-functions";
import { persistBangchakFuelMonthlySnapshot } from "./core/persistFuelMonthlySnapshot";

export const syncBangchakFuelMonthlySnapshot = onCall(
    {
        region: "asia-southeast1",
        cors: true,
        enforceAppCheck: false,
        invoker: "public",
        timeoutSeconds: 120,
        memory: "256MiB",
    },
    async (request) => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }
        if (request.auth.token.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }

        const db = admin.firestore();
        const result = await persistBangchakFuelMonthlySnapshot(db, new Date());

        if (result.ok) {
            logger.info("[syncBangchakFuelMonthlySnapshot] OK", {
                monthKey: result.monthKey,
                itemCount: result.itemCount,
            });
            return {
                ok: true as const,
                monthKey: result.monthKey,
                itemCount: result.itemCount,
            };
        }

        logger.warn("[syncBangchakFuelMonthlySnapshot] Stored error doc", {
            monthKey: result.monthKey,
            errorMessage: result.errorMessage,
        });
        return {
            ok: false as const,
            monthKey: result.monthKey,
            itemCount: 0,
            errorMessage: result.errorMessage,
        };
    }
);
