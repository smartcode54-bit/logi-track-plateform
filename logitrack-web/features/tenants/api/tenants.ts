/**
 * Tenants administration for platform admins (T18 owner addition, 2026-10-10: multi-tenant from the
 * first release; Appendix B §B.2.4, Appendix C §C.8). `GET/POST /v1/tenants`, `PATCH /v1/tenants/{id}`
 * (`platform:manage_tenants`), and the tenant admins through `GET /v1/tenants/{id}/members` and
 * `PUT/DELETE /v1/tenants/{id}/members/{userId}`. A new admin is created with
 * `POST /v1/users {role: "tenant_admin", tenantId}` (features/users/api/users.ts).
 *
 * Only carrier tenants are created here: the own-fleet row comes from the seed or the ETL and the
 * quarantine row from migration 0002 (R56). Go writes `tenant_created` / `tenant_updated` and the
 * outbox events in the same transaction.
 */
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";

import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { fetchAllPages } from "@/lib/goPages";

export type TenantKind = "own_fleet" | "carrier" | "quarantine";
export type TenantStatus = "active" | "pending" | "suspended";
export type LegalType = "individual" | "company";

export const TENANT_STATUSES: TenantStatus[] = ["active", "pending", "suspended"];
export const TENANT_KINDS: TenantKind[] = ["own_fleet", "carrier", "quarantine"];

/** A tenant of `GET /v1/tenants` (list item; the full profile is `GET /v1/tenants/{id}`). */
export interface TenantDTO {
    id: string;
    kind: TenantKind;
    code: string | null;
    nameTh: string;
    nameEn: string | null;
    status: TenantStatus;
    legalType: LegalType | null;
    contractorTenantId: string | null;
    createdAt: string;
}

/** One row of `GET /v1/tenants/{id}/members`. */
export interface TenantMemberDTO {
    user: { id: string; email: string | null; displayName: string | null };
    role: string;
    status: string;
    lastLoginAt: string | null;
}

export interface CreateTenantInput {
    code: string;
    nameTh: string;
    nameEn?: string;
    legalType: LegalType;
}

export interface UpdateTenantInput {
    nameTh?: string;
    nameEn?: string | null;
    status?: TenantStatus;
}

export interface TenantsFilter {
    kind: string;
    status: string;
}

export const TENANTS_KEY = ["tenants"] as const;
export const TENANTS_PATH = "/v1/tenants";

export function tenantsQueryKey(filter: TenantsFilter) {
    return ["tenants", { kind: filter.kind, status: filter.status }] as const;
}

type TenantsKey = ReturnType<typeof tenantsQueryKey>;

/** `['tenants', {kind, status}]`: the keyset list (focus refetch; no tenant event, Appendix B §B.4.3). */
export function useTenants(filter: TenantsFilter, enabled = true) {
    return useInfiniteQuery({
        queryKey: tenantsQueryKey(filter),
        queryFn: goInfiniteQueryFn<TenantDTO, TenantsKey>(TENANTS_PATH, {
            query: ([, f]) => ({ kind: f.kind || undefined, status: f.status || undefined, limit: 50 }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: 60_000,
        enabled,
    });
}

/** Every tenant (for pickers of platform admins: the tenant of a new user, a membership to add). */
export function useAllTenants(enabled: boolean) {
    return useQuery({
        queryKey: ["tenants", { all: true }] as const,
        queryFn: ({ signal }) => fetchAllPages<TenantDTO>(TENANTS_PATH, {}, signal),
        staleTime: 5 * 60_000,
        enabled,
    });
}

export function tenantMembersKey(tenantId: string, role: string) {
    return ["tenants", tenantId, "members", { role }] as const;
}

/** `['tenants', id, 'members', {role}]`: the members of one tenant (all pages). */
export function useTenantMembers(tenantId: string | null, role: string) {
    return useQuery({
        queryKey: tenantMembersKey(tenantId ?? "", role),
        queryFn: ({ signal }) =>
            fetchAllPages<TenantMemberDTO>(goPath`/v1/tenants/${tenantId ?? ""}/members`, role ? { role } : {}, signal),
        enabled: Boolean(tenantId),
        staleTime: 30_000,
    });
}

export function useInvalidateTenants() {
    const client = useQueryClient();
    return () => client.invalidateQueries({ queryKey: TENANTS_KEY });
}

/** `POST /v1/tenants {kind: "carrier", code, nameTh, nameEn?, legalType}`. */
export function createTenant(input: CreateTenantInput): Promise<TenantDTO> {
    const body: Record<string, unknown> = { kind: "carrier", code: input.code.trim(), nameTh: input.nameTh.trim(), legalType: input.legalType };
    const nameEn = input.nameEn?.trim();
    if (nameEn) body.nameEn = nameEn;
    return goFetch<TenantDTO>(TENANTS_PATH, { method: "POST", body });
}

/** `PATCH /v1/tenants/{id}`: names and status (the structural `code`, `kind`, `contractorTenantId` stay unedited here). */
export function updateTenant(id: string, input: UpdateTenantInput): Promise<TenantDTO> {
    const body: Record<string, unknown> = {};
    if (input.nameTh !== undefined) body.nameTh = input.nameTh.trim();
    if (input.nameEn !== undefined) body.nameEn = input.nameEn === null || input.nameEn.trim() === "" ? null : input.nameEn.trim();
    if (input.status !== undefined) body.status = input.status;
    return goFetch<TenantDTO>(goPath`/v1/tenants/${id}`, { method: "PATCH", body });
}
