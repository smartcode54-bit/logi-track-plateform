"use client"

import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * Labels every body cell of `table` with its column's header (`data-label`), for the phone layout of a stacked
 * table (app/globals.css, DESIGN.md Tables): below md each row is a card, one "label value" line per cell and the
 * first cell as the card's title, so the first column and a cell spanning columns get no label. It follows the
 * rows and the header text as they change (a new page, a language switch).
 */
export function useStackedCellLabels(table: React.RefObject<HTMLTableElement | null>, enabled = true) {
  React.useEffect(() => {
    const element = table.current
    if (!enabled || !element) return
    const apply = () => {
      const head = element.tHead?.rows[element.tHead.rows.length - 1]
      const labels: string[] = []
      for (const th of Array.from(head?.cells ?? [])) {
        labels.push(th.textContent?.trim() ?? "")
        for (let i = 1; i < th.colSpan; i++) labels.push("")
      }
      for (const body of Array.from(element.tBodies)) {
        for (const row of Array.from(body.rows)) {
          let column = 0
          for (const cell of Array.from(row.cells)) {
            const label = column > 0 && cell.colSpan === 1 ? labels[column] : ""
            if (label) {
              if (cell.dataset.label !== label) cell.dataset.label = label
            } else if (cell.dataset.label !== undefined) {
              delete cell.dataset.label
            }
            column += cell.colSpan
          }
        }
      }
    }
    apply()
    // Rows and header text only: the labels written here are attributes, so they never wake the observer.
    const observer = new MutationObserver(apply)
    observer.observe(element, { childList: true, subtree: true, characterData: true })
    return () => observer.disconnect()
  }, [table, enabled])
}

/** `stack`: below md the rows become cards (a list a customer or partner reads on a phone); wider screens keep the table. */
function Table({ className, stack = false, ...props }: React.ComponentProps<"table"> & { stack?: boolean }) {
  const ref = React.useRef<HTMLTableElement>(null)
  useStackedCellLabels(ref, stack)
  return (
    <div
      data-slot="table-container"
      className="relative w-full overflow-x-auto"
    >
      <table
        ref={ref}
        data-slot="table"
        data-stack={stack ? "mobile" : undefined}
        className={cn("w-full caption-bottom text-sm", className)}
        {...props}
      />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <thead
      data-slot="table-header"
      className={cn("[&_tr]:border-b", className)}
      {...props}
    />
  )
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return (
    <tbody
      data-slot="table-body"
      className={cn("[&_tr:last-child]:border-0", className)}
      {...props}
    />
  )
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      data-slot="table-footer"
      className={cn(
        "bg-muted/50 border-t font-medium [&>tr]:last:border-b-0",
        className
      )}
      {...props}
    />
  )
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn(
        "hover:bg-muted/50 data-[state=selected]:bg-muted border-b transition-colors",
        className
      )}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        "text-foreground h-10 px-2 text-left align-middle font-medium whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        className
      )}
      {...props}
    />
  )
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return (
    <td
      data-slot="table-cell"
      className={cn(
        "p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        className
      )}
      {...props}
    />
  )
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("text-muted-foreground mt-4 text-sm", className)}
      {...props}
    />
  )
}

export {
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
}
