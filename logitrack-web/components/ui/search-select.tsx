"use client";

/**
 * The one searchable select of the web (UI2, issue #142): every pick-from-a-list control uses it, directly
 * or through the compound `Select` API of `./select` (a declarative front end of the same panel).
 *
 * Built on the shadcn Combobox pattern: a Radix Popover holds a `cmdk` Command. cmdk is about 6 KB and was
 * already in the bundle; react-select (25-30 KB gzip plus the emotion runtime) was not needed for any of the
 * features below.
 *
 * - Type to search: Thai and English, case- and diacritic-insensitive (`lib/searchText.ts`). Lists of
 *   `SEARCH_ABOVE` options or fewer open without a search box (the arrow keys and Enter still pick).
 * - Options carry a label and secondary text (code + name, plate + type), may be disabled, and the list
 *   shows a loading state, an error, an empty state and an optional "create" row.
 * - Remote lists (`onSearchChange`): the caller fetches with the typed term (debounced, TanStack Query,
 *   a `q` parameter) and passes the page it got; the options are then shown as given, so the matching is
 *   the server's (the Thai / diacritic folding above applies to local lists only). The selected value
 *   stays labelled when it is not in the current page (`selectedOption(s)`, else the last label seen).
 * - Above `VIRTUALIZE_ABOVE` matching options the list is virtualised (TanStack Virtual, in its own chunk
 *   `./search-select-virtual`, preloaded when such a select mounts, so short selects keep it out of the
 *   initial JS), and a list of 5,000 keeps well under 100 rows in the DOM; every option stays reachable by
 *   scroll and search (no silent caps, Appendix E §E.10).
 * - Single (`SearchSelect`, optional clear button) and multi (`MultiSearchSelect`, chips). The
 *   react-hook-form binding is `./form-search-select`.
 * - The popover is modal and portalled on the `popover` layer of the layering scale (UI1, app/globals.css),
 *   above every Dialog and Sheet, so it opens, scrolls and picks inside a Dialog, Sheet or Popover with no
 *   z-index or `position` props.
 *
 * Rule (UI2): a fixed list of five options or fewer (a status filter, yes/no) may use a segmented control
 * or a radio group instead; every other pick-from-a-list control uses this component.
 */
import * as React from "react";
import { Command as CommandPrimitive } from "cmdk";
import { Check, ChevronsUpDown, Loader2, Plus, Search, X } from "lucide-react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useLanguage } from "@/context/language";
import { filterBySearch } from "@/lib/searchText";
import { cn } from "@/lib/utils";

/** One option. `text` is what the search matches when `label` is not plain text. */
export interface SearchSelectOption {
    value: string;
    label: React.ReactNode;
    /** Searchable text of the label (defaults to the label's text). */
    text?: string;
    /** Secondary text, shown after the label in the list (a code, a truck type). */
    description?: React.ReactNode;
    /** More text the search matches (a code, the other language's name). */
    keywords?: readonly string[];
    disabled?: boolean;
}

/** Lists longer than this open with a search box (shorter ones pick with the arrow keys). */
export const SEARCH_ABOVE = 7;
/** Above this many matching options the open list is virtualised. */
export const VIRTUALIZE_ABOVE = 200;
/** Rows the list shows while its virtualised chunk loads, or if that chunk fails (with a note). */
export const FALLBACK_ROWS = 50;
/** Height of one option row (`py-1.5` + `text-sm`), px. */
export const OPTION_ROW_HEIGHT = 32;

/** The plain text of a React node (strings and numbers, depth first), for searching a JSX label. */
export function nodeText(node: React.ReactNode): string {
    if (node == null || typeof node === "boolean") return "";
    if (typeof node === "string" || typeof node === "number" || typeof node === "bigint") return String(node);
    if (Array.isArray(node)) return node.map(nodeText).filter(Boolean).join(" ");
    if (React.isValidElement(node)) {
        return nodeText((node.props as { children?: React.ReactNode }).children);
    }
    return "";
}

/** Everything the search matches of an option: its text (or label), description and keywords. */
export function optionSearchText(o: SearchSelectOption): string {
    const parts = [o.text ?? nodeText(o.label), nodeText(o.description), ...(o.keywords ?? [])];
    return parts.filter(Boolean).join(" ");
}

