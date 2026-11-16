"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { computePnl, type PnlResult } from "@/lib/pnlCompute";
import { fetchPnlMonthSources, type PnlMonthSources } from "../api/pnlSources";

/** Load one P&L month and compute it live (ADR 0031 §9 — nothing is stored). */
export function usePnlMonth(year: number, month: number) {
    const [sources, setSources] = useState<PnlMonthSources | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    const reload = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            setSources(await fetchPnlMonthSources(year, month));
        } catch (e) {
            console.error("P&L load failed", e);
            setSources(null);
            setError(e instanceof Error ? e : new Error(String(e)));
        } finally {
            setLoading(false);
        }
    }, [year, month]);

    useEffect(() => {
        void reload();
    }, [reload]);

    const result: PnlResult | null = useMemo(
        () => (sources ? computePnl({ ...sources.inputs, now: new Date() }) : null),
        [sources],
    );

    return { result, sources, loading, error, reload };
}
