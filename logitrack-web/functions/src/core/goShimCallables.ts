/**
 * The legacy answers of the T32 callable shims (mv-go, developer-spec.md §11.10, Appendix B §B.2.22, Appendix C §C.6.5;
 * R45): setDriverClaims -> GET /v1/mobile/me and addDeliveryStop -> POST /v1/mobile/tasks/{id}/stops. The installed
 * 3.x APKs keep calling the callables by name; these functions turn the Go answer back into the callable contract,
 * byte for byte (the same keys in the same order, the same messages, the same HttpsError codes), so nothing changes
 * for the device. Pure apart from the claims store, so they are tested without Firebase (goShimCallables.test.ts).
 */

import { HttpsError } from "firebase-functions/v2/https";
import { type ShimAnswer, type ShimLogger, shimData, shimError } from "./goShim";

// --- setDriverClaims -> GET /v1/mobile/me -------------------------------------------------------------------------

/** The three answers of setDriverClaims (fn auth.ts:75-136), keys in their legacy order. */
export type SetDriverClaimsResponse =
    | { success: true; role: "driver"; message: "User already has driver privileges" }
    | { success: true; role: null; message: "User is not a driver" }
    | { success: true; role: "driver"; driverId: string; message: "Driver privileges granted" };

/** The Firebase side of setDriverClaims: the caller's custom claims and its users/{uid} mirror document. */
export interface ClaimsStore {
    getClaims(uid: string): Promise<Record<string, unknown>>;
    setClaims(uid: string, claims: Record<string, unknown>): Promise<void>;
    /** users/{uid} `{role: "driver"}` merged (the legacy mirror write, auth.ts:118-120). */
    markDriver(uid: string): Promise<void>;
}

/** The Go codes that mean "this Firebase user is not a LogiTrack driver" (the legacy "User is not a driver"). */
const NOT_A_DRIVER_CODES = ["no_account", "driver_profile_required"];

/**
 * Reads the answer of GET /v1/mobile/me: the driverId claim (driver.legacyDocId: the Firestore drivers doc id, or the
 * uuid of a driver created in Go), null for a user who is not a driver (no linked driver, no LogiTrack account, a
 * driver membership without its drivers row), undefined when the call failed (unreachable, refused credentials, a
 * disabled account, anything else).
 */
export function driverIdFromMe(status: number, body: unknown): string | null | undefined {
    if (status === 200) {
        const driver = shimData(body).driver;
        if (driver === null || driver === undefined) return null;
        const ref = (driver as Record<string, unknown>).legacyDocId;
        return typeof ref === "string" && ref !== "" ? ref : undefined;
    }
    if (status === 403 && NOT_A_DRIVER_CODES.includes(shimError(body).code)) return null;
    return undefined;
}

/**
 * setDriverClaims through Go: who the caller is comes from GET /v1/mobile/me (PostgreSQL decides, never the Firestore
 * drivers collection); the claims are merged, never overwritten (Appendix C §C.7 #2). A caller who already holds the
 * driver role keeps the legacy "already" answer, and a stale driverId claim is corrected on the way. Every failure is
 * the legacy `internal` "Failed to set driver claim".
 */
export async function setDriverClaimsViaGo(
    uid: string,
    answer: ShimAnswer,
    store: ClaimsStore,
    log: ShimLogger,
): Promise<SetDriverClaimsResponse> {
    const driverId = driverIdFromMe(answer.status, answer.body);
    if (driverId === undefined) {
        const e = shimError(answer.body);
        log.error("[setDriverClaims] shim: GET /v1/mobile/me failed", { status: answer.status, code: e.code });
        throw new HttpsError("internal", "Failed to set driver claim");
    }
    if (driverId === null) {
        return { success: true, role: null, message: "User is not a driver" };
    }
    try {
        const claims = await store.getClaims(uid);
        if (claims.role === "driver") {
            if (claims.driverId !== driverId) {
                await store.setClaims(uid, { ...claims, driverId });
            }
            return { success: true, role: "driver", message: "User already has driver privileges" };
        }
        await store.setClaims(uid, { ...claims, role: "driver", driverId });
        await store.markDriver(uid);
        return { success: true, role: "driver", driverId, message: "Driver privileges granted" };
    } catch (err) {
        log.error("[setDriverClaims] shim: the claims merge failed", { error: String(err) });
        throw new HttpsError("internal", "Failed to set driver claim");
    }
}