/** The UI strings of the select: the page's language when there is a provider, else English. */
function useSelectStrings() {
    let t: ((key: string, params?: Record<string, string | number>) => string) | null = null;
    try {
        // eslint-disable-next-line react-hooks/rules-of-hooks -- same call on every render; it throws only without a provider
        t = useLanguage().t;
    } catch {
        t = null;
    }
    return React.useCallback(
        (key: string, fallback: string, params?: Record<string, string | number>) => {
            const fill = (s: string) => s.replace(/\{(\w+)\}/g, (_, k: string) => String(params?.[k] ?? ""));
            if (!t) return fill(fallback);
            const out = t(`common.select.${key}`, params);
            return !out || out === `common.select.${key}` ? fill(fallback) : out;
        },
        [t]
    );
}

/**
 * The options that label the selected values: the current options, the caller's `known` ones (a remote
 * page may not hold the value) and the ones picked here (kept in state, so a picked value keeps its label
 * after the remote page changes).
 */
function useOptionLookup(
    options: readonly SearchSelectOption[],
    known: readonly (SearchSelectOption | null | undefined)[],
    values: readonly string[]
) {
    const [picked, setPicked] = React.useState<ReadonlyMap<string, SearchSelectOption>>(() => new Map());
    // A selected value found in the current options is remembered too (derived state, set during render:
    // only when its text changed, so options rebuilt on every render cannot loop).
    let next: Map<string, SearchSelectOption> | null = null;
    for (const v of values) {
        const o = options.find((x) => x.value === v);
        if (!o) continue;
        const had = picked.get(v);
        if (had && optionSearchText(had) === optionSearchText(o)) continue;
        next ??= new Map(picked);
        next.set(v, o);
    }
    if (next) setPicked(next);
    // `known` is a fresh array (of fresh objects) on most renders, and its length follows the selection of a
    // multi select: the memo keys on its text instead, so the deps keep a fixed size and the memo holds.
    const knownKey = known.map((o) => (o ? `${o.value}\u0001${optionSearchText(o)}` : "")).join("\u0002");
    const lookup = React.useMemo(() => {
        const map = new Map(picked);
        for (const o of known) if (o) map.set(o.value, o);
        for (const o of options) map.set(o.value, o);
        return map;
        // eslint-disable-next-line react-hooks/exhaustive-deps -- `knownKey` stands for `known` (same text = same labels)
    }, [options, picked, knownKey]);
    const remember = React.useCallback((o: SearchSelectOption) => {
        setPicked((prev) => (prev.get(o.value) === o ? prev : new Map(prev).set(o.value, o)));
    }, []);
    return [lookup, remember] as const;
}

export const selectTriggerClassName =
    "border-input data-[placeholder]:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive dark:bg-input/30 dark:hover:bg-input/50 flex w-full min-w-0 items-center justify-between gap-2 rounded-md border bg-transparent px-3 py-2 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50 data-[size=default]:min-h-9 data-[size=sm]:min-h-8 [&_svg]:pointer-events-none [&_svg]:shrink-0";

export const selectContentClassName =
    "w-[var(--radix-popover-trigger-width)] min-w-[14rem] max-w-[min(32rem,calc(100vw-1rem))] p-0";

// ---------------------------------------------------------------------------------------------------------
// The panel: search box + list (shared by SearchSelect, MultiSearchSelect and the compound Select).
// ---------------------------------------------------------------------------------------------------------

const loadVirtualRows = () => import("./search-select-virtual");
/** Preloads the virtualised list's chunk (a select whose options pass `VIRTUALIZE_ABOVE` calls it on mount). */
export function preloadVirtualRows() {
    loadVirtualRows().catch(() => undefined);
}
const VirtualRows = React.lazy(() => loadVirtualRows().then((m) => ({ default: m.SearchSelectVirtualRows })));

/** Preloads the virtualised list's chunk once a select holds a long list (before it opens). */
export function usePreloadWhenLong(count: number) {
    React.useEffect(() => {
        if (count > VIRTUALIZE_ABOVE) preloadVirtualRows();
    }, [count]);
}

