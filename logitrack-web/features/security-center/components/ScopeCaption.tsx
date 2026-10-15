"use client";

import { useLanguage } from "@/context/language";
import { useMe } from "@/features/auth/api/useMe";
import type { SecurityReach } from "../api/reach";

/** Describes the selected query's existing backend reach; never selects or filters rows. */
export function ScopeCaption({ reach, organisationName, resource = "organisation" }: {
    reach: SecurityReach | undefined;
    organisationName?: string;
    resource?: "organisation" | "users" | "installations" | "overview";
}) {
    const { t } = useLanguage();
    const me = useMe().data;
    if (reach === undefined) return null;
    const organisation = organisationName
        ? t("securityCenter.ui4.scopeOrganisation", { name: organisationName })
        : t("securityCenter.overview.scopeTenant");
    const devices = me?.tenant?.kind === "own_fleet" && me.tenant.role !== "driver"
        ? t("securityCenter.ui4.scopeLinkedCarriers", { scope: organisation })
        : organisation;
    const users = me?.steward
        ? t("securityCenter.ui4.scopeMembershipless", { scope: devices })
        : devices;
    const scope = resource === "users" ? users : resource === "installations" ? devices : resource === "overview"
        ? t("securityCenter.ui4.scopeOverview", { organisation, users, devices })
        : organisation;
    return (
        <span className="mt-1 block text-xs" data-testid="scope-caption">
            {reach === "all" ? t("securityCenter.overview.scopeAll") : scope}
        </span>
    );
}
