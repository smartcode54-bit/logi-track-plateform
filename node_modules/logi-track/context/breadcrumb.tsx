"use client";

import { createContext, useContext, useState, ReactNode } from "react";

type BreadcrumbContextType = {
    customLastItem: string | null;
    setCustomLastItem: (value: string | null) => void;
};

const BreadcrumbContext = createContext<BreadcrumbContextType | undefined>(undefined);

export function BreadcrumbProvider({ children }: { children: ReactNode }) {
    const [customLastItem, setCustomLastItem] = useState<string | null>(null);

    return (
        <BreadcrumbContext.Provider value={{ customLastItem, setCustomLastItem }}>
            {children}
        </BreadcrumbContext.Provider>
    );
}

export function useBreadcrumb() {
    const context = useContext(BreadcrumbContext);
    if (!context) {
        throw new Error("useBreadcrumb must be used within a BreadcrumbProvider");
    }
    return context;
}
