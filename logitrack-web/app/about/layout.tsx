import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /about (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/about");

export default function AboutLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/about" />
            {children}
        </>
    );
}
