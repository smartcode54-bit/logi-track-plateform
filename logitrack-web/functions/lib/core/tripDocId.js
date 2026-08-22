"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.validateTripDocId = validateTripDocId;
/**
 * Pure validation for `trip_records` document IDs — no firebase imports, so it stays unit-testable
 * (same split as core/jobCategoryWrite.ts).
 *
 * `trip_records` keys documents by the trip ID the driver types, so an admin correcting a typo picks
 * the new document ID by hand. These are the constraints Firestore places on a document ID; a value
 * that violates them either throws deep in the SDK or resolves to something other than a sibling
 * document (a "/" turns the rest into a subcollection path).
 */
function validateTripDocId(id) {
    if (!id)
        return "Trip ID is required";
    if (id.includes("/"))
        return "Trip ID cannot contain '/'";
    if (id === "." || id === "..")
        return "Trip ID cannot be '.' or '..'";
    if (/^__.*__$/.test(id))
        return "Trip ID cannot be wrapped in double underscores";
    // The limit is 1500 *bytes*: Thai trip labels cost 3 bytes per character.
    if (new TextEncoder().encode(id).length > 1500)
        return "Trip ID is too long";
    return null;
}
//# sourceMappingURL=tripDocId.js.map