"use client";

import { doc, getDoc, onSnapshot, serverTimestamp, setDoc, Timestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS, MOBILE_APP_SETTINGS_DOC } from "@/lib/collections";
import { stripUndefined } from "@/lib/firestoreWrite";
import type { MobileAppSettings, MobileAppSettingsFormValues } from "@/validate/mobileAppSettingsSchema";

function settingsRef() {
    return doc(db, COLLECTIONS.SETTINGS, MOBILE_APP_SETTINGS_DOC);
}

function toDate(value: unknown): Date | null {
    return value instanceof Timestamp ? value.toDate() : null;
}

function toSettings(data: Record<string, unknown> | undefined): MobileAppSettings {
    return {
        minAllowedVersion: typeof data?.minAllowedVersion === "string" ? data.minAllowedVersion : "",
        apkDownloadUrl: typeof data?.apkDownloadUrl === "string" ? data.apkDownloadUrl : "",
        releaseNotes: typeof data?.releaseNotes === "string" ? data.releaseNotes : "",
        minAllowedVersionSetAt: toDate(data?.minAllowedVersionSetAt),
        minAllowedVersionSetBy:
            typeof data?.minAllowedVersionSetBy === "string" ? data.minAllowedVersionSetBy : "",
        latestVersion: typeof data?.latestVersion === "string" ? data.latestVersion : "",
        latestBuildNumber: typeof data?.latestBuildNumber === "string" ? data.latestBuildNumber : "",
        apkSizeBytes: typeof data?.apkSizeBytes === "number" ? data.apkSizeBytes : undefined,
        apkSha256: typeof data?.apkSha256 === "string" ? data.apkSha256 : "",
        flavor: typeof data?.flavor === "string" ? data.flavor : "",
        releasedAt: toDate(data?.releasedAt),
        releasedBy: typeof data?.releasedBy === "string" ? data.releasedBy : "",
    };
}

/** Read the driver-app release settings. Returns empty defaults when the doc does not exist yet. */
export async function getMobileAppSettings(): Promise<MobileAppSettings> {
    const snap = await getDoc(settingsRef());
    return toSettings(snap.exists() ? snap.data() : undefined);
}

/** Live-subscribe to the release settings (used by the Mobile Clients badge). */
export function subscribeMobileAppSettings(
    onChange: (settings: MobileAppSettings) => void,
    onError?: (error: unknown) => void,
): () => void {
    return onSnapshot(
        settingsRef(),
        (snap) => onChange(toSettings(snap.exists() ? snap.data() : undefined)),
        (error) => onError?.(error),
    );
}

/**
 * Save the admin-editable fields.
 *
 * `setDoc(..., { merge: true })` rather than `updateDoc`: the doc does not exist in a fresh project
 * and `updateDoc` would throw. merge also leaves the script-owned build metadata untouched.
 */
export async function updateMobileAppSettings(
    values: MobileAppSettingsFormValues,
    actor: string,
    previousMinAllowedVersion: string,
): Promise<void> {
    const minChanged = values.minAllowedVersion !== previousMinAllowedVersion;
    await setDoc(
        settingsRef(),
        stripUndefined({
            minAllowedVersion: values.minAllowedVersion,
            apkDownloadUrl: values.apkDownloadUrl,
            releaseNotes: values.releaseNotes,
            // Only stamp the audit fields when the floor actually moved, so editing a URL does not
            // rewrite the history of who last changed what blocks the fleet.
            ...(minChanged
                ? { minAllowedVersionSetAt: serverTimestamp(), minAllowedVersionSetBy: actor }
                : {}),
        }),
        { merge: true },
    );
}

/**
 * Raise the floor to the published version — this is what actually locks older builds out.
 *
 * Writes nothing but the floor and its audit stamp. The caller is responsible for checking that a
 * download URL exists first; forcing without one leaves drivers on a dialog with no button.
 */
export async function forceUpdateToLatest(latestVersion: string, actor: string): Promise<void> {
    await setDoc(
        settingsRef(),
        {
            minAllowedVersion: latestVersion,
            minAllowedVersionSetAt: serverTimestamp(),
            minAllowedVersionSetBy: actor,
        },
        { merge: true },
    );
}
