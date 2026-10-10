"use client";

import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { customersQueryOptions, type CustomerData } from "./customers";

const NO_CUSTOMERS: CustomerData[] = [];

/** `['customers']`: every customer, one cached list for the tab (10 min stale). */
export function useCustomers(): UseQueryResult<CustomerData[]>;
export function useCustomers<T>(select: (customers: CustomerData[]) => T): UseQueryResult<T>;
export function useCustomers<T>(select?: (customers: CustomerData[]) => T): UseQueryResult<T | CustomerData[]> {
    return useQuery({ ...customersQueryOptions, select });
}

/** The list itself, empty until loaded (for pickers that render nothing while loading). */
export function useCustomerList(): CustomerData[] {
    return useCustomers().data ?? NO_CUSTOMERS;
}
