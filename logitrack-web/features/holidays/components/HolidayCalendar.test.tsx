import { readFileSync } from "node:fs";
import path from "node:path";
import React from "react";
import { afterEach, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { default: HolidayCalendar } = await import("./HolidayCalendar");
const css = readFileSync(path.resolve("features/holidays/components/holidayCalendar.css"), "utf8");

afterEach(() => {
    document.querySelector("style[data-holiday-test]")?.remove();
});

it.each(["dayGridMonth", "multiMonthYear"])("keeps both same-day holiday labels visible and clickable in %s", async (view) => {
    const style = document.createElement("style");
    style.dataset.holidayTest = "";
    style.textContent = css;
    document.head.append(style);
    const eventClick = vi.fn();
    const dateClick = vi.fn();
    const user = userEvent.setup();
    render(
        <HolidayCalendar
            initialView={view}
            initialDate="2026-10-15"
            locale="en"
            height="auto"
            dayMaxEvents={10}
            events={[
                { id: "public", title: "Public holiday", start: "2026-10-15", allDay: true, display: "block" },
                { id: "company", title: "Company holiday", start: "2026-10-15", allDay: true, display: "block" },
            ]}
            eventClick={eventClick}
            dateClick={dateClick}
        />
    );

    for (const [index, title] of ["Public holiday", "Company holiday"].entries()) {
        const label = await screen.findByText(title);
        const event = label.closest<HTMLElement>(".fc-daygrid-event")!;
        expect(event.closest(`.fc-${view}-view`)).not.toBeNull();
        expect(getComputedStyle(event).opacity).not.toBe("0");
        expect(getComputedStyle(event).position).toBe("relative");
        await user.click(label);
        expect(eventClick).toHaveBeenCalledTimes(index + 1);
        expect(eventClick.mock.lastCall?.[0].event.id).toBe(index === 0 ? "public" : "company");
        expect(dateClick).not.toHaveBeenCalled();
    }
});
