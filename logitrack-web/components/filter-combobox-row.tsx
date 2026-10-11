"use client";

import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

/** One row of a `FilterCombobox` list (the plain list and the virtualised one). */
export function FilterComboboxRow({
    selected,
    onClick,
    label,
    count,
    badge,
}: {
    selected: boolean;
    onClick: () => void;
    label: string;
    count?: number;
    badge?: string;
}) {
    return (
        <div
            className="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
            onClick={onClick}
        >
            <Check className={cn("h-4 w-4 shrink-0", selected ? "opacity-100" : "opacity-0")} />
            <span className="min-w-0 flex-1 truncate">
                {label}
                {badge && <span className="ml-2 text-xs text-amber-600">{badge}</span>}
            </span>
            {typeof count === "number" && (
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{count}</span>
            )}
        </div>
    );
}
