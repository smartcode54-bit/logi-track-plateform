"use client";

import { useMemo } from "react";
import { Flag, MapPin } from "lucide-react";
import { useLanguage } from "@/context/language";
import { FilterCombobox, type FilterComboboxOption } from "@/components/filter-combobox";
import { PLACE_FILTER_ALL, PLACE_FILTER_NONE, type PlaceFilterOption } from "@/lib/placeFilter";

export interface PlaceFilterComboboxProps {
    options: PlaceFilterOption[];
    value: string;
    onChange: (value: string) => void;
    /** Which control this is — decides the icon and the "all" label. */
    kind: "origin" | "destination";
    /** i18n key prefix — "driverMonitor.filter". */
    keyPrefix: string;
    className?: string;
    /** Rendered above the control (the export dialog uses a label; the filter bar does not). */
    label?: string;
}

/**
 * Searchable origin / destination filter — place-specific labelling on top of `FilterCombobox`.
 *
 * Options are resolved place identities, so one hub is one option however it was spelled. Values
 * that match no hub/SOC master record keep their raw string and are badged, never merged away
 * (ADR 0006 §4) — which is what makes the dropdown double as a data-quality report.
 */
export function PlaceFilterCombobox({
    options,
    value,
    onChange,
    kind,
    keyPrefix,
    className,
    label,
}: PlaceFilterComboboxProps) {
    const { t } = useLanguage();

    const comboOptions = useMemo<FilterComboboxOption[]>(
        () =>
            options.map((o) => ({
                value: o.value,
                label:
                    o.value === PLACE_FILTER_NONE
                        ? t(`${keyPrefix}.placeNotSpecified` as never)
                        : o.label,
                count: o.count,
                badge: o.isUnresolved ? t(`${keyPrefix}.placeNotInMaster` as never) : undefined,
            })),
        [options, keyPrefix, t]
    );

    return (
        <FilterCombobox
            options={comboOptions}
            value={value}
            onChange={onChange}
            allValue={PLACE_FILTER_ALL}
            allLabel={t(
                (kind === "origin" ? `${keyPrefix}.allOrigins` : `${keyPrefix}.allDestinations`) as never
            )}
            searchPlaceholder={t(`${keyPrefix}.placeSearch` as never)}
            noResultsLabel={t(`${keyPrefix}.placeNoResults` as never)}
            icon={
                kind === "origin" ? (
                    <MapPin className="h-4 w-4 shrink-0 opacity-50" />
                ) : (
                    <Flag className="h-4 w-4 shrink-0 opacity-50" />
                )
            }
            className={className}
            label={label}
            fallbackLabel={(v) => v.replace(/^(code|raw):/, "")}
            clearLabel={t(`${keyPrefix}.clearFilter` as never)}
        />
    );
}
