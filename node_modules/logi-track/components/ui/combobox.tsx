"use client"

import * as React from "react"
import { Check, ChevronsUpDown, Search } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover"

export interface ComboboxOption {
    value: string
    label: string
}

interface ComboboxProps {
    options: ComboboxOption[] | readonly string[]
    value?: string
    onSelect: (value: string) => void
    placeholder?: string
    searchPlaceholder?: string
    emptyText?: string
    className?: string
}

export function Combobox({
    options,
    value,
    onSelect,
    placeholder = "Select option",
    searchPlaceholder = "Search...",
    emptyText = "No option found.",
    className,
}: ComboboxProps) {
    const [open, setOpen] = React.useState(false)
    const [searchQuery, setSearchQuery] = React.useState("")

    // Normalize options to object array
    const formattedOptions = React.useMemo(() => {
        if (options.length > 0 && typeof options[0] === 'string') {
            return (options as readonly string[]).map(opt => ({ value: opt, label: opt }))
        }
        return options as ComboboxOption[]
    }, [options])

    // Filter options based on search query
    const filteredOptions = React.useMemo(() => {
        if (!searchQuery.trim()) {
            return formattedOptions
        }
        const query = searchQuery.toLowerCase()
        return formattedOptions.filter(option =>
            option.label.toLowerCase().includes(query) ||
            option.value.toLowerCase().includes(query)
        )
    }, [formattedOptions, searchQuery])

    const selectedLabel = React.useMemo(() => {
        const option = formattedOptions.find((opt) => opt.value === value)
        return option ? option.label : placeholder
    }, [value, formattedOptions, placeholder])

    const handleSelect = (optionValue: string) => {
        const newValue = value === optionValue ? "" : optionValue
        onSelect(newValue)
        setOpen(false)
        setSearchQuery("")
    }

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button
                    variant="outline"
                    role="combobox"
                    aria-expanded={open}
                    type="button"
                    className={cn("w-full justify-between", className)}
                >
                    <span className={cn(!value ? "text-muted-foreground" : "")}>
                        {selectedLabel}
                    </span>
                    <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                </Button>
            </PopoverTrigger>
            <PopoverContent className="w-[300px] p-0" align="start">
                <div className="flex items-center border-b px-3">
                    <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                    <Input
                        placeholder={searchPlaceholder}
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                        className="border-0 focus-visible:ring-0 focus-visible:ring-offset-0 h-11"
                        autoFocus
                    />
                </div>
                <div className="max-h-[300px] overflow-y-auto overflow-x-hidden p-1">
                    {filteredOptions.length === 0 ? (
                        <div className="py-6 text-center text-sm text-muted-foreground">
                            {emptyText}
                        </div>
                    ) : (
                        <div className="space-y-1">
                            {filteredOptions.map((option) => {
                                const isSelected = value === option.value
                                return (
                                    <button
                                        key={option.value}
                                        type="button"
                                        onClick={() => handleSelect(option.value)}
                                        className={cn(
                                            "relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none",
                                            "hover:bg-accent hover:text-accent-foreground",
                                            "focus:bg-accent focus:text-accent-foreground",
                                            isSelected && "bg-accent text-accent-foreground"
                                        )}
                                    >
                                        <Check
                                            className={cn(
                                                "mr-2 h-4 w-4",
                                                isSelected ? "opacity-100" : "opacity-0"
                                            )}
                                        />
                                        {option.label}
                                    </button>
                                )
                            })}
                        </div>
                    )}
                </div>
            </PopoverContent>
        </Popover>
    )
}
