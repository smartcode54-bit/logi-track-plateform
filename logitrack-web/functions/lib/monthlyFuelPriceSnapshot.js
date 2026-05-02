"use strict";
/**
 * Runs daily 05:00 Asia/Bangkok: fetch Bangchak retail prices and upsert fuel_monthly_snapshots/{yyyy-MM}.
 * The calendar-month doc is refreshed on each run (manual sync uses the same writer).
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
exports.recordMonthlyBangchakFuelSnapshot = void 0;
const admin = __importStar(require("firebase-admin"));
const scheduler_1 = require("firebase-functions/v2/scheduler");
const params_1 = require("firebase-functions/params");
const firebase_functions_1 = require("firebase-functions");
const persistFuelMonthlySnapshot_1 = require("./core/persistFuelMonthlySnapshot");
const scheduledFuelSnapshotEnabled = (0, params_1.defineString)("FUEL_MONTHLY_SNAPSHOT_ENABLED", {
    description: "Enable scheduled Bangchak fuel price snapshot job ('true'/'false'). Runs daily 05:00 Bangkok; env name kept for backward compatibility.",
    default: "true",
});
exports.recordMonthlyBangchakFuelSnapshot = (0, scheduler_1.onSchedule)({
    schedule: "0 5 * * *",
    timeZone: "Asia/Bangkok",
    region: "asia-southeast1",
    timeoutSeconds: 120,
    memory: "256MiB",
}, async () => {
    const db = admin.firestore();
    if (scheduledFuelSnapshotEnabled.value() !== "true") {
        firebase_functions_1.logger.info("[recordMonthlyBangchakFuelSnapshot] Disabled via FUEL_MONTHLY_SNAPSHOT_ENABLED.");
        return;
    }
    const result = await (0, persistFuelMonthlySnapshot_1.persistBangchakFuelMonthlySnapshot)(db, new Date());
    if (result.ok) {
        firebase_functions_1.logger.info("[recordMonthlyBangchakFuelSnapshot] Stored snapshot", {
            monthKey: result.monthKey,
            count: result.itemCount,
        });
    }
    else {
        firebase_functions_1.logger.error("[recordMonthlyBangchakFuelSnapshot] Failed", {
            monthKey: result.monthKey,
            message: result.errorMessage,
        });
    }
});
//# sourceMappingURL=monthlyFuelPriceSnapshot.js.map