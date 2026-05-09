import { useAuth } from "@/context/auth";

/**
 * Hook to get the customer scope ID for customer-role users.
 * Returns { customerScopeId, isCustomer }
 */
export function useCustomerScope() {
  const auth = useAuth();

  const isCustomer = auth?.customClaims?.role === "customer";
  const customerScopeId =
    typeof auth?.customClaims?.customerScopeId === "string"
      ? auth.customClaims.customerScopeId
      : null;

  return { customerScopeId, isCustomer };
}
