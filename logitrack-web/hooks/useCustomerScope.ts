import { useAuth } from "@/context/auth";

/**
 * Hook to get the customer scope ID for customer-role users.
 * Returns { customerScopeId, isCustomer }
 */
export function useCustomerScope() {
  const { customClaims } = useAuth();

  const isCustomer = customClaims?.role === "customer";
  const customerScopeId =
    typeof customClaims?.customerScopeId === "string"
      ? customClaims.customerScopeId
      : null;

  return { customerScopeId, isCustomer };
}
