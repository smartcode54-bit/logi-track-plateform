import React, { useEffect } from "react";
import { useLanguage } from "@/context/language";
import type { Language } from "@/context/locales/load";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

function InLanguage({ language, children }: { language: Language; children: React.ReactNode }) {
    const { language: active, setLanguage } = useLanguage();
    useEffect(() => setLanguage(language), [language, setLanguage]);
    return active === language ? children : null;
}

export function renderInLanguage(ui: React.ReactElement, language: Language) {
    return renderWithProviders(<InLanguage language={language}>{ui}</InLanguage>);
}
