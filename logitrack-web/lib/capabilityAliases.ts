/**
 * Legacy capability ids that the Go catalog renamed (Appendix C §C.2.6). Every other value of
 * `CAPABILITIES` (lib/capabilities.ts) is already a catalog key (`features/auth/api/me.test.ts` checks
 * it), so pages keep passing `CAPABILITIES.x` to `usePermission` / `can` while the answer comes from
 * Go (`['me']`). Its own module so the providers do not carry the legacy capability table (TW4).
 */
export const LEGACY_CAPABILITY_ALIASES: Readonly<Record<string, string>> = {
    "security:manage_users": "users:manage",
};

/** The catalog key a legacy capability id stands for (unchanged for a catalog key). */
export function toCatalogKey(capability: string): string {
    return LEGACY_CAPABILITY_ALIASES[capability] ?? capability;
}
