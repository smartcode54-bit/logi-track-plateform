"use client";

import { useLanguage } from "@/context/language";
import { useMe } from "../api/useMe";
import { principalRoleLabel } from "../utils/principalLabel";

/**
 * The scope line of the signed-in principal in the active language ("WRT · Tenant admin", "CJSF · Customer view",
 * "Platform admin"; UI3, issue #143): a selector over `['me']` with no read of its own. "" while signed out.
 */
export function useScopeLabel(): string {
    const { t, language } = useLanguage();
    const { data: me } = useMe();
    return principalRoleLabel(me ?? null, t, language);
}
