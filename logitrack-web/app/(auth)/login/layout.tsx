import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /login (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/login");

export default function LoginLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/login" />
            {children}
        </>
    );
}
