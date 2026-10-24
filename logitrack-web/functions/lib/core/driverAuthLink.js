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
exports.buildDriverRelinkUpdate = buildDriverRelinkUpdate;
const admin = __importStar(require("firebase-admin"));
/**
 * Build the Firestore update that re-links a driver doc to `newAuthId` while
 * preserving the previously-linked Auth UID in `authIdHistory`.
 *
 * Why: trip_records / vehicle_expenses / standby_records store whichever Auth
 * UID was current when the doc was written. When an email / Google re-link
 * moves the driver to a NEW uid, those old docs would otherwise resolve to no
 * driver and a report would show a raw id (e.g. `W47P8pGs…`). Keeping every
 * past uid in `authIdHistory` lets name resolution find them again — see
 * lib/driverName.ts `driverIdentityKeys` on the web side.
 *
 * Every server path that OVERWRITES `drivers/{doc}.authId` with a different uid
 * must route through this (updateDriverAccount, createUser-link, linkDriverToUser)
 * so no past uid is silently dropped.
 *
 * Pass the driver doc's current authId (already read from the doc) as
 * `currentAuthId`; it may be undefined for a freshly-created driver.
 */
function buildDriverRelinkUpdate(currentAuthId, newAuthId) {
    const update = { authId: newAuthId };
    const prev = String(currentAuthId ?? "").trim();
    if (prev && prev !== newAuthId) {
        update.authIdHistory = admin.firestore.FieldValue.arrayUnion(prev);
    }
    return update;
}
//# sourceMappingURL=driverAuthLink.js.map