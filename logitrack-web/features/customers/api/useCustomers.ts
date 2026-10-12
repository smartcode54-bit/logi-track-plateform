"use client";

import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import {
    createCustomer,
    customerOptionsQuery,
    customerQuery,
    customersListQuery,
    updateCustomer,
    type CustomerListFilter,
    type CustomerMinimalDTO,
} from "./customers";

const NO_OPTIONS: CustomerMinimalDTO[] = [];

/** `['customers', {q}]` (infinite): the customers page. */
export function useCustomerPages(filter: CustomerListFilter) {
    return useInfiniteQuery(customersListQuery(filter));
}

/** `['customers','detail',id]`. */
export function useCustomer(id: string) {
    return useQuery(customerQuery(id));
}

/** `['customers','options']`: every customer for a master-data picker (one request per stale time). */
export function useCustomerOptions(enabled = true) {
    const query = useQuery({ ...customerOptionsQuery, enabled });
    return { ...query, options: query.data ?? NO_OPTIONS };
}

/** Create and update; both make every `['customers', …]` entry stale. */
export function useSaveCustomer() {
    const meta = { invalidates: [queryKeys.customers.all()] };
    return {
        create: useMutation({ mutationFn: createCustomer, meta }),
        update: useMutation({ mutationFn: updateCustomer, meta }),
    };
}
