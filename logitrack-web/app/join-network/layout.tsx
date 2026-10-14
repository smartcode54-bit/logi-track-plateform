import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /join-network (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/join-network");

export default function JoinNetworkLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/join-network" />
            {children}
        </>
    );
}
