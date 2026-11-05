"use client";

import { useState } from "react";
import { Plus, Trash2, GripVertical, Check, ChevronsUpDown, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FormLabel } from "@/components/ui/form";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { placeMatchesQuery } from "@/lib/customerPlaces";
import { useLanguage } from "@/context/language";
import { useFormContext } from "react-hook-form";
import type { DeliveryStop } from "@/validate/taskSchema";

/** Rows rendered per search; narrow the search to reach the rest. */
const MAX_STOP_RESULTS = 100;

interface DeliveryStopsEditorProps {
    /**
     * Places a stop may go to — the caller decides the set (FM: SOCs, LH: the job customer's hubs).
     * A multi-delivery job belongs to one customer, so there is no per-stop customer picker: the
     * stop's customer link is derived from its hub doc when the task is saved.
     */
    destinationOptions: Array<{ code: string; name: string }>;
    placeholder: string;
    /** Placeholder of the search box inside each stop's picker. */
    searchPlaceholder: string;
    /** Shown in the picker when the search matches nothing. */
    noMatchMessage: string;
    /** Shown when `destinationOptions` is empty (e.g. LH before a customer is chosen). */
    emptyOptionsMessage?: string;
}

export default function DeliveryStopsEditor({
    destinationOptions,
    placeholder,
    searchPlaceholder,
    noMatchMessage,
    emptyOptionsMessage,
}: DeliveryStopsEditorProps) {
    const { t } = useLanguage();
    const form = useFormContext();
    const stops = form.watch("deliveryStops") as DeliveryStop[] | undefined;
    const stopsError = form.formState.errors.deliveryStops?.message;
    const [draggedIndex, setDraggedIndex] = useState<number | null>(null);
    /** Index of the stop whose picker is open — one at a time. */
    const [openStop, setOpenStop] = useState<number | null>(null);
    const [stopSearch, setStopSearch] = useState("");
    const noOptions = destinationOptions.length === 0;

    const addStop = () => {
        const currentStops = stops || [];
        const newIndex = currentStops.length + 1;
        const newStop: DeliveryStop = {
            index: newIndex,
            destination: "",
            status: "pending",
        };
        form.setValue("deliveryStops", [...currentStops, newStop]);
    };

    const removeStop = (index: number) => {
        const currentStops = stops || [];
        const filtered = currentStops
            .filter((_, i) => i !== index)
            .map((stop, i) => ({ ...stop, index: i + 1 }));
        form.setValue("deliveryStops", filtered);
    };

    const updateStopDestination = (index: number, destination: string) => {
        const currentStops = [...(stops || [])];
        currentStops[index] = { ...currentStops[index], destination };
        form.setValue("deliveryStops", currentStops);
        form.clearErrors("deliveryStops");
    };

    const moveStop = (fromIndex: number, toIndex: number) => {
        const currentStops = [...(stops || [])];
        const [movedStop] = currentStops.splice(fromIndex, 1);
        currentStops.splice(toIndex, 0, movedStop);
        const reindexed = currentStops.map((stop, i) => ({ ...stop, index: i + 1 }));
        form.setValue("deliveryStops", reindexed);
        setDraggedIndex(null);
    };

    const optionsNotice = noOptions && emptyOptionsMessage && (
        <p className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded-md px-3 py-2">
            {emptyOptionsMessage}
        </p>
    );
    const errorNotice = typeof stopsError === "string" && (
        <p className="text-sm font-medium text-destructive">{stopsError}</p>
    );

    if (!stops || stops.length === 0) {
        return (
            <div className="space-y-2">
                {optionsNotice}
                <div className="border border-dashed rounded-md p-4 text-center">
                    <p className="text-sm text-muted-foreground mb-2">
                        {t("firstMile.task.noStops")}
                    </p>
                    <Button type="button" variant="outline" size="sm" onClick={addStop} disabled={noOptions}>
                        <Plus className="h-4 w-4 mr-2" />
                        {t("firstMile.task.addStop")}
                    </Button>
                </div>
                {errorNotice}
            </div>
        );
    }

    return (
        <div className="space-y-3">
            {optionsNotice}
            {stops.map((stop, idx) => {
                const selected = destinationOptions.find((opt) => opt.code === stop.destination);
                const takenByOtherStops = new Set(
                    stops.filter((_, i) => i !== idx).map((s) => s.destination).filter(Boolean)
                );
                const isOpen = openStop === idx;
                const matches = isOpen
                    ? destinationOptions.filter((opt) => placeMatchesQuery(opt, stopSearch))
                    : [];
                const pick = (code: string) => {
                    if (takenByOtherStops.has(code)) return;
                    updateStopDestination(idx, code);
                    setOpenStop(null);
                };
                return (
                    <div
                        key={idx}
                        className={`border rounded-md p-3 space-y-3 transition-opacity ${
                            draggedIndex === idx ? "opacity-50" : ""
                        }`}
                        draggable
                        onDragStart={() => setDraggedIndex(idx)}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={() => {
                            if (draggedIndex !== null && draggedIndex !== idx) {
                                moveStop(draggedIndex, idx);
                            }
                        }}
                    >
                        <div className="flex items-center gap-2">
                            <GripVertical className="h-4 w-4 text-muted-foreground cursor-move" />
                            <span className="font-semibold text-sm">
                                {t("firstMile.task.stopIndex", { index: idx + 1 })}
                            </span>
                            {idx === 0 && (
                                <span className="text-xs bg-blue-100 text-blue-700 px-2 py-1 rounded">
                                    {t("firstMile.task.primaryDestination")}
                                </span>
                            )}
                            <div className="flex-1" />
                            <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onClick={() => removeStop(idx)}
                                className="text-red-600 hover:text-red-700"
                            >
                                <Trash2 className="h-4 w-4" />
                                {t("firstMile.task.removeStop")}
                            </Button>
                        </div>

                        <div>
                            <FormLabel className="text-xs">
                                {t("firstMile.task.stopDestination")}
                            </FormLabel>
                            {/* Searchable: an LH customer can have 100+ hubs. An unlisted saved value shows the placeholder. */}
                            <Popover
                                open={isOpen}
                                onOpenChange={(open) => {
                                    setOpenStop(open ? idx : null);
                                    if (open) setStopSearch("");
                                }}
                                modal={true}
                            >
                                <PopoverTrigger asChild>
                                    <Button
                                        type="button"
                                        variant="outline"
                                        role="combobox"
                                        aria-expanded={isOpen}
                                        disabled={noOptions}
                                        className={cn(
                                            "w-full justify-between h-auto min-h-9 py-2 font-normal",
                                            !selected && "text-muted-foreground"
                                        )}
                                    >
                                        <span className="block truncate text-left flex-1 min-w-0">
                                            {selected ? selected.name || selected.code : placeholder}
                                        </span>
                                        <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50 ml-2" />
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent
                                    className="w-[var(--radix-popover-trigger-width)] p-0 z-[1010]"
                                    align="start"
                                >
                                    <div className="flex items-center border-b px-3">
                                        <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                                        <input
                                            className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                            placeholder={searchPlaceholder}
                                            value={stopSearch}
                                            onChange={(e) => setStopSearch(e.target.value)}
                                            onKeyDown={(e) => {
                                                if (e.key !== "Enter") return;
                                                e.preventDefault();
                                                const first = matches.find((opt) => !takenByOtherStops.has(opt.code));
                                                if (first) pick(first.code);
                                            }}
                                            autoFocus
                                        />
                                    </div>
                                    <div role="listbox" className="max-h-[240px] overflow-y-auto p-1">
                                        {matches.length === 0 ? (
                                            <div className="py-4 text-center text-sm text-muted-foreground">
                                                {noMatchMessage}
                                            </div>
                                        ) : (
                                            matches.slice(0, MAX_STOP_RESULTS).map((opt) => {
                                                const taken = takenByOtherStops.has(opt.code);
                                                const isSelected = opt.code === stop.destination;
                                                return (
                                                    <div
                                                        key={opt.code}
                                                        role="option"
                                                        aria-selected={isSelected}
                                                        aria-disabled={taken}
                                                        onClick={() => pick(opt.code)}
                                                        className={cn(
                                                            "relative flex select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none",
                                                            taken
                                                                ? "cursor-not-allowed opacity-50"
                                                                : "cursor-default hover:bg-accent hover:text-accent-foreground",
                                                            isSelected && "bg-accent"
                                                        )}
                                                    >
                                                        <Check
                                                            className={cn(
                                                                "mr-2 h-4 w-4 shrink-0",
                                                                isSelected ? "opacity-100" : "opacity-0"
                                                            )}
                                                        />
                                                        <span className="block leading-tight flex-1 min-w-0">
                                                            {opt.name || opt.code}
                                                        </span>
                                                        {opt.name && opt.name !== opt.code && (
                                                            <span className="ml-2 shrink-0 text-xs text-muted-foreground">
                                                                {opt.code}
                                                            </span>
                                                        )}
                                                    </div>
                                                );
                                            })
                                        )}
                                    </div>
                                </PopoverContent>
                            </Popover>
                            {stop.destination && !selected && (
                                <p className="text-xs text-amber-700 mt-1">
                                    {t("firstMile.task.stopDestinationNotListed", { destination: stop.destination })}
                                </p>
                            )}
                        </div>
                    </div>
                );
            })}

            {errorNotice}

            <Button type="button" variant="outline" size="sm" onClick={addStop} disabled={noOptions} className="w-full">
                <Plus className="h-4 w-4 mr-2" />
                {t("firstMile.task.addStop")}
            </Button>
        </div>
    );
}
