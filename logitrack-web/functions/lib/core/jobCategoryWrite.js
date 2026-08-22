"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.resolveJobCategoryWrite = resolveJobCategoryWrite;
/**
 * Decide the jobCategory value to persist on a task write (ADR 0010 R2).
 *
 * A `undefined` return means **omit the field**: on an UPDATE that leaves the stored value
 * untouched — the whole point. Coercing to PRIMARY on every write used to silently reset a เสริม
 * task back to หลัก when a caller updated other fields without resending jobCategory. On a CREATE
 * with nothing sent we still default PRIMARY.
 */
function resolveJobCategoryWrite(sent, isCreate) {
    if (sent !== undefined) {
        return sent === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY";
    }
    return isCreate ? "PRIMARY" : undefined;
}
//# sourceMappingURL=jobCategoryWrite.js.map