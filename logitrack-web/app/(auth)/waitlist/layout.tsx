import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /waitlist (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/waitlist");

export default function WaitlistLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/waitlist" />
            {children}
        </>
    );
}
