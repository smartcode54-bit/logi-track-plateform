"use client";

import { useMemo } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { pollWhileVisible } from "@/lib/realtime/polling";
import {
    createDriver,
    createDriverLogin,
    driverOptionsQuery,
    driverPagesQuery,
    driverQuery,
    updateDriver,
    type DriverDTO,
    type DriverListFilter,
    type DriverOptionDTO,
    type StatusCounts,
} from "./drivers";

const NO_DRIVERS: DriverDTO[] = [];
const NO_OPTIONS: DriverOptionDTO[] = [];

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

/** `['drivers','options']`: drivers for a picker. */
export function useDriverOptions(enabled = true) {
    const query = useQuery({ ...driverOptionsQuery, enabled });
    return { ...query, options: query.data ?? NO_OPTIONS };
}

/** `['drivers','detail',id]`. */
export function useDriver(id: string) {
    return useQuery(driverQuery(id));
}

/** Profile create and update (both make `['drivers']` stale) and the login creation. */
export function useDriverWrites() {
    const meta = { invalidates: [queryKeys.drivers.all()] };
    return {
        create: useMutation({ mutationFn: createDriver, meta }),
        update: useMutation({ mutationFn: updateDriver, meta }),
        createLogin: useMutation({ mutationFn: createDriverLogin, meta: { invalidates: [queryKeys.drivers.all(), queryKeys.users.all()] } }),
    };
}
