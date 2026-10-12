"use client";

import { useMemo } from "react";
import { MapPin } from "lucide-react";

import { FilterCombobox, type FilterComboboxOption } from "@/components/filter-combobox";
import { useLanguage } from "@/context/language";
import { selectHubFilterOptions, type HubRow } from "../api/selectors";

interface HubFilterComboboxProps {
    /** The page's `useHubs(selectHubRows)` rows: every hub, uncapped. */
    hubs?: readonly HubRow[];
    /** The options as built already (the Go boards: every hub of `['hubs',{view:'go'}]`, value = the hub code). */
    options?: FilterComboboxOption[];
    /** A hub code, or `"all"`. */
    value: string;
    onChange: (value: string) => void;
    /** The page's "every hub" text (each board words it differently). */
    allLabel: string;
    className?: string;
}

/**
 * The source-hub filter of the task boards (first-mile, line-haul, job-assign; TW8): a searchable
 * combobox over every cached hub, by name or code, instead of a Radix select that listed only the first
 * 50 (Appendix E §E.10 row 7). Long lists are virtualised by `FilterCombobox`.
 */
export function HubFilterCombobox({ hubs, options: given, value, onChange, allLabel, className }: HubFilterComboboxProps) {
    const { t } = useLanguage();
    const options = useMemo(() => given ?? selectHubFilterOptions(hubs ?? []), [given, hubs]);
    return (
        <FilterCombobox
            options={options}
            value={value}
            onChange={onChange}
            allValue="all"
            allLabel={allLabel}
            searchPlaceholder={t("masterData.hubFilter.search")}
            noResultsLabel={t("masterData.hubFilter.noResults")}
            clearLabel={t("masterData.hubFilter.clear")}
            icon={<MapPin className="h-4 w-4 shrink-0 opacity-50" />}
            className={className}
        />
    );
}