/** Renders `fallback` when the virtualised list's chunk fails to load. */
class VirtualRowsBoundary extends React.Component<{ fallback: React.ReactNode; children: React.ReactNode }, { failed: boolean }> {
    state = { failed: false };
    static getDerivedStateFromError() {
        return { failed: true };
    }
    render() {
        return this.state.failed ? this.props.fallback : this.props.children;
    }
}

export interface SearchSelectPanelProps {
    options: readonly SearchSelectOption[];
    /** The selected values (one for a single select). */
    selected: ReadonlySet<string>;
    onPick: (option: SearchSelectOption) => void;
    searchable: boolean;
    /** Remote mode: the term goes to the caller, and `options` are shown as given (no local filter). */
    onSearchChange?: (term: string) => void;
    searchPlaceholder?: string;
    emptyText?: React.ReactNode;
    loading?: boolean;
    error?: React.ReactNode;
    /** A "create" row for the typed term (shown when the term matches no option exactly). */
    onCreate?: (term: string) => void;
    createLabel?: (term: string) => React.ReactNode;
    listId?: string;
    "aria-label"?: string;
}

function OptionRow({ option, selected, onPick }: { option: SearchSelectOption; selected: boolean; onPick: (o: SearchSelectOption) => void }) {
    return (
        <CommandPrimitive.Item
            value={option.value}
            disabled={option.disabled}
            onSelect={() => onPick(option)}
            data-slot="search-select-option"
            data-checked={selected ? "true" : undefined}
            className="relative flex min-h-8 cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50"
        >
            <Check aria-hidden className={cn("h-4 w-4 shrink-0", selected ? "opacity-100" : "opacity-0")} />
            <span className="min-w-0 flex-1 truncate">{option.label}</span>
            {option.description != null && option.description !== "" ? (
                <span className="shrink-0 truncate text-xs text-muted-foreground max-w-[45%]">{option.description}</span>
            ) : null}
        </CommandPrimitive.Item>
    );
}

