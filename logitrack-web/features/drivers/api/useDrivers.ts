"use client";

import { useCallback, useMemo, useState } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { pollWhileVisible } from "@/lib/realtime/polling";
import {
    createDriver,
    createDriverLogin,
    driverPagesQuery,
    driverQuery,
    driverStatusCountsQuery,
    updateDriver,
    type DriverDTO,
    type DriverListFilter,
    type DriverLoginInput,
    type StatusCounts,
} from "./drivers";
import type { CreateUserResult } from "@/features/users/api/users";
import { PartialCreateError } from "@/lib/goUpload";

const NO_DRIVERS: DriverDTO[] = [];
const NO_COUNTS: StatusCounts = {};

/**
 * `['drivers', {status, q}]` (infinite): keyset pages of 100, polled every 60 s while the tab is
 * visible and on focus once older than that (Appendix E §E.5 row 23, replacing the `onSnapshot`
 * listener capped at 100 rows). `statusCounts` is the server's count over every matching driver.
 */
export function useDriverPages(filter: DriverListFilter) {
    const query = useInfiniteQuery({ ...driverPagesQuery(filter), placeholderData: keepPreviousData, ...pollWhileVisible() });
    const drivers = useMemo(() => query.data?.pages.flatMap((p) => p.data ?? []) ?? NO_DRIVERS, [query.data]);
    const statusCounts: StatusCounts = query.data?.pages[0]?.meta?.statusCounts ?? {};
    return { ...query, drivers, statusCounts };
}

/** `['drivers',{view:'statusCounts'}]`: drivers per status (counters; pickers search on the server). */
export function useDriverStatusCounts(enabled = true) {
    const query = useQuery({ ...driverStatusCountsQuery, enabled });
    return { ...query, counts: query.data ?? NO_COUNTS };
}

/** `['drivers','detail',id]`. */
export function useDriver(id: string) {
    return useQuery(driverQuery(id));
}

/** Profile create and update (both make `['drivers']` stale). The login is `useCreateDriverLogin`. */
export function useDriverWrites() {
    const client = useQueryClient();
    const meta = { invalidates: [queryKeys.drivers.all()] };
    return {
        create: useMutation({ mutationFn: createDriver, meta, onError: (error) => {
            if (error instanceof PartialCreateError) void client.invalidateQueries({ queryKey: queryKeys.drivers.all() });
        } }),
        update: useMutation({ mutationFn: updateDriver, meta }),
    };
}

/**
 * A driver's login (`POST /v1/users {role:"driver", driverId, tenantId}`). Deliberately not a
 * `useMutation`: the answer holds the temporary password, shown once (R29), and a mutation keeps its
 * data in the tab's shared mutation cache for minutes after the form unmounts. As with the users page
 * (T19) and the API-key secret (T54S), the caller keeps the result in its own state only; this hook
 * only tracks the pending flag and makes `['drivers']` and `['users']` stale afterwards.
 */
export function useCreateDriverLogin() {
    const client = useQueryClient();
    const [isPending, setPending] = useState(false);
    const create = useCallback(
        async (input: DriverLoginInput): Promise<CreateUserResult> => {
            setPending(true);
            try {
                const result = await createDriverLogin(input);
                void client.invalidateQueries({ queryKey: queryKeys.drivers.all() });
                void client.invalidateQueries({ queryKey: queryKeys.users.all() });
                return result;
            } finally {
                setPending(false);
            }
        },
        [client]
    );
    return { create, isPending };
}
