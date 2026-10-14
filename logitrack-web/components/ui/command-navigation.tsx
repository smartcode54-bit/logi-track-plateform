"use client";

import type { RefObject } from "react";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/ui/command";

/** A navigation list whose caller supplies already filtered destinations in menu order. */
export function CommandNavigation({ inputRef, label, placeholder, emptyText, query, onQueryChange, items, onSelect }: {
    inputRef: RefObject<HTMLInputElement | null>;
    label: string;
    placeholder: string;
    emptyText: string;
    query: string;
    onQueryChange: (query: string) => void;
    items: readonly { value: string; label: string; group?: string }[];
    onSelect: (value: string) => void;
}) {
    return (
        <Command shouldFilter={false} vimBindings={false} label={label} className="min-h-0 rounded-none">
            <CommandInput ref={inputRef} value={query} onValueChange={onQueryChange}
                aria-label={label} placeholder={placeholder}
                className="text-base focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring" />
            <CommandList label={label} className="min-h-0 flex-1 p-2">
                <CommandEmpty>{emptyText}</CommandEmpty>
                {items.map((item) => (
                    <CommandItem key={item.value} value={item.value} onSelect={() => onSelect(item.value)}
                        className="min-h-11 gap-3 px-3 py-2 aria-selected:bg-primary aria-selected:text-primary-foreground data-[disabled=false]:pointer-events-auto data-[disabled=false]:opacity-100">
                        <span className="min-w-0 flex-1 break-words">{item.label}</span>
                        {item.group ? <span className="max-w-1/2 break-words text-right text-sm">{item.group}</span> : null}
                    </CommandItem>
                ))}
            </CommandList>
        </Command>
    );
}
