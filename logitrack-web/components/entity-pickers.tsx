"use client";

/**
 * The shared pickers of the master records (UI2, issue #142): drivers, trucks, hubs / SOCs, customers and
 * users, each a `SearchSelect` (or `MultiSearchSelect`) that searches on the server.
 *
 * - The typed term is debounced (`PICKER_DEBOUNCE_MS`) and sent as `q`; the query runs only while the list
 *   is open, through TanStack Query (cached per term), so a picker never reads every record.
 * - The selected value keeps its label when the current page does not hold it: pass `selectedLabel` (or the
 *   picked option is remembered by `SearchSelect`).
 * - Errors show the API's message in the list (`apiErrorText`).
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
    type HubPick,
    type TruckPick,
} from "@/features/accounting/api/pickers.go";
import { userSearchQuery, type UserDTO, type UsersReach } from "@/features/users/api/users";
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

// --- drivers ----------------------------------------------------------------------------------------------

export const driverOption = (d: DriverPick): SearchSelectOption => ({ value: d.id, label: d.displayName || d.id.slice(0, 8) });

/** `GET /v1/drivers?fields=minimal&q` (staff and dispatchers). */
export const DriverSelect = React.forwardRef<HTMLButtonElement, PickerProps>(function DriverSelect({ selectedLabel, ...props }, ref) {
    const search = useServerSearch(driverPicksQuery, driverOption);
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

/** `GET /v1/trucks?q` (plate, province, brand, model). */
export const TruckSelect = React.forwardRef<HTMLButtonElement, PickerProps>(function TruckSelect({ selectedLabel, ...props }, ref) {
    const search = useServerSearch(truckPicksQuery, truckOption);
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

/** `GET /v1/customers?fields=minimal&q`; the value is the customer id. */
export const CustomerSearchSelect = React.forwardRef<HTMLButtonElement, PickerProps>(function CustomerSearchSelect({ selectedLabel, ...props }, ref) {
    const search = useServerSearch(customerSearchQuery, customerOption);
    return <SearchSelect ref={ref} {...props} {...search} selectedOption={selectedFrom(props.value, selectedLabel)} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

/** Several customers by billing party id (the users page's customer scopes). */
export const CustomerPartyMultiSelect = React.forwardRef<
    HTMLButtonElement,
    Omit<MultiSearchSelectProps, "options" | "onSearchChange" | "loading" | "error"> & { selectedLabels?: Record<string, string> }
>(function CustomerPartyMultiSelect({ selectedLabels, ...props }, ref) {
    const toOption = React.useCallback(
        (c: CustomerPick): SearchSelectOption => ({ ...customerOption(c), value: c.billingPartyId ?? `customer:${c.id}`, disabled: !c.billingPartyId }),
        []
    );
    const search = useServerSearch(customerSearchQuery, toOption);
    const selectedOptions = props.value.flatMap((v) => (selectedLabels?.[v] ? [{ value: v, label: selectedLabels[v] }] : []));
    return <MultiSearchSelect ref={ref} {...props} {...search} selectedOptions={selectedOptions} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});

// --- users ------------------------------------------------------------------------------------------------

export const userOption = (u: UserDTO): SearchSelectOption => ({
    value: u.id,
    label: u.displayName || u.email || u.id.slice(0, 8),
    description: u.displayName && u.email ? u.email : undefined,
});

/** `GET /v1/users?q&status=active` (a platform principal passes `reach="all"`). */
export const UserSelect = React.forwardRef<HTMLButtonElement, PickerProps & { reach?: UsersReach }>(function UserSelect({ selectedLabel, reach = "tenant", ...props }, ref) {
    const make = React.useCallback((q: string) => userSearchQuery(q, reach), [reach]);
    const search = useServerSearch(make, userOption);
    return <SearchSelect ref={ref} {...props} {...search} selectedOption={selectedFrom(props.value, selectedLabel)} onOpenChange={(o) => {
        search.onOpenChange(o);
        props.onOpenChange?.(o);
    }} />;
});
