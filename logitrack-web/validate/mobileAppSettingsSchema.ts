import { z } from "zod";

/**
 * The admin-editable half of Firestore `settings/mobile_app`.
 *
 * Deliberately narrow: the release script owns `latestVersion`, `apkSha256`, `releasedAt` and the
 * rest of the build metadata, and this form must never be able to write them. If those were fields
 * here, saving a stale form would clobber what a build published. See
 * shared-docs/adr/0007-mobile-forced-update-pipeline.md.
 */
export const MobileAppSettingsFormSchema = z.object({
    /**
     * The floor that actually blocks the driver app. Editable directly (not only via the force
     * button) so the lever works both ways — lowering it un-blocks drivers without a rebuild.
     */
    minAllowedVersion: z
        .string()
        .regex(/^\d+\.\d+\.\d+$/, "Enter a version like 1.2.3")
        .or(z.literal("")),
    /** Link shown in the force-update dialog. Normally written by the release script. */
    apkDownloadUrl: z
        .string()
        .url("Enter a valid https:// URL")
        .startsWith("https://", "Enter a valid https:// URL")
        .or(z.literal("")),
    releaseNotes: z.string().optional(),
});

export type MobileAppSettingsFormValues = z.infer<typeof MobileAppSettingsFormSchema>;

/** The full doc as read back, including the script-owned fields the UI shows read-only. */
export interface MobileAppSettings extends MobileAppSettingsFormValues {
    minAllowedVersionSetAt?: Date | null;
    minAllowedVersionSetBy?: string;
    latestVersion?: string;
    latestBuildNumber?: string;
    apkSizeBytes?: number;
    apkSha256?: string;
    flavor?: string;
    releasedAt?: Date | null;
    releasedBy?: string;
}