export function SearchSelectPanel({
    options,
    selected,
    onPick,
    searchable,
    onSearchChange,
    searchPlaceholder,
    emptyText,
    loading,
    error,
    onCreate,
    createLabel,
    listId,
    "aria-label": ariaLabel,
}: SearchSelectPanelProps) {
    const s = useSelectStrings();
    const [term, setTerm] = React.useState("");
    const remote = onSearchChange !== undefined;
    const shown = React.useMemo(
        () => (remote || !term.trim() ? options : filterBySearch(options, term, optionSearchText)),
        [options, term, remote]
    );
    const virtual = shown.length > VIRTUALIZE_ABOVE;
    const listRef = React.useRef<HTMLDivElement | null>(null);
    usePreloadWhenLong(options.length);
    const [listEl, setListEl] = React.useState<HTMLDivElement | null>(null);
    const attachList = React.useCallback((el: HTMLDivElement | null) => {
        listRef.current = el;
        setListEl(el);
    }, []);

    const firstSelected = React.useMemo(() => {
        for (const o of options) if (selected.has(o.value)) return o.value;
        return undefined;
    }, [options, selected]);
    // cmdk's highlighted row: the selected one when the list opens, then the first match of each search.
    const [active, setActive] = React.useState<string>(firstSelected ?? "");
    const setSearch = (v: string) => {
        setTerm(v);
        onSearchChange?.(v);
        if (listRef.current) listRef.current.scrollTop = 0;
    };
    React.useEffect(() => {
        if (!term) return;
        const first = shown.find((o) => !o.disabled);
        setActive(first ? first.value : "");
    }, [shown, term]);

    const trimmed = term.trim();
    const showCreate =
        !!onCreate && trimmed !== "" && !options.some((o) => (o.text ?? nodeText(o.label)).trim().toLocaleLowerCase() === trimmed.toLocaleLowerCase());

    const row = (o: SearchSelectOption) => <OptionRow key={o.value} option={o} selected={selected.has(o.value)} onPick={onPick} />;
    // While the virtualised list's chunk loads (it is preloaded when a long select mounts), or if it fails:
    // the first rows and a visible note, never thousands of rows and never a silent cut (Appendix E §E.10).
    const partial = (
        <>
            {shown.slice(0, FALLBACK_ROWS).map(row)}
            <p className="px-2 py-1.5 text-xs text-muted-foreground" role="status">
                {s("partial", "Showing {shown} of {total}. Type to narrow the list.", { shown: FALLBACK_ROWS, total: shown.length })}
            </p>
        </>
    );

    return (
        <CommandPrimitive
            shouldFilter={false}
            loop
            value={active}
            onValueChange={setActive}
            label={ariaLabel}
            className="flex w-full flex-col overflow-hidden rounded-md bg-popover text-popover-foreground outline-none"
            tabIndex={searchable ? undefined : -1}
            data-slot="search-select-panel"
        >
            {searchable ? (
                <div className="flex items-center gap-2 border-b px-3" cmdk-input-wrapper="">
                    <Search aria-hidden className="h-4 w-4 shrink-0 opacity-50" />
                    <CommandPrimitive.Input
                        value={term}
                        onValueChange={setSearch}
                        placeholder={searchPlaceholder ?? s("search", "Search...")}
                        className="flex h-10 w-full bg-transparent py-2 text-sm outline-none placeholder:text-muted-foreground"
                    />
                    {loading ? <Loader2 aria-hidden className="h-4 w-4 shrink-0 animate-spin opacity-60" /> : null}
                </div>
            ) : null}
            <CommandPrimitive.List
                ref={attachList}
                id={listId}
                data-slot="search-select-list"
                className="max-h-[min(300px,var(--radix-popover-content-available-height,300px))] overflow-y-auto overflow-x-hidden overscroll-contain p-1"
            >
                {error ? <p className="px-2 py-2 text-xs text-destructive">{error}</p> : null}
                {loading && shown.length === 0 ? (
                    <CommandPrimitive.Loading>
                        <div className="flex items-center justify-center gap-2 py-6 text-sm text-muted-foreground">
                            <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
                            {s("loading", "Loading...")}
                        </div>
                    </CommandPrimitive.Loading>
                ) : null}
                {!loading && !error && shown.length === 0 && !showCreate ? (
                    <div className="py-6 text-center text-sm text-muted-foreground" role="status">
                        {emptyText ?? s("empty", "No results")}
                    </div>
                ) : null}
                {virtual ? (
                    <VirtualRowsBoundary fallback={partial}>
                        <React.Suspense fallback={partial}>
                            <VirtualRows
                                options={shown}
                                scrollElement={listEl}
                                resetKey={term}
                                initialValue={firstSelected}
                                renderRow={row}
                                rowHeight={OPTION_ROW_HEIGHT}
                            />
                        </React.Suspense>
                    </VirtualRowsBoundary>
                ) : (
                    shown.map(row)
                )}
                {showCreate ? (
                    <CommandPrimitive.Item
                        value={`__search-select-create__:${trimmed}`}
                        onSelect={() => onCreate?.(trimmed)}
                        className="flex min-h-8 cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[selected=true]:bg-accent"
                    >
                        <Plus aria-hidden className="h-4 w-4 shrink-0" />
                        <span className="truncate">{createLabel ? createLabel(trimmed) : s("create", "Create “{term}”", { term: trimmed })}</span>
                    </CommandPrimitive.Item>
                ) : null}
            </CommandPrimitive.List>
        </CommandPrimitive>
    );
}

// ---------------------------------------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------------------------------------

interface TriggerOwnProps {
    open: boolean;
    empty: boolean;
    size?: "sm" | "default";
    icon?: React.ReactNode;
    loading?: boolean;
    /** Shown on the right; clicking it (or Delete / Backspace on the trigger) clears. */
    onClear?: () => void;
    clearLabel?: string;
    listId?: string;
}

/** The button that opens the list (a `role="combobox"` trigger, the shadcn Select look). */
export const SearchSelectTrigger = React.forwardRef<
    HTMLButtonElement,
    TriggerOwnProps & Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "value">