// --- addDeliveryStop -> POST /v1/mobile/tasks/{id}/stops ------------------------------------------------------------

/** The answers of addDeliveryStop the shim rebuilds (`{ok, message}`; the legacy deliveryStops echo is not read by
 * the APK, loading_phase_page.dart:1394-1403, and is not rebuilt). */
export interface AddDeliveryStopShimResponse {
    ok: boolean;
    message: string;
}

/** The legacy messages and HttpsError texts of addDeliveryStop (fn multiDeliveryTrips.ts:341-462). */
export const ADD_STOP_MESSAGES = {
    added: "Delivery stop added successfully",
    upgraded: "Task upgraded to multi-delivery; stop added",
    duplicate: "Delivery stop with same destination already exists",
    destinationRequired: "destination is required",
    notFound: "Task not found",
    notOwner: "Not authorized to edit this task",
    noPrimary: "Task has no primary destination — cannot add delivery stops",
    failed: "Failed to add delivery stop",
} as const;

/**
 * The body of the shim call: the destination, and sourceId (a non-empty string, trimmed) and isCustom (true only) as
 * the APK sends them. The APK's destinationLinkedCustomer* fields are the stop hub's linked customer
 * (AddDeliveryStopDialog: h.linkedCustomerId), which Go takes from the hub itself; a driver never names a billing party.
 */
export function addStopShimBody(data: Record<string, unknown>): Record<string, unknown> {
    const out: Record<string, unknown> = { destination: typeof data.destination === "string" ? data.destination : "" };
    if (typeof data.sourceId === "string" && data.sourceId.trim() !== "") out.sourceId = data.sourceId.trim();
    if (data.isCustom === true) out.isCustom = true;
    return out;
}

/**
 * Maps the answer of POST /v1/mobile/tasks/{id}/stops onto the callable: 200/202 `{ok: true, message}` (the upgrade
 * message when the task was single-stop), 409 duplicate_destination `{ok: false, message}`, and the HttpsErrors the
 * legacy callable threw: not-found, permission-denied (another driver's task, not a driver), invalid-argument,
 * failed-precondition (no primary destination); anything else (refused credentials, Go unreachable, a write-back
 * conflict) is internal "Failed to add delivery stop".
 */
export function mapAddStopAnswer(answer: ShimAnswer): AddDeliveryStopShimResponse {
    const { status, body } = answer;
    if (status === 200 || status === 202) {
        const upgraded = shimData(body).upgradedFromSingle === true;
        return { ok: true, message: upgraded ? ADD_STOP_MESSAGES.upgraded : ADD_STOP_MESSAGES.added };
    }
    const e = shimError(body);
    switch (true) {
        case status === 409 && e.code === "duplicate_destination":
            return { ok: false, message: ADD_STOP_MESSAGES.duplicate };
        case status === 409 && e.reason === "no_primary_destination":
            throw new HttpsError("failed-precondition", ADD_STOP_MESSAGES.noPrimary);
        case status === 404:
            throw new HttpsError("not-found", ADD_STOP_MESSAGES.notFound);
        case status === 403:
            throw new HttpsError("permission-denied", ADD_STOP_MESSAGES.notOwner);
        case status === 422 || status === 400:
            throw new HttpsError("invalid-argument", e.field === "destination" || e.field === "" ? ADD_STOP_MESSAGES.destinationRequired : `${e.field} is invalid`);
    }
    throw new HttpsError("internal", ADD_STOP_MESSAGES.failed);
}
