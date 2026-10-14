import { PublicPageTitle } from "@/components/public-page-title";
import { publicPageMetadata } from "@/lib/publicPageMetadata";

/** The tab title of /forgot-password (UI3E): English from the server, the active language once the page runs. */
export const metadata = publicPageMetadata("/forgot-password");

export default function ForgotPasswordLayout({ children }: { children: React.ReactNode }) {
    return (
        <>
            <PublicPageTitle page="/forgot-password" />
            {children}
        </>
    );
}