>(function SearchSelectTrigger({ open, empty, size = "default", icon, loading, onClear, clearLabel, listId, className, children, onKeyDown, ...props }, ref) {
    return (
        <button
            ref={ref}
            type="button"
            role="combobox"
            aria-expanded={open}
            aria-haspopup="listbox"
            aria-controls={open ? listId : undefined}
            data-slot="select-trigger"
            data-size={size}
            data-placeholder={empty ? "" : undefined}
            className={cn(selectTriggerClassName, className)}
            onKeyDown={(e) => {
                onKeyDown?.(e);
                if (e.defaultPrevented) return;
                if (onClear && !empty && (e.key === "Delete" || e.key === "Backspace")) {
                    e.preventDefault();
                    onClear();
                }
            }}
            {...props}
        >
            <span className="flex min-w-0 flex-1 items-center gap-2">
                {icon}
                {children}
            </span>
            <span className="flex shrink-0 items-center gap-1">
                {loading ? <Loader2 aria-hidden className="h-4 w-4 animate-spin opacity-50" /> : null}
                {onClear && !empty ? (
                    // A span, not a <button>: it sits inside the trigger button (nested buttons are invalid
                    // HTML). The keyboard clears with Delete / Backspace on the trigger.
                    <span
                        role="button"
                        tabIndex={-1}
                        aria-label={clearLabel}
                        title={clearLabel}
                        data-slot="search-select-clear"
                        className="rounded-sm opacity-60 hover:opacity-100 hover:text-destructive"
                        onPointerDown={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                        }}
                        onClick={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                            onClear();
                        }}
                    >
                        <X className="h-4 w-4" />
                    </span>
                ) : null}
                <ChevronsUpDown aria-hidden className="h-4 w-4 opacity-50" />
            </span>
        </button>
    );
});

// ---------------------------------------------------------------------------------------------------------
// Single select
// ---------------------------------------------------------------------------------------------------------

interface CommonProps {
    options: readonly SearchSelectOption[];
    placeholder?: React.ReactNode;
    searchPlaceholder?: string;
    emptyText?: React.ReactNode;
    disabled?: boolean;
    loading?: boolean;
    error?: React.ReactNode;
    /** Show the search box; default: more than `SEARCH_ABOVE` options, or a remote list. */
    searchable?: boolean;
    /** Remote mode (server search): called with the typed term; pass the fetched page as `options`. */
    onSearchChange?: (term: string) => void;
    onCreate?: (term: string) => void;
    createLabel?: (term: string) => React.ReactNode;
    id?: string;
    className?: string;
    contentClassName?: string;
    size?: "sm" | "default";
    /** A leading icon in the trigger. */
    icon?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onBlur?: React.FocusEventHandler<HTMLButtonElement>;
    "aria-label"?: string;
    "aria-labelledby"?: string;
    "aria-invalid"?: boolean | "true" | "false";
    "aria-describedby"?: string;
    "data-testid"?: string;
}

export interface SearchSelectProps extends CommonProps {
    value: string | null | undefined;
    onValueChange: (value: string, option: SearchSelectOption | null) => void;
    /** Adds a clear button; clearing emits `clearValue` (default ""). */
    clearable?: boolean;
    clearValue?: string;
    /** The clear button's accessible name (default "Clear"). */
    clearLabel?: string;
    /** The option of `value` when it may be missing from `options` (a remote page). */
    selectedOption?: SearchSelectOption | null;
    /** The trigger text of the selected option (default: its label). */
    renderValue?: (option: SearchSelectOption) => React.ReactNode;
}

function useOpenState(open: boolean | undefined, onOpenChange: ((o: boolean) => void) | undefined) {
    const [inner, setInner] = React.useState(false);
    const isOpen = open ?? inner;
    const setOpen = React.useCallback(
        (o: boolean) => {
            if (open === undefined) setInner(o);
            onOpenChange?.(o);
        },
        [open, onOpenChange]
    );
    return [isOpen, setOpen] as const;
}

