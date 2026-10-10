"use client";

import { useAuth } from "@/context/auth";
import { isCustomerPrincipal, type MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";

const selectCustomer = (me: MeDTO | null) => isCustomerPrincipal(me);

/**
 * The customer scope of a customer principal (developer-spec.md §10.6): a selector over `['me']`.
 *
 * - `isCustomer`: a customer-scope principal without a tenant membership (the legacy `customer`
 *   role, Appendix C §C.1.6), from `['me']`.
 * - `customerScopeId`: the legacy Firestore customer id that Firestore-backed pages filter by. Go
 *   knows the scope by its billing party; the legacy id travels only in the Firebase bridge token
 *   (Appendix C §C.6.3), so it is read from there until the page's domain moves to Go, where scope
 *   is enforced by the server (RLS) and this id is no longer needed.
 */
export function useCustomerScope() {
    const auth = useAuth();
    const { data: isCustomer = false } = useMe(selectCustomer);
    const scopeId = auth?.customClaims?.customerScopeId;
    const customerScopeId = isCustomer && typeof scopeId === "string" ? scopeId : null;
    return { customerScopeId, isCustomer };
}
