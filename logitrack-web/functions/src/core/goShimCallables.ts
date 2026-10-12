/**
 * The legacy answers of the T32 callable shims (mv-go, developer-spec.md §11.10, Appendix B §B.2.22, Appendix C §C.6.5;
 * R45): setDriverClaims -> GET /v1/mobile/me and addDeliveryStop -> POST /v1/mobile/tasks/{id}/stops, and of T48:
 * markBroadcastRead -> POST /v1/mobile/broadcasts/{id}/read. The installed
 * 3.x APKs keep calling the callables by name; these functions turn the Go answer back into the callable contract,
 * byte for byte (the same keys in the same order, the same messages, the same HttpsError codes), so nothing changes
 * for the device. Pure apart from the claims store, so they are tested without Firebase (goShimCallables.test.ts).
 */

import { HttpsError } from "firebase-functions/v2/https";
import { type ShimAnswer, type ShimLogger, isMobileShimCall, shimData, shimError, shimFailureLogged } from "./goShim";

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
 * Whether addDeliveryStop forwards a call to Go: the shim is on (base, shimTarget) and the caller holds the driver
 * claims setDriverClaims gives (role driver or a driverId, never an admin; isMobileShimCall). Any other Firebase user
 * keeps the legacy path and its permission check, so a sign-in without those claims never spends the rate budget the
 * one cf_shim key shares between every driver.
 */
export function addStopForwards(base: string, token: Record<string, unknown> | undefined): boolean {
    return base !== "" && isMobileShimCall(token, false);
}

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
 * details.reason of the Go 404 for a task that does not exist for the driver (operations.ReasonTaskNotFound). Any
 * other 404 (a route an older API lacks, an API without FIREBASE_PROJECT_ID, a wrong LOGITRACK_API_BASE_URL) is a
 * configuration fault, not a missing task.
 */
export const TASK_NOT_FOUND_REASON = "task_not_found";

/**
 * Maps the answer of POST /v1/mobile/tasks/{id}/stops onto the callable: 200/202 `{ok: true, message}` (the upgrade
 * message when the task was single-stop), 409 duplicate_destination `{ok: false, message}`, and the HttpsErrors the
 * legacy callable threw: not-found (404 task_not_found only), permission-denied (another driver's task, not a
 * driver), invalid-argument, failed-precondition (no primary destination). Anything else (refused credentials, Go
 * unreachable or failing, a 404 that is not a missing task, a cf_shim key on the wrong listener, a rate limit, a
 * write-back conflict) is internal "Failed to add delivery stop", logged as an error once (here, unless callShim
 * already did) so an operator sees the API's status, code and reason.
 */
export function mapAddStopAnswer(answer: ShimAnswer, log: ShimLogger): AddDeliveryStopShimResponse {
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
        case status === 404 && e.code === "not_found" && e.reason === TASK_NOT_FOUND_REASON:
            throw new HttpsError("not-found", ADD_STOP_MESSAGES.notFound);
        case status === 403 && e.reason !== "api_key_scope":
            throw new HttpsError("permission-denied", ADD_STOP_MESSAGES.notOwner);
        case status === 422 || status === 400:
            throw new HttpsError("invalid-argument", e.field === "destination" || e.field === "" ? ADD_STOP_MESSAGES.destinationRequired : `${e.field} is invalid`);
    }
    if (!shimFailureLogged(answer)) {
        log.error("[addDeliveryStop] shim: unexpected answer from the Go API", { status, code: e.code, reason: e.reason });
    }
    throw new HttpsError("internal", ADD_STOP_MESSAGES.failed);
}

// --- markBroadcastRead -> POST /v1/mobile/broadcasts/{id}/read (T48) ------------------------------------------------

/** The answer of markBroadcastRead (fn chat.ts:203-248), keys in their legacy order. */
export interface MarkBroadcastReadResponse {
    ok: boolean;
    alreadyRead: boolean;
}

/** The legacy HttpsError texts of markBroadcastRead, and the text of a failed shim call. */
export const MARK_READ_MESSAGES = {
    notFound: "Broadcast not found",
    failed: "Failed to mark the broadcast read",
} as const;

/**
 * details.reason of the Go 404 for a broadcast that does not exist for the driver (broadcasts.ReasonBroadcastNotFound:
 * unknown, voided, or not readable by the driver). Any other 404 is a configuration fault, not a missing broadcast.
 */
export const BROADCAST_NOT_FOUND_REASON = "broadcast_not_found";

/**
 * Whether markBroadcastRead forwards a call to Go: the shim is on and the caller holds the driver claims (role driver or
 * a driverId, never an admin; isMobileShimCall). Any other Firebase user keeps the legacy path.
 */
export function markReadForwards(base: string, token: Record<string, unknown> | undefined): boolean {
    return base !== "" && isMobileShimCall(token, false);
}

/**
 * Maps the answer of POST /v1/mobile/broadcasts/{id}/read onto the callable: 200 `{ok: true, alreadyRead}` (the
 * broadcast_reads primary key makes a second read alreadyRead true), 404 broadcast_not_found the legacy not-found
 * "Broadcast not found". Anything else (refused credentials, Go unreachable or failing, another 404, a rate limit, a
 * driver without a driver profile in PostgreSQL) is internal, logged as an error once (here, unless callShim already
 * did); the APK ignores the answer (broadcast_detail_page.dart:38-47).
 */
export function mapMarkReadAnswer(answer: ShimAnswer, log: ShimLogger): MarkBroadcastReadResponse {
    const { status, body } = answer;
    if (status === 200) {
        return { ok: true, alreadyRead: shimData(body).alreadyRead === true };
    }
    const e = shimError(body);
    if (status === 404 && e.code === "not_found" && e.reason === BROADCAST_NOT_FOUND_REASON) {
        throw new HttpsError("not-found", MARK_READ_MESSAGES.notFound);
    }
    if (!shimFailureLogged(answer)) {
        log.error("[markBroadcastRead] shim: unexpected answer from the Go API", { status, code: e.code, reason: e.reason });
    }
    throw new HttpsError("internal", MARK_READ_MESSAGES.failed);
}