export const SearchSelect = React.forwardRef<HTMLButtonElement, SearchSelectProps>(function SearchSelect(
    {
        options,
        value,
        onValueChange,
        clearable,
        clearValue = "",
        clearLabel,
        selectedOption,
        renderValue,
        placeholder,
        searchPlaceholder,
        emptyText,
        disabled,
        loading,
        error,
        searchable,
        onSearchChange,
        onCreate,
        createLabel,
        id,
        className,
        contentClassName,
        size,
        icon,
        open: openProp,
        onOpenChange,
        ...aria
    },
    ref
) {
    const s = useSelectStrings();
    const [open, setOpen] = useOpenState(openProp, onOpenChange);
    const listId = React.useId();
    usePreloadWhenLong(options.length);
    const [lookup, remember] = useOptionLookup(options, [selectedOption], value ? [value] : []);
    const current = value != null && value !== "" ? (lookup.get(value) ?? null) : null;
    const selected = React.useMemo(() => new Set(value != null && value !== "" ? [value] : []), [value]);
    const hasValue = value != null && value !== "" && value !== clearValue;
    const triggerText = current ? (renderValue ? renderValue(current) : current.label) : hasValue ? value : null;
    const empty = triggerText == null;
    const remote = onSearchChange !== undefined;

    const changeOpen = (o: boolean) => {
        setOpen(o);
        if (!o && remote) onSearchChange?.("");
    };
    return (
        <Popover open={open} onOpenChange={changeOpen} modal>
            <PopoverTrigger asChild disabled={disabled}>
                <SearchSelectTrigger
                    ref={ref}
                    id={id}
                    open={open}
                    empty={empty}
                    size={size}
                    icon={icon}
                    listId={listId}
                    disabled={disabled}
                    className={className}
                    onClear={clearable && !disabled && hasValue ? () => onValueChange(clearValue, null) : undefined}
                    clearLabel={clearLabel ?? s("clear", "Clear")}
                    {...aria}
                >
                    <span className={cn("truncate", empty && "text-muted-foreground")}>{empty ? (placeholder ?? s("placeholder", "Select...")) : triggerText}</span>
                    {current?.description != null && current.description !== "" && !renderValue ? (
                        <span className="hidden truncate text-xs text-muted-foreground sm:inline">{current.description}</span>
                    ) : null}
                </SearchSelectTrigger>
            </PopoverTrigger>
            <PopoverContent align="start" className={cn(selectContentClassName, contentClassName)} onOpenAutoFocus={focusPanel}>
                <SearchSelectPanel
                    options={options}
                    selected={selected}
                    onPick={(o) => {
                        remember(o);
                        onValueChange(o.value, o);
                        changeOpen(false);
                    }}
                    searchable={searchable ?? (remote || options.length > SEARCH_ABOVE)}
                    onSearchChange={onSearchChange}
                    searchPlaceholder={searchPlaceholder}
                    emptyText={emptyText}
                    loading={loading}
                    error={error}
                    onCreate={
                        onCreate
                            ? (term) => {
                                  onCreate(term);
                                  changeOpen(false);
                              }
                            : undefined
                    }
                    createLabel={createLabel}
                    listId={listId}
                    aria-label={aria["aria-label"]}
                />
            </PopoverContent>
        </Popover>
    );
});

/** Puts the focus in the panel when the popover opens: the search box, else the list (for the arrow keys). */
export function focusPanel(e: Event) {
    const root = e.currentTarget as HTMLElement | null;
    const target = root?.querySelector<HTMLElement>("[cmdk-input]") ?? root?.querySelector<HTMLElement>("[cmdk-root]");
    if (target) {
        e.preventDefault();
        target.focus();
    }
}

// ---------------------------------------------------------------------------------------------------------
// Multi select (chips)
// ---------------------------------------------------------------------------------------------------------

export interface MultiSearchSelectProps extends CommonProps {
    value: readonly string[];
    onValueChange: (values: string[], options: SearchSelectOption[]) => void;
    /** Options of values that may be missing from `options` (a remote page). */
    selectedOptions?: readonly SearchSelectOption[];
    /** At most this many values; further picks are ignored. */
    max?: number;
}

