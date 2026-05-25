import { useAuth } from "@/context/auth";

/**
 * Hook to get the company scope ID for subcontractor-role users.
 * Returns { companyId, isCompanyScoped }
 *
 * isCompanyScoped = true when the user is a subcontractor tenant with a companyId claim.
 * Admin / Manager users have companyId = null → isCompanyScoped = false → see all data.
 */
export function useCompanyScope() {
  const auth = useAuth();

  const companyId =
    typeof auth?.customClaims?.companyId === "string"
      ? auth.customClaims.companyId
      : null;

  // A user is "company scoped" only if they have a companyId claim AND are not admin/manager
  const role = auth?.customClaims?.role;
  const isAdminLike = role === "admin" || role === "manager";
  const isCompanyScoped = !!companyId && !isAdminLike;

  return { companyId, isCompanyScoped };
}
