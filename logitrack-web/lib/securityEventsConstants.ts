/** Values stored in `security_events.type` (extend as new writers are added). */
export const SECURITY_EVENT_TYPES = [
    "role_matrix_saved",
    "user_role_changed",
    "user_created",
    "user_sessions_revoked",
] as const;

export type SecurityEventType = (typeof SECURITY_EVENT_TYPES)[number];

export function isSecurityEventType(v: string): v is SecurityEventType {
    return (SECURITY_EVENT_TYPES as readonly string[]).includes(v);
}
