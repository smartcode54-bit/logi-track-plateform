"use client";

/**
 * The compound (shadcn-style) API of the searchable select (UI2, issue #142):
 *
 *     <Select value={v} onValueChange={setV}>
 *       <SelectTrigger className="w-[180px]"><SelectValue placeholder="..." /></SelectTrigger>
 *       <SelectContent>
 *         <SelectItem value="a">A</SelectItem>
 *       </SelectContent>
 *     </Select>
 *
 * It is a declarative front end of `SearchSelect` (`./search-select`): the same Popover + cmdk panel, the
 * same Thai / English search, keyboard and screen-reader behaviour, and no Radix Select (the package is
 * gone; `lib/selectPrimitives.test.ts` keeps it and the native `<select>` out). `SelectContent` reads its
 * `SelectItem` children (direct children, arrays and fragments) as the options; it renders nothing
 * itself while closed. Lists of more than `SEARCH_ABOVE` options get the search box; above
 * `VIRTUALIZE_ABOVE` the list is virtualised.
 *
 * Long or remote lists (drivers, trucks, hubs, customers, users) use the option-based pickers of
 * `components/pickers` instead, which search on the server.
 */
import * as React from "react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

import {
    SEARCH_ABOVE,
    SearchSelectPanel,
    SearchSelectTrigger,
    focusPanel,
    nodeText,
    selectContentClassName,
    usePreloadWhenLong,
    type SearchSelectOption,
} from "./search-select";

type OptionsStore = {
    get: () => readonly SearchSelectOption[];
    set: (options: readonly SearchSelectOption[]) => void;
    subscribe: (fn: () => void) => () => void;
};

function createOptionsStore(): OptionsStore {
    let options: readonly SearchSelectOption[] = [];
    let signature = "";
    const listeners = new Set<() => void>();
    return {
        get: () => options,
        set: (next) => {
            const sig = next.map((o) => `${o.value}\u0001${o.text ?? ""}\u0001${o.disabled ? 1 : 0}`).join("\u0002");
            if (sig === signature) return;
            signature = sig;
            options = next;
            listeners.forEach((fn) => fn());
        },
        subscribe: (fn) => {
            listeners.add(fn);
            return () => listeners.delete(fn);
        },
    };
}

interface SelectContextValue {
    value: string;
    setValue: (v: string) => void;
    open: boolean;
    setOpen: (o: boolean) => void;
    disabled: boolean;
    store: OptionsStore;
    listId: string;
}

const SelectContext = React.createContext<SelectContextValue | null>(null);

function useSelectContext(part: string): SelectContextValue {
    const ctx = React.useContext(SelectContext);
    if (!ctx) throw new Error(`<${part}> must be used within <Select>`);
    return ctx;
}

function useStoreOptions(store: OptionsStore) {
    return React.useSyncExternalStore(store.subscribe, store.get, store.get);
}

export interface SelectProps {
    value?: string;
    defaultValue?: string;
    onValueChange?: (value: string) => void;
    open?: boolean;
    defaultOpen?: boolean;
    onOpenChange?: (open: boolean) => void;
    disabled?: boolean;
    children?: React.ReactNode;
}

function Select({ value: valueProp, defaultValue, onValueChange, open: openProp, defaultOpen, onOpenChange, disabled = false, children }: SelectProps) {
    const [innerValue, setInnerValue] = React.useState(defaultValue ?? "");
    const [innerOpen, setInnerOpen] = React.useState(defaultOpen ?? false);
    const value = valueProp ?? innerValue;
    const open = openProp ?? innerOpen;
    const [store] = React.useState(createOptionsStore);
    const listId = React.useId();
    const ctx = React.useMemo<SelectContextValue>(
        () => ({
            value,
            setValue: (v) => {
                if (valueProp === undefined) setInnerValue(v);
                onValueChange?.(v);
            },
            open,
            setOpen: (o) => {
                if (openProp === undefined) setInnerOpen(o);
                onOpenChange?.(o);
            },
            disabled,
            store,
            listId,
        }),
        [value, valueProp, onValueChange, open, openProp, onOpenChange, disabled, store, listId]
    );
    return (
        <SelectContext.Provider value={ctx}>
            <Popover open={open} onOpenChange={ctx.setOpen} modal>
                {children}
            </Popover>
        </SelectContext.Provider>
    );
}

type SelectTriggerProps = Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "value"> & {
    size?: "sm" | "default";
    /** Ignored (older call sites passed the field value here). */
    value?: unknown;
};

