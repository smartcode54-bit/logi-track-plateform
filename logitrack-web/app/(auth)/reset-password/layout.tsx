import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /reset-password (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/reset-password");

export default function ResetPasswordLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/reset-password" />
            {children}
        </>
    );
}
