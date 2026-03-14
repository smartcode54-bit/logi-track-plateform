/**
 * Callable Cloud Functions for holidays. See docs/CALLABLE_FUNCTIONS.md for the onCall + httpsCallable pattern.
 */
import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const HOLIDAYS_COLLECTION = "holidays";

/** Serializable holiday payload from client (date as ISO string) */
interface HolidayPayload {
    date: string;
    type?: string;
    name?: string;
    holidayNameEN?: string;
    holidayNameTH?: string;
    description?: string;
    descriptionEn?: string;
    descriptionTh?: string;
    isRecurring?: boolean;
    status?: string;
}

interface SaveGeneratedHolidaysRequest {
    finalHolidays: HolidayPayload[];
    initialHolidays: HolidayPayload[];
}

function dateToKey(dateStr: string, type: string | undefined): string {
    return `${dateStr}|${type ?? ""}`;
}

/**
 * Callable: save generated holidays (upsert kept, delete removed). Admin only.
 */
export const saveGeneratedHolidays = onCall(async (request): Promise<{ saved: number; deleted: number }> => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can save generated holidays");
    }

    const data = request.data as SaveGeneratedHolidaysRequest;
    if (!data?.finalHolidays || !Array.isArray(data.finalHolidays) || !data?.initialHolidays || !Array.isArray(data.initialHolidays)) {
        throw new HttpsError("invalid-argument", "finalHolidays and initialHolidays arrays are required");
    }

    const finalHolidays = data.finalHolidays as HolidayPayload[];
    const initialHolidays = data.initialHolidays as HolidayPayload[];
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

    const existingByKey: Record<string, { id: string; date: Date; type: string }> = {};
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
        const payload: Record<string, unknown> = {
            ...h,
            date: dateTimestamp,
            status: "PUBLISHED",
            updatedAt: admin.firestore.Timestamp.now(),
        };

        if (existing?.id) {
            batch.update(holidaysRef.doc(existing.id), payload);
        } else {
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
export const deleteHoliday = onCall(async (request): Promise<{ ok: boolean }> => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can delete holidays");
    }
    const data = request.data as { id?: string };
    if (!data?.id || typeof data.id !== "string") {
        throw new HttpsError("invalid-argument", "id is required");
    }
    const db = admin.firestore();
    await db.collection(HOLIDAYS_COLLECTION).doc(data.id).delete();
    return { ok: true };
});

/** Request for saveHoliday: create (no id) or update (with id) */
interface SaveHolidayRequest extends HolidayPayload {
    id?: string;
}

/**
 * Callable: create or update a single holiday. Admin only.
 */
export const saveHoliday = onCall(async (request): Promise<{ id: string }> => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can save holidays");
    }
    const data = request.data as SaveHolidayRequest;
    if (!data?.date || typeof data.date !== "string") {
        throw new HttpsError("invalid-argument", "date is required");
    }
    const uid = request.auth.uid;
    const db = admin.firestore();
    const holidaysRef = db.collection(HOLIDAYS_COLLECTION);
    const dateTimestamp = admin.firestore.Timestamp.fromDate(new Date(data.date));
    const payload: Record<string, unknown> = {
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