const SelectTrigger = React.forwardRef<HTMLButtonElement, SelectTriggerProps>(function SelectTrigger(
    { className, size = "default", children, disabled, value: _ignored, ...props },
    ref
) {
    void _ignored;
    const ctx = useSelectContext("SelectTrigger");
    const options = useStoreOptions(ctx.store);
    const empty = !options.some((o) => o.value === ctx.value) && !ctx.value;
    return (
        <PopoverTrigger asChild disabled={ctx.disabled || disabled}>
            <SearchSelectTrigger
                ref={ref}
                open={ctx.open}
                empty={empty}
                size={size}
                listId={ctx.listId}
                disabled={ctx.disabled || disabled}
                // The old trigger was `w-fit`; call sites set their width with className.
                className={cn("w-fit", className)}
                {...props}
            >
                {children}
            </SearchSelectTrigger>
        </PopoverTrigger>
    );
});

/** The selected option's label (the `SelectItem` children), else the placeholder. */
function SelectValue({ placeholder, className }: { placeholder?: React.ReactNode; className?: string }) {
    const ctx = useSelectContext("SelectValue");
    const options = useStoreOptions(ctx.store);
    const current = options.find((o) => o.value === ctx.value);
    if (current) {
        return (
            <span data-slot="select-value" className={cn("flex min-w-0 items-center gap-2 truncate", className)}>
                {current.label}
            </span>
        );
    }
    return (
        <span data-slot="select-value" className={cn("truncate text-muted-foreground", className)}>
            {ctx.value || placeholder}
        </span>
    );
}

export interface SelectItemProps {
    value: string;
    disabled?: boolean;
    /** Searchable text when the children are not plain text. */
    textValue?: string;
    /** Secondary text shown after the label in the list. */
    description?: React.ReactNode;
    keywords?: readonly string[];
    className?: string;
    children?: React.ReactNode;
}

/** One option of a `SelectContent`. It renders nothing itself: the content reads its props. */
function SelectItem(props: SelectItemProps): React.ReactElement | null {
    void props;
    return null;
}

/** The `SelectItem`s among `children` (arrays and fragments included), in order. */
export function collectSelectItems(children: React.ReactNode): SearchSelectOption[] {
    const out: SearchSelectOption[] = [];
    const walk = (node: React.ReactNode) => {
        React.Children.forEach(node, (child) => {
            if (!React.isValidElement(child)) return;
            if (child.type === React.Fragment) {
                walk((child.props as { children?: React.ReactNode }).children);
                return;
            }
            if (child.type === SelectItem) {
                const p = child.props as SelectItemProps;
                out.push({
                    value: p.value,
                    label: p.children,
                    text: p.textValue ?? nodeText(p.children),
                    description: p.description,
                    keywords: p.keywords,
                    disabled: p.disabled,
                });
                return;
            }
            if (process.env.NODE_ENV !== "production") {
                console.warn("SelectContent: only <SelectItem> children (in arrays or fragments) are options; ignored", child.type);
            }
        });
    };
    walk(children);
    return out;
}

export interface SelectContentProps {
    className?: string;
    children?: React.ReactNode;
    /** Search box placeholder; the box shows for lists of more than `SEARCH_ABOVE` options. */
    searchPlaceholder?: string;
    searchable?: boolean;
    emptyText?: React.ReactNode;
    align?: "start" | "center" | "end";
    /** Accepted for compatibility with the Radix API and ignored (the panel always pops below or above). */
    position?: "popper" | "item-aligned";
}

function SelectContent({ className, children, searchPlaceholder, searchable, emptyText, align = "start" }: SelectContentProps) {
    const ctx = useSelectContext("SelectContent");
    const options = React.useMemo(() => collectSelectItems(children), [children]);
    usePreloadWhenLong(options.length);
    React.useLayoutEffect(() => {
        ctx.store.set(options);
    }, [ctx.store, options]);
    const selected = React.useMemo(() => new Set(ctx.value ? [ctx.value] : []), [ctx.value]);
    return (
        <PopoverContent align={align} className={cn(selectContentClassName, className)} onOpenAutoFocus={focusPanel} data-slot="select-content">
            <SearchSelectPanel
                options={options}
                selected={selected}
                onPick={(o) => {
                    ctx.setValue(o.value);
                    ctx.setOpen(false);
                }}
                searchable={searchable ?? options.length > SEARCH_ABOVE}
                searchPlaceholder={searchPlaceholder}
                emptyText={emptyText}
                listId={ctx.listId}
            />
        </PopoverContent>
    );
}

export { Select, SelectContent, SelectItem, SelectTrigger, SelectValue };
