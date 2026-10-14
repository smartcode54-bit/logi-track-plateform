import { cookies } from "next/headers";

import { LandingPage } from "@/components/landing/LandingPage";
import { ACCESS_COOKIE } from "@/lib/bff/session";

/**
 * `/` (UI3C, #143): the landing page for a signed-out visitor, the console for a signed-in one. Whether the request
 * carries the `lt_at` session cookie (path `/`, HttpOnly, so only the server sees it) decides what shows while
 * `['me']` loads: the "opening your console" state, or the landing at once. Only the cookie's presence is read here;
 * `['me']` decides, and the edge gate verifies the session on `/app`.
 */
export default async function Home() {
    const sessionHint = (await cookies()).has(ACCESS_COOKIE);
    return <LandingPage sessionHint={sessionHint} />;
}
