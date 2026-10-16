import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { z } from "zod";

import { ApiError } from "@/lib/apiError";
import { goFetch, goFetchEnvelope, goPath } from "@/lib/goFetch";
import { goNextPageParam } from "@/lib/goQuery";

const version = z.string().regex(/^(0|[1-9][0-9]*)$/);
const supportAccessSchema = z.object({
    enabled: z.boolean(),
    version,
    changedAt: z.string().datetime({ offset: true }).nullable(),
    changedBy: z.string().nullable(),
    eligibleToToggle: z.boolean(),
});
const memberSchema = z.object({
    user: z.object({ id: z.string(), displayName: z.string().nullable(), email: z.string().nullable() }),
    role: z.string(),
    status: z.string(),
    grantedByPlatform: z.boolean(),
    ratifiedAt: z.string().nullable(),
    grantVersion: version,
    eligibilityBasis: z.enum(["member", "ratified", "first_admin_self_chosen", "pending"]),
    eligibleToRatify: z.boolean(),
});

export type SupportAccessDTO = z.infer<typeof supportAccessSchema>;
export type SupportMemberDTO = z.infer<typeof memberSchema>;

function parse<T>(schema: z.ZodType<T>, data: unknown): T {
    const result = schema.safeParse(data);
    if (!result.success) throw new ApiError({ status: 200, code: "bad_response", message: "Invalid support access response" });
    return result.data;
}

export const supportAccessKey = (tenantId: string) => ["support-access", tenantId] as const;
export const supportStateKey = (tenantId: string) => [...supportAccessKey(tenantId), "state"] as const;

export function useSupportAccess(tenantId: string) {
    return useQuery({
        queryKey: supportStateKey(tenantId),
        queryFn: async ({ signal }) => parse(supportAccessSchema, await goFetch<unknown>(goPath`/v1/tenants/${tenantId}/support-access`, { signal })),
        staleTime: 0,
        retry: false,
    });
}

export function useSupportMembers(tenantId: string) {
    return useInfiniteQuery({
        queryKey: [...supportAccessKey(tenantId), "members"],
        queryFn: async ({ signal, pageParam }) => {
            const page = await goFetchEnvelope<unknown>(goPath`/v1/tenants/${tenantId}/members`, { signal, query: { limit: 100, cursor: pageParam } });
            return { ...page, data: parse(z.array(memberSchema), page.data) };
        },
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: 0,
        retry: false,
    });
}

export async function setSupportAccess(tenantId: string, enabled: boolean, expectedVersion: string): Promise<SupportAccessDTO> {
    return parse(supportAccessSchema, await goFetch<unknown>(goPath`/v1/tenants/${tenantId}/support-access`, {
        method: "PUT", body: { enabled, expectedVersion },
    }));
}

export async function ratifySupportMember(tenantId: string, userId: string, expectedGrantVersion: string): Promise<void> {
    await goFetch(goPath`/v1/tenants/${tenantId}/members/${userId}/ratify`, { method: "POST", body: { expectedGrantVersion } });
}

export function pendingRatification(member: SupportMemberDTO): boolean {
    return member.grantedByPlatform && member.ratifiedAt === null && member.eligibilityBasis !== "first_admin_self_chosen";
}
