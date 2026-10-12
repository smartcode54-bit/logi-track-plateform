/**
 * The Firestore source of `['badges']` while the `dashboard` web flag is off (TW4 interim, Appendix E
 * §E.5 row 19): one `getCountFromServer` on `waitlist` per fetch for a `waitlist:view` principal, no
 * listener. `badges.ts` imports this module only when the flag sends a fetch here, so the shell's chunk
 * (the sidebar mounts `useBadges` on every /app route) carries no Firestore code. Deleted by TW7.
 */
import type { QueryFunctionContext } from "@tanstack/react-query";
import { collection, getCountFromServer } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { hasCapability, meQueryOptions } from "@/features/auth/api/me";
import type { BadgesDTO } from "./badges";

/** One count query for the waitlist; every other counter is Go's (`GET /v1/badges`). */
export async function fetchBadgesFromFirestore({ client }: Pick<QueryFunctionContext, "client">): Promise<BadgesDTO> {
    const me = await client.ensureQueryData(meQueryOptions);
    const badges: BadgesDTO = {};
    if (hasCapability(me, "waitlist:view")) {
        const snapshot = await getCountFromServer(collection(db, COLLECTIONS.WAITLIST));
        badges.waitlist = snapshot.data().count;
    }
    return badges;
}
