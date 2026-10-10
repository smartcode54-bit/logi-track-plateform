"use client";

import { useQuery } from "@tanstack/react-query";
import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { badgesQueryOptions, type BadgesDTO } from "./badges";

const signedIn = (me: MeDTO | null) => me !== null;

/** `['badges']`, polled every 60 s while the tab is visible; idle while signed out. */
export function useBadges<T = BadgesDTO>(select?: (badges: BadgesDTO) => T) {
    const { data: enabled = false } = useMe(signedIn);
    return useQuery({ ...badgesQueryOptions, select, enabled });
}
