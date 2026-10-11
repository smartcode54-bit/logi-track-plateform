/**
 * The role matrix over Go (T54S; T51 routes `GET`/`PUT /v1/roles/matrix`, Appendix B §B.2.6, Appendix C
 * §C.2.5; developer-spec.md §10.7, §10.13; Appendix E §E.2.4). One request loads every tenant role
 * with every key a tenant role can hold (no per-role `getDoc` loop, `roles/page.tsx:336-372`); a save
 * replaces the layer's override rows in one transaction, which also writes `role_matrix_saved` and the
 * realtime `roles.changed` (no `logSecurityEvent` callable). `role_capability_overrides` replaces
 * `permissions_config/{role}`, which Go never reads.
 *
 * Layers: the caller's effective tenant (default), another tenant (`?tenantId`, platform_admin), or the
 * platform-wide layer (`?layer=platform`, platform_admin). The key is `['roles','matrix',{layer,
 * tenantId}]`; a save invalidates `['roles']` and `['me']` (the caller's own capabilities may change).
 */
import { useQuery } from "@tanstack/react-query";

import { goFetchEnvelope } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const ROLE_MATRIX_PATH = "/v1/roles/matrix";

/** The tenant roles of the matrix, in catalog order (`authz.TenantRoles`). */
export const MATRIX_ROLES = ["tenant_admin", "manager", "operation_staff", "operator", "user", "driver"] as const;

/** One key of one role (Appendix C §C.2.5). */
export interface MatrixCellDTO {
    /** The catalog default. */
    default: boolean;
    /** What the role holds in this layer now. */
    effective: boolean;
    /** The layer holds a row for this cell. */
    overridden: boolean;
    /** The layer may hold a row for this cell. */
    overridable: boolean;
    /** Tenant layer only: the platform-wide row the tenant inherits. */
    platformOverride?: boolean;
}

export interface MatrixRoleDTO {
    role: string;
    /** False for `tenant_admin` (no tenant can lock itself out). */
    overridable: boolean;
    capabilities: Record<string, MatrixCellDTO>;
}

/** `GET /v1/roles/matrix` (Go `iam.Matrix`). */
export interface RoleMatrixDTO {
    layer: "tenant" | "platform";
    tenantId: string | null;
    tenantKind: string | null;
    roles: MatrixRoleDTO[];
}

/** Which layer a matrix request names. `{layer:"default"}` = the caller's effective tenant (or, for a
 * platform_admin without one, the platform-wide layer). */
export type MatrixTarget = { layer: "default" } | { layer: "platform" } | { layer: "tenant"; tenantId: string };

export const DEFAULT_MATRIX_TARGET: MatrixTarget = { layer: "default" };

export function roleMatrixKey(target: MatrixTarget) {
    return queryKeys.roles.matrix({ layer: target.layer, tenantId: target.layer === "tenant" ? target.tenantId : "" });
}

/** The query parameters of a matrix request for `target`. */
export function matrixQuery(target: MatrixTarget): Record<string, string | undefined> {
    if (target.layer === "platform") return { layer: "platform" };
    if (target.layer === "tenant") return { tenantId: target.tenantId };
    return {};
}

/** `['roles','matrix',{layer,tenantId}]` (10 min; `roles.changed` and the save invalidate it). */
export function useRoleMatrix(target: MatrixTarget, enabled = true) {
    return useQuery({
        queryKey: roleMatrixKey(target),
        queryFn: async ({ signal }) => (await goFetchEnvelope<RoleMatrixDTO>(ROLE_MATRIX_PATH, { signal, query: matrixQuery(target) })).data,
        ...QUERY_POLICY.masterData,
        enabled,
    });
}

/** One row of `PUT /v1/roles/matrix`. */
export interface MatrixOverride {
    role: string;
    capability: string;
    allowed: boolean;
}

/** Cell edits: `role -> capability -> desired value`. */
export type MatrixEdits = Record<string, Record<string, boolean>>;

/** The value a cell shows: the edit, else what the role holds now. */
export function cellValue(matrix: RoleMatrixDTO, edits: MatrixEdits, role: string, capability: string): boolean {
    const edited = edits[role]?.[capability];
    if (edited !== undefined) return edited;
    return matrix.roles.find((r) => r.role === role)?.capabilities[capability]?.effective ?? false;
}

/** The edits that differ from the loaded matrix (an edit back to the loaded value is no change). */
export function changedCells(matrix: RoleMatrixDTO, edits: MatrixEdits): MatrixOverride[] {
    const out: MatrixOverride[] = [];
    for (const r of matrix.roles) {
        for (const [capability, allowed] of Object.entries(edits[r.role] ?? {})) {
            const cell = r.capabilities[capability];
            if (cell?.overridable && cell.effective !== allowed) out.push({ role: r.role, capability, allowed });
        }
    }
    return out;
}

/**
 * The body of a save: the complete set of the layer's overrides afterwards (Go removes a row the body
 * leaves out). Every row the layer holds is repeated with its value (an edit included), and every edit
 * of a cell without a row is added; Go then drops a new row equal to the inherited value, so a cell
 * edited back to its default leaves no row behind. Only overridable cells are sent.
 */
export function matrixOverrides(matrix: RoleMatrixDTO, edits: MatrixEdits): MatrixOverride[] {
    const out: MatrixOverride[] = [];
    for (const r of matrix.roles) {
        for (const [capability, cell] of Object.entries(r.capabilities)) {
            if (!cell.overridable) continue;
            const edited = edits[r.role]?.[capability];
            if (cell.overridden) {
                out.push({ role: r.role, capability, allowed: edited ?? cell.effective });
            } else if (edited !== undefined && edited !== cell.effective) {
                out.push({ role: r.role, capability, allowed: edited });
            }
        }
    }
    return out;
}

export interface SaveMatrixResult {
    matrix: RoleMatrixDTO;
    /** Override rows the save changed (0: nothing written, no event). */
    changes: number;
}

/** `PUT /v1/roles/matrix` with the layer's complete override set. */
export async function saveRoleMatrix(target: MatrixTarget, overrides: MatrixOverride[]): Promise<SaveMatrixResult> {
    const env = await goFetchEnvelope<RoleMatrixDTO>(ROLE_MATRIX_PATH, {
        method: "PUT",
        query: matrixQuery(target),
        body: { overrides },
    });
    const changes = typeof env.meta?.changes === "number" ? env.meta.changes : 0;
    return { matrix: env.data, changes };
}
