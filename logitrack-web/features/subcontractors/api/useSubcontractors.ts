"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import {
    createSubcontractor,
    invalidateSubcontractors,
    suspendSubcontractor,
    subcontractorOptionsQuery,
    subcontractorQuery,
    subcontractorsListQuery,
    updateSubcontractor,
    uploadSubcontractorDocument,
    type SubcontractorOptionDTO,
    type SubcontractorSummaryDTO,
} from "./subcontractors";

const NO_ROWS: SubcontractorSummaryDTO[] = [];
const NO_OPTIONS: SubcontractorOptionDTO[] = [];

export function useSuspendSubcontractor() {
    const client = useQueryClient();
    return useMutation({
        mutationFn: suspendSubcontractor,
        retry: false,
        onSuccess: async () => {
            await Promise.all([
                invalidateSubcontractors(client),
                client.invalidateQueries({ queryKey: queryKeys.tenants.all() }),
            ]);
        },
    });
}

/** `['subcontractors', {view:'full'}]`: the list page. */
export function useSubcontractorList() {
    const query = useQuery(subcontractorsListQuery);
    return { ...query, rows: query.data ?? NO_ROWS };
}

/** `['subcontractors','options']`: every carrier for a picker (10 min, one request for all forms). */
export function useSubcontractorOptions(enabled = true) {
    const query = useQuery({ ...subcontractorOptionsQuery, enabled });
    return { ...query, options: query.data ?? NO_OPTIONS };
}

/** `['subcontractors','detail',id]`. */
export function useSubcontractor(id: string) {
    return useQuery(subcontractorQuery(id));
}

/** Create, update and document upload; writes make every `['subcontractors', …]` entry stale. */
export function useSaveSubcontractor() {
    const meta = { invalidates: [queryKeys.subcontractors.all()] };
    return {
        create: useMutation({ mutationFn: createSubcontractor, meta }),
        update: useMutation({ mutationFn: updateSubcontractor, meta }),
        upload: useMutation({ mutationFn: uploadSubcontractorDocument }),
    };
}
