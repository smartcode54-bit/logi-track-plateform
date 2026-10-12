"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { companiesQueryOptions, ownerCompanyQueryOptions, saveCompany, type CompanyDTO } from "./companies";

const NO_COMPANIES: CompanyDTO[] = [];

/** `['companies',{view:'all'}]`: the companies page. */
export function useCompanies() {
    const query = useQuery(companiesQueryOptions);
    return { ...query, companies: query.data ?? NO_COMPANIES };
}

/**
 * `['companies',{owner:true}]`. The profile form passes `fresh`: it refetches on mount instead of
 * starting from a copy up to 10 minutes old, because it writes the record back.
 */
export function useOwnerCompany(options: { fresh?: boolean } = {}) {
    return useQuery({ ...ownerCompanyQueryOptions, ...(options.fresh ? { refetchOnMount: "always" as const } : {}) });
}

/** Create or update a company (with its staged assets); makes every `['companies', …]` entry stale. */
export function useSaveCompany() {
    return useMutation({ mutationFn: saveCompany, meta: { invalidates: [queryKeys.companies.all()] } });
}
