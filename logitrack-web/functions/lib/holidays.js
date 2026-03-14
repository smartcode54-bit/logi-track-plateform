"use strict";
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
exports.saveHoliday = exports.deleteHoliday = exports.saveGeneratedHolidays = void 0;
/**
 * Callable Cloud Functions for holidays. See docs/CALLABLE_FUNCTIONS.md for the onCall + httpsCallable pattern.
 */
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const HOLIDAYS_COLLECTION = "holidays";
function dateToKey(dateStr, type) {
    return `${dateStr}|${type ?? ""}`;
}
/**
 * Callable: save generated holidays (upsert kept, delete removed). Admin only.
 */
exports.saveGeneratedHolidays = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can save generated holidays");
    }
    const data = request.data;
    if (!data?.finalHolidays || !Array.isArray(data.finalHolidays) || !data?.initialHolidays || !Array.isArray(data.initialHolidays)) {
        throw new https_1.HttpsError("invalid-argument", "finalHolidays and initialHolidays arrays are required");
    }
    const finalHolidays = data.finalHolidays;
    const initialHolidays = data.initialHolidays;
    const uid = request.auth.uid;
    const db = admin.firestore();
    const holidaysRef = db.collection(HOLIDAYS_COLLECTION);
    // Determine year from payload for query (use first date available)
    const firstDateStr = finalHolidays[0]?.date ?? initialHolidays[0]?.date;
    if (!firstDateStr) {
        return { saved: 0, deleted: 0 };
    }
    const year = new Date(firstDateStr).getFullYear();
    const startOfYear = new Date(year, 0, 1);
    const endOfYear = new Date(year, 11, 31, 23, 59, 59, 999);
    // Fetch existing holidays in that year
    const existingSnap = await holidaysRef
        .where("date", ">=", admin.firestore.Timestamp.fromDate(startOfYear))
        .where("date", "<=", admin.firestore.Timestamp.fromDate(endOfYear))
        .get();
    const existingByKey = {};
    existingSnap.docs.forEach((d) => {
        const docData = d.data();
        const date = docData.date?.toDate?.() ?? new Date(docData.date);
        const dateStr = date.toISOString().slice(0, 10);
        const type = docData.type ?? "";
        existingByKey[dateToKey(dateStr, type)] = { id: d.id, date, type };
    });
    const batch = db.batch();
    // Keys we are keeping
    const finalKeys = new Set(finalHolidays.map((h) => dateToKey(h.date.slice(0, 10), h.type)));
    const removed = initialHolidays.filter((h) => !finalKeys.has(dateToKey(h.date.slice(0, 10), h.type)));
    removed.forEach((h) => {
        const dateStr = h.date.slice(0, 10);
        const key = dateToKey(dateStr, h.type);
        const existing = existingByKey[key];
        if (existing?.id) {
            batch.delete(holidaysRef.doc(existing.id));
        }
    });
    finalHolidays.forEach((h) => {
        const dateStr = h.date.slice(0, 10);
        const key = dateToKey(dateStr, h.type);
        const existing = existingByKey[key];
        const dateTimestamp = admin.firestore.Timestamp.fromDate(new Date(h.date));
        const payload = {
            ...h,
            date: dateTimestamp,
            status: "PUBLISHED",
            updatedAt: admin.firestore.Timestamp.now(),
        };
        if (existing?.id) {
            batch.update(holidaysRef.doc(existing.id), payload);
        }
        else {
            batch.set(holidaysRef.doc(), {
                ...payload,
                createdAt: admin.firestore.Timestamp.now(),
                createdBy: uid,
            });
        }
    });
    await batch.commit();
    return { saved: finalHolidays.length, deleted: removed.length };
});
/**
 * Callable: delete a single holiday by id. Admin only.
 */
exports.deleteHoliday = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can delete holidays");
    }
    const data = request.data;
    if (!data?.id || typeof data.id !== "string") {
        throw new https_1.HttpsError("invalid-argument", "id is required");
    }
    const db = admin.firestore();
    await db.collection(HOLIDAYS_COLLECTION).doc(data.id).delete();
    return { ok: true };
});
/**
 * Callable: create or update a single holiday. Admin only.
 */
exports.saveHoliday = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can save holidays");
    }
    const data = request.data;
    if (!data?.date || typeof data.date !== "string") {
        throw new https_1.HttpsError("invalid-argument", "date is required");
    }
    const uid = request.auth.uid;
    const db = admin.firestore();
    const holidaysRef = db.collection(HOLIDAYS_COLLECTION);
    const dateTimestamp = admin.firestore.Timestamp.fromDate(new Date(data.date));
    const payload = {
        ...data,
        date: dateTimestamp,
        status: data.status ?? "DRAFT",
        updatedAt: admin.firestore.Timestamp.now(),
    };
    if (data.id) {
        await holidaysRef.doc(data.id).update(payload);
        return { id: data.id };
    }
    const docRef = await holidaysRef.add({
        ...payload,
        createdAt: admin.firestore.Timestamp.now(),
        createdBy: uid,
    });
    return { id: docRef.id };
});
//# sourceMappingURL=holidays.js.map