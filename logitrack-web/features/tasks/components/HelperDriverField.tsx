"use client";

import { useMemo, useState } from "react";
import { Check, ChevronsUpDown, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { Driver } from "@/validate/driverSchema";

export interface HelperDriverFieldProps {
    /** All drivers (the picker lists only those with an authId). */
    drivers: Driver[];
    /** Current value: helperDriverIds (Auth UID array, length 0..1). */
    value: string[] | undefined | null;
    /** Emits the next helperDriverIds (length 0 or 1). */
    onChange: (next: string[]) => void;
    /** Main driver's doc id — excluded from the helper options. */
    excludeDriverId?: string;
    /** Main driver's Auth UID — excluded from the helper options. */
    excludeAuthId?: string;
    disabled?: boolean;
    label: string;
    placeholder: string;
    noneLabel: string;
    searchPlaceholder: string;
}

/**
 * Single-helper selector. The data model (`tasks.helperDriverIds`) is an array
 * only for Firestore `array-contains` index compatibility — see ADR-0001 — so
 * this field deliberately allows at most ONE helper and stores the driver's
 * Auth UID (the orchestrator matches helper pay by authId).
 */
export function HelperDriverField({
    drivers,
    value,
    onChange,
    excludeDriverId,
    excludeAuthId,
    disabled,
    label,
    placeholder,
    noneLabel,
    searchPlaceholder,
}: HelperDriverFieldProps) {
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState("");

    const selectedAuthId = value?.[0] ?? "";

    const options = useMemo(
        () =>
            drivers.filter((d) => {
                if (!d.authId) return false; // pay requires an authId match
                if (excludeAuthId && d.authId === excludeAuthId) return false;
                if (excludeDriverId && d.id === excludeDriverId) return false;
                return true;
            }),
        [drivers, excludeAuthId, excludeDriverId]
    );

    const nameOf = (authId: string) => {
        const d = drivers.find((x) => x.authId === authId);
        return d ? `${d.firstName} ${d.lastName}`.trim() : authId;
    };

    const filtered = useMemo(() => {
        const q = search.trim().toLowerCase();
        if (!q) return options;
        return options.filter((d) =>
            `${d.firstName} ${d.lastName}`.toLowerCase().includes(q)
        );
    }, [options, search]);

    return (
        <div className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">{label}</span>
            <Popover open={open} onOpenChange={(o) => { setOpen(o); if (o) setSearch(""); }}>
                <PopoverTrigger asChild>
                    <Button
                        type="button"
                        variant="outline"
                        role="combobox"
                        disabled={disabled}
                        className={cn(
                            "w-full justify-between font-normal",
                            !selectedAuthId && "text-muted-foreground"
                        )}
                    >
                        <span className="truncate">
                            {selectedAuthId ? nameOf(selectedAuthId) : placeholder}
                        </span>
                        <span className="flex items-center gap-1 shrink-0">
                            {selectedAuthId && !disabled && (
                                <X
                                    className="h-4 w-4 opacity-60 hover:opacity-100"
                                    onClick={(e) => {
                                        e.preventDefault();
                                        e.stopPropagation();
                                        onChange([]);
                                    }}
                                />
                            )}
                            <ChevronsUpDown className="h-4 w-4 opacity-50" />
                        </span>
                    </Button>
                </PopoverTrigger>
                <PopoverContent className="w-[--radix-popover-trigger-width] p-0 z-[1010]" align="start">
                    <div className="flex items-center border-b px-3">
                        <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                        <input
                            className="flex h-10 w-full bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                            placeholder={searchPlaceholder}
                            value={search}
                            onChange={(e) => setSearch(e.target.value)}
                            autoFocus
                        />
                    </div>
                    <div className="max-h-[220px] overflow-y-auto p-1">
                        <div
                            className="relative flex cursor-pointer select-none items-center rounded-sm px-2 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                            onClick={() => { onChange([]); setOpen(false); }}
                        >
                            <Check className={cn("mr-2 h-4 w-4", !selectedAuthId ? "opacity-100" : "opacity-0")} />
                            {noneLabel}
                        </div>
                        {filtered.map((d) => {
                            const authId = d.authId!;
                            return (
                                <div
                                    key={authId}
                                    className="relative flex cursor-pointer select-none items-center rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
                                    onClick={() => { onChange([authId]); setOpen(false); }}
                                >
                                    <Check className={cn("mr-2 h-4 w-4", authId === selectedAuthId ? "opacity-100" : "opacity-0")} />
                                    {d.firstName} {d.lastName}
                                </div>
                            );
                        })}
                    </div>
                </PopoverContent>
            </Popover>
        </div>
    );
}
