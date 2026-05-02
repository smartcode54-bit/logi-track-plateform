"use strict";
/**
 * Admin callable: fetch current Bangchak prices and upsert this month's snapshot (same shape as scheduled job).
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
exports.syncBangchakFuelMonthlySnapshot = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const firebase_functions_1 = require("firebase-functions");
const persistFuelMonthlySnapshot_1 = require("./core/persistFuelMonthlySnapshot");
exports.syncBangchakFuelMonthlySnapshot = (0, https_1.onCall)({
    region: "asia-southeast1",
    cors: true,
    enforceAppCheck: false,
    invoker: "public",
    timeoutSeconds: 120,
    memory: "256MiB",
}, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const db = admin.firestore();
    const result = await (0, persistFuelMonthlySnapshot_1.persistBangchakFuelMonthlySnapshot)(db, new Date());
    if (result.ok) {
        firebase_functions_1.logger.info("[syncBangchakFuelMonthlySnapshot] OK", {
            monthKey: result.monthKey,
            itemCount: result.itemCount,
        });
        return {
            ok: true,
            monthKey: result.monthKey,
            itemCount: result.itemCount,
        };
    }
    firebase_functions_1.logger.warn("[syncBangchakFuelMonthlySnapshot] Stored error doc", {
        monthKey: result.monthKey,
        errorMessage: result.errorMessage,
    });
    return {
        ok: false,
        monthKey: result.monthKey,
        itemCount: 0,
        errorMessage: result.errorMessage,
    };
});
//# sourceMappingURL=syncBangchakFuelMonthlySnapshot.js.map