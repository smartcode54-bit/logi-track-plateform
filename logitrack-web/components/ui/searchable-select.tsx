"use client";

import * as React from "react";
import { Search } from "lucide-react";
import * as SelectPrimitive from "@radix-ui/react-select";
import { CheckIcon, ChevronDownIcon } from "lucide-react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

export interface SearchableSelectOption {
    value: string;
    label: string;
}

interface SearchableSelectProps {
    value: string;
    onValueChange: (value: string) => void;
    options: SearchableSelectOption[];
    placeholder?: string;
    label?: string;
    disabled?: boolean;
    className?: string;
    id?: string;
}

export function SearchableSelect({
    value,
    onValueChange,
    options,
    placeholder = "Select an option...",
    label,
    disabled = false,
    className,
    id,
}: SearchableSelectProps) {
    const [open, setOpen] = React.useState(false);
    const [searchValue, setSearchValue] = React.useState("");

    const filteredOptions = React.useMemo(() => {
        if (!searchValue.trim()) return options;
        const lowerSearch = searchValue.toLowerCase();
        return options.filter(
            (opt) =>
                opt.label.toLowerCase().includes(lowerSearch) ||
                opt.value.toLowerCase().includes(lowerSearch)
        );
    }, [options, searchValue]);


    return (
        <div className={cn("relative", className)}>
            {label && (
                <label
                    htmlFor={id}
                    className="text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
                >
                    {label}
                </label>
            )}

            <SelectPrimitive.Root
                value={value}
                onValueChange={onValueChange}
                open={open}
                onOpenChange={setOpen}
            >
                <div className="relative">
                    <SelectPrimitive.Trigger
                        id={id}
                        disabled={disabled}
                        className={cn(
                            "border-input data-[placeholder]:text-muted-foreground [&_svg:not([class*='text-'])]:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive dark:bg-input/30 dark:hover:bg-input/50 flex w-full items-center justify-between gap-2 rounded-md border bg-transparent px-3 py-2 text-sm whitespace-nowrap shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50 h-9 *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-2 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4"
                        )}
                    >
                        <SelectPrimitive.Value placeholder={placeholder} />
                        <SelectPrimitive.Icon asChild>
                            <ChevronDownIcon className="size-4 opacity-50" />
                        </SelectPrimitive.Icon>
                    </SelectPrimitive.Trigger>
                </div>

                <SelectPrimitive.Portal>
                    <SelectPrimitive.Content
                        className={cn(
                            "bg-popover text-popover-foreground data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 relative z-[1005] max-h-60 min-w-[var(--radix-select-trigger-width)] origin-[--radix-select-content-transform-origin] overflow-hidden rounded-md border shadow-md",
                            "data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1"
                        )}
                        position="popper"
                        align="start"
                        sideOffset={4}
                    >
                        <div className="sticky top-0 z-10 bg-popover border-b px-2 py-1.5 space-y-1">
                            <div className="relative">
                                <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
                                <Input
                                    placeholder="Search..."
                                    className="pl-8 h-7 text-xs"
                                    value={searchValue}
                                    onChange={(e) => setSearchValue(e.target.value)}
                                    onClick={(e) => e.stopPropagation()}
                                    onKeyDown={(e) => e.stopPropagation()}
                                    autoFocus
                                />
                            </div>
                        </div>

                        <SelectPrimitive.ScrollUpButton className="flex cursor-default items-center justify-center py-1">
                            <ChevronDownIcon className="size-4 rotate-180" />
                        </SelectPrimitive.ScrollUpButton>

                        <SelectPrimitive.Viewport
                            className="p-1 w-full"
                        >
                            {filteredOptions.length === 0 ? (
                                <div className="px-2 py-6 text-center text-sm text-muted-foreground">
                                    No options found
                                </div>
                            ) : (
                                filteredOptions.map((opt) => (
                                    <SelectPrimitive.Item
                                        key={opt.value}
                                        value={opt.value}
                                        className={cn(
                                            "focus:bg-accent focus:text-accent-foreground [&_svg:not([class*='text-'])]:text-muted-foreground relative flex w-full cursor-default items-center gap-2 rounded-sm py-1.5 pr-8 pl-2 text-sm outline-hidden select-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4"
                                        )}
                                    >
                                        <span
                                            className="absolute right-2 flex size-3.5 items-center justify-center"
                                        >
                                            <SelectPrimitive.ItemIndicator>
                                                <CheckIcon className="size-4" />
                                            </SelectPrimitive.ItemIndicator>
                                        </span>
                                        <SelectPrimitive.ItemText>{opt.label}</SelectPrimitive.ItemText>
                                    </SelectPrimitive.Item>
                                ))
                            )}
                        </SelectPrimitive.Viewport>

                        <SelectPrimitive.ScrollDownButton className="flex cursor-default items-center justify-center py-1">
                            <ChevronDownIcon className="size-4" />
                        </SelectPrimitive.ScrollDownButton>
                    </SelectPrimitive.Content>
                </SelectPrimitive.Portal>
            </SelectPrimitive.Root>
        </div>
    );
}
