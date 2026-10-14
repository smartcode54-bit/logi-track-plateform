"use client";

/**
 * The shared pickers of the master records (UI2, issue #142): drivers, trucks, hubs / SOCs and customers,
 * each a `SearchSelect` (or `MultiSearchSelect`) that searches on the server.
 *
 * - The typed term is debounced (`PICKER_DEBOUNCE_MS`) and sent as `q`; the query runs only while the list
 *   is open, through TanStack Query (cached per term), so a picker never reads every record.
 * - The selected value keeps its label when the current page does not hold it: pass `selectedLabel` (or the
 *   picked option is remembered by `SearchSelect`).
 * - Errors show the API's message in the list (`apiErrorText`).
 * - Matching is the Go endpoint's: a case-insensitive substring of `q` (no Thai tone-mark / diacritic
 *   folding and no any-word-order match; those apply to local lists only, `lib/searchText.ts`).
 * - `HubSelect` has no product caller yet: the task and trip pages still read the whole hub master for
 *   their row lookups and filter it in the browser (`HubCombobox`); moving them is the follow-up of #142.
 * - The customer pickers label a value the page set (a row's customer, a default) by id
 *   (`useCustomerLookup`, `['customers','ref',id]`), so no page reads every customer to show one (UI2C).
 */
import * as React from "react";
import { useQuery, type QueryKey, type UseQueryOptions } from "@tanstack/react-query";

import {
    MultiSearchSelect,
    SearchSelect,
    type MultiSearchSelectProps,
    type SearchSelectOption,
    type SearchSelectProps,
} from "@/components/ui/search-select";
import { useLanguage } from "@/context/language";
import {
    customerLabel,
    customerSearchQuery,
    driverPicksQuery,
    hubLabel,
    hubSearchQuery,
    truckPicksQuery,
    type CustomerPick,
    type DriverPick,
    type DriverPickFilter,
    type HubPick,
    type TruckPick,
    type TruckPickFilter,
} from "@/features/accounting/api/pickers.go";
import { useCustomerLookup } from "@/features/accounting/api/useCustomerLookup";
import { apiErrorText } from "@/lib/apiError";

export const PICKER_DEBOUNCE_MS = 250;

/** A trailing debounce of a value. */
export function useDebouncedValue<T>(value: T, ms = PICKER_DEBOUNCE_MS): T {
    const [v, setV] = React.useState(value);
    React.useEffect(() => {
        const id = setTimeout(() => setV(value), ms);
        return () => clearTimeout(id);
    }, [value, ms]);
    return v;
}

type ListQuery<T, K extends QueryKey> = (q: string) => UseQueryOptions<T[], Error, T[], K>;

/**
 * The server search of a picker: the term state, the debounced query (enabled while open), and the props
 * to spread on a `SearchSelect`.
 */
export function useServerSearch<T, K extends QueryKey>(makeQuery: ListQuery<T, K>, toOption: (item: T) => SearchSelectOption) {
    const { t } = useLanguage();
    const [open, setOpen] = React.useState(false);
    const [term, setTerm] = React.useState("");
    const q = useDebouncedValue(term);
    const query = useQuery({ ...makeQuery(q), enabled: open });
    const options = React.useMemo(() => (query.data ?? []).map(toOption), [query.data, toOption]);
    return {
        options,
        loading: open && (query.isFetching || term !== q),
        error: query.error ? apiErrorText(query.error, t) : undefined,
        onSearchChange: setTerm,
        onOpenChange: setOpen,
    };
}

type PickerProps = Omit<SearchSelectProps, "options" | "onSearchChange" | "loading" | "error" | "selectedOption"> & {
    /** The label of `value` when it may not be in the first page (the record's display name). */
    selectedLabel?: string | null;
};

function selectedFrom(value: string | null | undefined, label: string | null | undefined): SearchSelectOption | null {
    return value && label ? { value, label } : null;
}

/** A page's touches of one option (a hint, a disabled row), laid over the picker's default option. */
export type DescribeOption<T> = (item: T) => Partial<SearchSelectOption> | undefined;

/** The option builder with the page's touches (keep `describe` stable, e.g. `useCallback`). */
function useDescribed<T>(toOption: (item: T) => SearchSelectOption, describe: DescribeOption<T> | undefined) {
    return React.useCallback(
        (item: T): SearchSelectOption => {
            const option = toOption(item);
            const extra = describe?.(item);
            return extra ? { ...option, ...extra } : option;
        },
        [toOption, describe]
    );
}

// --- drivers ----------------------------------------------------------------------------------------------

export const driverOption = (d: DriverPick): SearchSelectOption => ({ value: d.id, label: d.displayName || d.id.slice(0, 8) });

/**
 * `GET /v1/drivers?fields=minimal&q` (staff and dispatchers); `status` narrows on the server, `details` reads
 * the full rows (licence type, status, home truck) for `describe`.
 */
