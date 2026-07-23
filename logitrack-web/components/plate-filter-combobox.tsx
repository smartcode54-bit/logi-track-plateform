"use client";

import { useMemo } from "react";
import { Truck } from "lucide-react";
import { useLanguage } from "@/context/language";
import { FilterCombobox, type FilterComboboxOption } from "@/components/filter-combobox";
import { PLATE_FILTER_ALL, PLATE_FILTER_NONE, type PlateFilterOption } from "@/lib/truckPlate";

export interface PlateFilterComboboxProps {
    options: PlateFilterOption[];
    value: string;
    onChange: (value: string) => void;
    /** i18n key prefix — "driverMonitor.filter" or "accounting.billingDocument.filters". */
    keyPrefix: string;
    className?: string;
    /** Rendered above the control (Billing Document uses a label; the filter bar does not). */
    label?: string;
}

/**
 * Searchable licence-plate filter — the plate-specific labelling on top of `FilterCombobox`.
 *
 * Options come from the loaded rows, so each shows its row count and orphan plates (a plate with no
 * fleet doc) are badged rather than hidden.
 */
export function PlateFilterCombobox({
    options,
    value,
    onChange,
    keyPrefix,
    className,
    label,
}: PlateFilterComboboxProps) {
    const { t } = useLanguage();

    const comboOptions = useMemo<FilterComboboxOption[]>(
        () =>
            options.map((o) => ({
                value: o.value,
                label:
                    o.value === PLATE_FILTER_NONE
                        ? t(`${keyPrefix}.plateNotSpecified` as never)
                        : (o.label ?? o.value),
                count: o.count,
                badge: o.isOrphan ? t(`${keyPrefix}.plateNotInFleet` as never) : undefined,
            })),
        [options, keyPrefix, t]
    );

    return (
        <FilterCombobox
            options={comboOptions}
            value={value}
            onChange={onChange}
            allValue={PLATE_FILTER_ALL}
            allLabel={t(`${keyPrefix}.allLicensePlates` as never)}
            searchPlaceholder={t(`${keyPrefix}.plateSearch` as never)}
            noResultsLabel={t(`${keyPrefix}.plateNoResults` as never)}
            icon={<Truck className="h-4 w-4 shrink-0 opacity-50" />}
            className={className}
            label={label}
            fallbackLabel={(v) => v.replace(/^(id|plate):/, "")}
            clearLabel={t(`${keyPrefix}.clearFilter` as never)}
        />
    );
}
