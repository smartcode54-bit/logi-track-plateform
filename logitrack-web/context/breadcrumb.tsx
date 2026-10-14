"use client";

import { createContext, useContext, useState, ReactNode } from "react";

type BreadcrumbContextType = {
    customLastItem: string | null;
    setCustomLastItem: (value: string | null) => void;
};

const BreadcrumbContext = createContext<BreadcrumbContextType | undefined>(undefined);

/** Outside the shell (a page rendered on its own, as in a component test) there is no crumb to name: a no-op. */
const NO_BREADCRUMB: BreadcrumbContextType = { customLastItem: null, setCustomLastItem: () => {} };

export function BreadcrumbProvider({ children }: { children: ReactNode }) {
    const [customLastItem, setCustomLastItem] = useState<string | null>(null);

    return (
        <BreadcrumbContext.Provider value={{ customLastItem, setCustomLastItem }}>
            {children}
        </BreadcrumbContext.Provider>
    );
}

/**
 * The header breadcrumb's last item (the app shell, app/app/layout.tsx). A page names itself through
 * `components/page-header.tsx` (`PageHeader`'s `recordName`, `usePageCrumb`) rather than calling this directly.
 */
export function useBreadcrumb(): BreadcrumbContextType {
    return useContext(BreadcrumbContext) ?? NO_BREADCRUMB;
}