export const DriverSelect = React.forwardRef<HTMLButtonElement, PickerProps & DriverPickFilter & { describe?: DescribeOption<DriverPick> }>(function DriverSelect(
    { selectedLabel, status, details, describe, ...props },
    ref
) {
    const make = React.useCallback((q: string) => driverPicksQuery(q, { status, details }), [status, details]);
    const search = useServerSearch(make, useDescribed(driverOption, describe));
    return <SearchSelect ref={ref} {...props} {...search} selectedOption={selectedFrom(props.value, selectedLabel)} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

// --- trucks -----------------------------------------------------------------------------------------------

export const truckOption = (tr: TruckPick): SearchSelectOption => ({
    value: tr.id,
    label: tr.licensePlate || tr.id.slice(0, 8),
    description: tr.type ?? undefined,
});

/** `GET /v1/trucks?q` (plate, province, brand, model); `ownership` narrows on the server. */
export const TruckSelect = React.forwardRef<HTMLButtonElement, PickerProps & TruckPickFilter & { describe?: DescribeOption<TruckPick> }>(function TruckSelect(
    { selectedLabel, ownership, describe, ...props },
    ref
) {
    const make = React.useCallback((q: string) => truckPicksQuery(q, { ownership }), [ownership]);
    const search = useServerSearch(make, useDescribed(truckOption, describe));
    return <SearchSelect ref={ref} {...props} {...search} selectedOption={selectedFrom(props.value, selectedLabel)} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

// --- hubs and SOCs ----------------------------------------------------------------------------------------

export const hubOption = (h: HubPick): SearchSelectOption => ({
    value: h.sourceId,
    label: hubLabel(h),
    description: h.sourceId !== hubLabel(h) ? h.sourceId : undefined,
    keywords: [h.sourceId, h.sourceNameEn ?? "", h.sourceNameTh ?? ""],
});

/**
 * `GET /v1/hubs?q&stationType`; the value is the hub's code (`sourceId`), what tasks and rate cards hold.
 * A code that is not in the master (a legacy task) stays shown as the code.
 */
export const HubSelect = React.forwardRef<HTMLButtonElement, PickerProps & { stationType?: string; selectedHub?: HubPick | null }>(function HubSelect(
    { selectedLabel, stationType, selectedHub, ...props },
    ref
) {
    const make = React.useCallback((q: string) => hubSearchQuery(q, stationType), [stationType]);
    const search = useServerSearch(make, hubOption);
    const selected = selectedHub ? hubOption(selectedHub) : selectedFrom(props.value, selectedLabel);
    return <SearchSelect ref={ref} {...props} {...search} selectedOption={selected} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

// --- customers --------------------------------------------------------------------------------------------

export const customerOption = (c: CustomerPick): SearchSelectOption => ({
    value: c.id,
    label: c.name,
    text: customerLabel(c),
    description: c.code || undefined,
});

/** A customer by its billing party id; a customer without a party is listed but cannot be picked. */
export const customerPartyOption = (c: CustomerPick): SearchSelectOption => ({
    ...customerOption(c),
    value: c.billingPartyId ?? `customer:${c.id}`,
    disabled: !c.billingPartyId,
});

export type CustomerPickerProps = PickerProps & {
    /** The value of an "every customer" row listed first (a filter's "all"); none = a customer is required. */
    allValue?: string;
    allLabel?: string;
};

/**
 * The single customer pickers. The value's label: the "all" row, `selectedLabel`, the option SearchSelect
 * remembers once picked, else the customer looked up by the value (`useCustomerLookup`; `…` while it
 * resolves, the raw id when Go knows none).
 */
const CustomerPicker = React.forwardRef<HTMLButtonElement, CustomerPickerProps & { toOption: (c: CustomerPick) => SearchSelectOption }>(function CustomerPicker(
    { selectedLabel, allValue, allLabel, toOption, ...props },
    ref
) {
    const search = useServerSearch(customerSearchQuery, toOption);
    const value = props.value ?? "";
    const all = React.useMemo(() => (allValue === undefined ? null : { value: allValue, label: allLabel ?? allValue }), [allValue, allLabel]);
    const isAll = all !== null && value === all.value;
    const lookup = useCustomerLookup(value && !isAll && !selectedLabel ? [value] : []);
    const found = lookup.get(value);
    const selected = isAll
        ? all
        : selectedLabel
          ? selectedFrom(value, selectedLabel)
          : value
            ? found
                ? { ...toOption(found), value }
                : { value, label: lookup.label(value) }
            : null;
    const options = React.useMemo(() => (all ? [all, ...search.options] : search.options), [all, search.options]);
    return <SearchSelect ref={ref} {...props} {...search} options={options} selectedOption={selected} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

/** `GET /v1/customers?fields=minimal&q`; the value is the customer id. */
export const CustomerSearchSelect = React.forwardRef<HTMLButtonElement, CustomerPickerProps>(function CustomerSearchSelect(props, ref) {
    return <CustomerPicker ref={ref} {...props} toOption={customerOption} />;
});

/**
 * The same search; the value is the customer's billing party id (what tasks, standby records and the income
 * filters hold).
 */
export const CustomerPartySelect = React.forwardRef<HTMLButtonElement, CustomerPickerProps>(function CustomerPartySelect(props, ref) {
    return <CustomerPicker ref={ref} {...props} toOption={customerPartyOption} />;
});

/** Several customers by billing party id (the users page's customer scopes). */
export const CustomerPartyMultiSelect = React.forwardRef<
    HTMLButtonElement,
    Omit<MultiSearchSelectProps, "options" | "onSearchChange" | "loading" | "error"> & { selectedLabels?: Record<string, string> }
>(function CustomerPartyMultiSelect({ selectedLabels, ...props }, ref) {
    const search = useServerSearch(customerSearchQuery, customerPartyOption);
    const selectedOptions = props.value.flatMap((v) => (selectedLabels?.[v] ? [{ value: v, label: selectedLabels[v] }] : []));
    return <MultiSearchSelect ref={ref} {...props} {...search} selectedOptions={selectedOptions} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});
