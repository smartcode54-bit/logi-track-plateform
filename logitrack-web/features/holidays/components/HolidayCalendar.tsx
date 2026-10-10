"use client";

import type { ComponentProps, Ref } from "react";
import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import multiMonthPlugin from "@fullcalendar/multimonth";
import interactionPlugin from "@fullcalendar/interaction";
import listPlugin from "@fullcalendar/list";

const PLUGINS = [dayGridPlugin, multiMonthPlugin, interactionPlugin, listPlugin];

export type HolidayCalendarProps = Omit<ComponentProps<typeof FullCalendar>, "plugins"> & {
    /** Gives the page the FullCalendar instance (`getApi()`) for view changes. */
    calendarRef?: Ref<FullCalendar>;
};

/**
 * FullCalendar with the plugins of the holidays page. The page loads this module with
 * `next/dynamic(..., { ssr: false })`, so the five `@fullcalendar/*` packages stay out of its
 * initial JS (developer-spec.md §10.11, Appendix E §E.7 row 7).
 */
export default function HolidayCalendar({ calendarRef, ...options }: HolidayCalendarProps) {
    return <FullCalendar ref={calendarRef} plugins={PLUGINS} {...options} />;
}
