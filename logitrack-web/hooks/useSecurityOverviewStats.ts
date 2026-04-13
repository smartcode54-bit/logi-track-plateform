"use client";

import { useEffect, useState } from "react";
import { useAuth } from "@/context/auth";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import {
    fetchMobileInstallationCount,
    fetchUserRoleCounts,
    type UserRoleCounts,
} from "@/lib/fetchSecurityOverviewStats";

export type SecurityOverviewDistribution = {
    drivers: number;
    operationsRoles: number;
    others: number;
    totalUsers: number;
};

function buildDistribution(counts: UserRoleCounts): SecurityOverviewDistribution {
    const drivers = counts.drivers;
    const operationsRoles = counts.managers + counts.operationStaff + counts.operators;
    const others = counts.admins + counts.subcontractors + counts.customers;
    return {
        drivers,
        operationsRoles,
        others,
        totalUsers: drivers + operationsRoles + others,
    };
}

export function useSecurityOverviewStats() {
    const auth = useAuth();
    const authLoading = auth?.loading ?? true;
    const currentUser = auth?.currentUser ?? null;
    const claims = auth?.customClaims as Record<string, unknown> | null | undefined;
    const isAdmin = claims?.admin === true;
    const role = typeof claims?.role === "string" ? claims.role : "";
    const partnerScopeId = typeof claims?.partnerScopeId === "string" ? claims.partnerScopeId.trim() : "";

    const { hasPermission: canViewMobileClients, loading: mobilePermLoading } = usePermission(
        CAPABILITIES.security_view_mobile_clients,
    );

    const [userStatsLoading, setUserStatsLoading] = useState(true);
    const [mobileCountLoading, setMobileCountLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [userCounts, setUserCounts] = useState<UserRoleCounts | null>(null);
    const [distribution, setDistribution] = useState<SecurityOverviewDistribution | null>(null);
    const [mobileInstallCount, setMobileInstallCount] = useState<number | null>(null);

    useEffect(() => {
        if (authLoading) return;

        if (!currentUser) {
            setUserStatsLoading(false);
            setUserCounts(null);
            setDistribution(null);
            return;
        }

        let cancelled = false;

        async function run() {
            setUserStatsLoading(true);
            setError(null);
            try {
                if (isAdmin) {
                    const uc = await fetchUserRoleCounts();
                    if (!cancelled) {
                        setUserCounts(uc);
                        setDistribution(buildDistribution(uc));
                    }
                } else if (!cancelled) {
                    setUserCounts(null);
                    setDistribution(null);
                }
            } catch (e) {
                console.error("[useSecurityOverviewStats] user counts", e);
                if (!cancelled) {
                    setError((e as Error).message || "fetch failed");
                    setUserCounts(null);
                    setDistribution(null);
                }
            } finally {
                if (!cancelled) setUserStatsLoading(false);
            }
        }

        void run();
        return () => {
            cancelled = true;
        };
    }, [authLoading, currentUser, isAdmin]);

    useEffect(() => {
        if (authLoading || !currentUser) {
            if (!authLoading && !currentUser) {
                setMobileCountLoading(false);
                setMobileInstallCount(null);
            }
            return;
        }

        if (mobilePermLoading) return;

        let cancelled = false;

        async function run() {
            setMobileCountLoading(true);
            try {
                if (canViewMobileClients) {
                    const n = await fetchMobileInstallationCount({ isAdmin, role, partnerScopeId });
                    if (!cancelled) setMobileInstallCount(n);
                } else if (!cancelled) {
                    setMobileInstallCount(null);
                }
            } catch (e) {
                console.error("[useSecurityOverviewStats] mobile count", e);
                if (!cancelled) {
                    setMobileInstallCount(null);
                    setError((e as Error).message || "fetch failed");
                }
            } finally {
                if (!cancelled) setMobileCountLoading(false);
            }
        }

        void run();
        return () => {
            cancelled = true;
        };
    }, [authLoading, currentUser, isAdmin, role, partnerScopeId, canViewMobileClients, mobilePermLoading]);

    return {
        loadingUserStats: authLoading || userStatsLoading,
        loadingMobileCount: authLoading || mobilePermLoading || mobileCountLoading,
        error,
        isAdmin,
        userCounts,
        distribution,
        mobileInstallCount,
        canViewMobileClients,
    };
}
