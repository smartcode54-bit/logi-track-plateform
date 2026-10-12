"use client";

import { useInfiniteQuery, useMutation, useQuery, type UseQueryResult } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import {
    createCustomer,
    customerOptionsQuery,
    customerQuery,
    customersListQuery,
    customersQueryOptions,
    updateCustomer,
    type CustomerData,
    type CustomerListFilter,
    type CustomerMinimalDTO,
} from "./customers";

const NO_CUSTOMERS: CustomerData[] = [];
const NO_OPTIONS: CustomerMinimalDTO[] = [];

/** `['customers']`: the legacy Firestore list (task dialogs and other pages outside the master-data group). */
export function useCustomers(): UseQueryResult<CustomerData[]>;
export function useCustomers<T>(select: (customers: CustomerData[]) => T): UseQueryResult<T>;
export function useCustomers<T>(select?: (customers: CustomerData[]) => T): UseQueryResult<T | CustomerData[]> {
    return useQuery({ ...customersQueryOptions, select });
}

/** The legacy list itself, empty until loaded. */
export function useCustomerList(): CustomerData[] {
    return useCustomers().data ?? NO_CUSTOMERS;
}

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
