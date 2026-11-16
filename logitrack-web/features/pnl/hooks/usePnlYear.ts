"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { computePnlYear, type PnlYear } from "@/lib/pnl/year";
import { fetchPnlYearSources, type PnlMonthSources } from "../api/pnlSources";

/** January → `lastMonth` of `year`, each month computed live like the month view. */
export function usePnlYear(year: number, lastMonth: number) {
    const [sources, setSources] = useState<PnlMonthSources | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    const reload = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            setSources(await fetchPnlYearSources(year, lastMonth));
        } catch (e) {
            console.error("P&L year load failed", e);
            setSources(null);
            setError(e instanceof Error ? e : new Error(String(e)));
        } finally {
            setLoading(false);
        }
    }, [year, lastMonth]);

    useEffect(() => {
        void reload();
    }, [reload]);

    const result: PnlYear | null = useMemo(
        () => (sources ? computePnlYear({ ...sources.inputs, now: new Date() }, lastMonth) : null),
        [sources, lastMonth],
    );

    return { result, loading, error, reload };
}