export const MultiSearchSelect = React.forwardRef<HTMLButtonElement, MultiSearchSelectProps>(function MultiSearchSelect(
    {
        options,
        value,
        onValueChange,
        selectedOptions,
        max,
        placeholder,
        searchPlaceholder,
        emptyText,
        disabled,
        loading,
        error,
        searchable,
        onSearchChange,
        onCreate,
        createLabel,
        id,
        className,
        contentClassName,
        size,
        icon,
        open: openProp,
        onOpenChange,
        ...aria
    },
    ref
) {
    const s = useSelectStrings();
    const [open, setOpen] = useOpenState(openProp, onOpenChange);
    const listId = React.useId();
    usePreloadWhenLong(options.length);
    const [lookup, remember] = useOptionLookup(options, selectedOptions ?? [], value);
    const selected = React.useMemo(() => new Set(value), [value]);
    const picked = value.map((v) => lookup.get(v) ?? { value: v, label: v });
    const remote = onSearchChange !== undefined;
    const emit = (values: string[]) =>
        onValueChange(
            values,
            values.map((v) => lookup.get(v) ?? { value: v, label: v })
        );
    const toggle = (o: SearchSelectOption) => {
        remember(o);
        if (selected.has(o.value)) emit(value.filter((v) => v !== o.value));
        else if (max === undefined || value.length < max) emit([...value, o.value]);
    };
    const changeOpen = (o: boolean) => {
        setOpen(o);
        if (!o && remote) onSearchChange?.("");
    };

    return (
        <Popover open={open} onOpenChange={changeOpen} modal>
            <PopoverTrigger asChild disabled={disabled}>
                <SearchSelectTrigger
                    ref={ref}
                    id={id}
                    open={open}
                    empty={picked.length === 0}
                    size={size}
                    icon={icon}
                    listId={listId}
                    disabled={disabled}
                    className={cn("h-auto py-1.5", className)}
                    onClear={!disabled ? () => emit([]) : undefined}
                    clearLabel={s("clear", "Clear")}
                    aria-multiselectable
                    {...aria}
                >
                    {picked.length === 0 ? (
                        <span className="truncate text-muted-foreground">{placeholder ?? s("placeholder", "Select...")}</span>
                    ) : (
                        <span className="flex min-w-0 flex-wrap gap-1" data-slot="search-select-chips">
                            {picked.map((o) => (
                                <span
                                    key={o.value}
                                    className="inline-flex max-w-full items-center gap-1 rounded-md border bg-secondary px-1.5 py-0.5 text-xs text-secondary-foreground"
                                >
                                    <span className="truncate">{o.label}</span>
                                    {!disabled ? (
                                        <span
                                            role="button"
                                            tabIndex={-1}
                                            aria-label={s("remove", "Remove {label}", { label: o.text ?? nodeText(o.label) })}
                                            className="rounded-sm opacity-60 hover:opacity-100"
                                            onPointerDown={(e) => {
                                                e.preventDefault();
                                                e.stopPropagation();
                                            }}
                                            onClick={(e) => {
                                                e.preventDefault();
                                                e.stopPropagation();
                                                emit(value.filter((v) => v !== o.value));
                                            }}
                                        >
                                            <X className="h-3 w-3" />
                                        </span>
                                    ) : null}
                                </span>
                            ))}
                        </span>
                    )}
                </SearchSelectTrigger>
            </PopoverTrigger>
            <PopoverContent align="start" className={cn(selectContentClassName, contentClassName)} onOpenAutoFocus={focusPanel}>
                <SearchSelectPanel
                    options={options}
                    selected={selected}
                    onPick={toggle}
                    searchable={searchable ?? (remote || options.length > SEARCH_ABOVE)}
                    onSearchChange={onSearchChange}
                    searchPlaceholder={searchPlaceholder}
                    emptyText={emptyText}
                    loading={loading}
                    error={error}
                    onCreate={onCreate}
                    createLabel={createLabel}
                    listId={listId}
                    aria-label={aria["aria-label"]}
                />
                {max !== undefined ? (
                    <p className="border-t px-3 py-1.5 text-xs text-muted-foreground" role="status">
                        {s("count", "{count} of {max} selected", { count: value.length, max })}
                    </p>
                ) : null}
            </PopoverContent>
        </Popover>
    );
});
