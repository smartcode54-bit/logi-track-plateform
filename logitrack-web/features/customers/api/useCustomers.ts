"use client";

import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { createCustomer, customerQuery, customersListQuery, updateCustomer, type CustomerListFilter } from "./customers";

/** `['customers', {q}]` (infinite): the customers page. */
export function useCustomerPages(filter: CustomerListFilter) {
    return useInfiniteQuery(customersListQuery(filter));
}

/** `['customers','detail',id]`. */
export function useCustomer(id: string) {
    return useQuery(customerQuery(id));
}

/** Create and update; both make every `['customers', …]` entry stale. */
export function useSaveCustomer() {
    const meta = { invalidates: [queryKeys.customers.all()] };
    return {
        create: useMutation({ mutationFn: createCustomer, meta }),
        update: useMutation({ mutationFn: updateCustomer, meta }),
    };
}
