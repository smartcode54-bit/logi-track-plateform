import { describe, it, expect, vi, beforeAll } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DateOnlyRangePicker, DateRangePicker } from "./date-range-picker";

// Radix positions the popover with these; jsdom ships neither.
beforeAll(() => {
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
    Element.prototype.scrollIntoView ??= () => {};
    // Radix guards its dismissable layer on pointer capture APIs jsdom lacks.
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
});

/** Day cells carry `data-day={day.date.toLocaleDateString()}` (see components/ui/calendar.tsx). */
function dayCell(date: Date): HTMLElement {
    const el = document.body.querySelector<HTMLElement>(
        `[data-day="${date.toLocaleDateString()}"]`
    );
    if (!el) throw new Error(`No day cell rendered for ${date.toLocaleDateString()}`);
    return el;
}

describe("DateRangePicker", () => {
    /**
     * The regression this component was built around: react-day-picker's addToRange() answers a
     * single click with a COMPLETE range whenever the calendar already holds one, so committing on
     * "range.from && range.to" fired on click one and closed the popover with a range the user
     * never picked. Picking a range then took several attempts.
     */
    it("does not commit on the first click when a range is already selected", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();

        render(
            <DateRangePicker
                from={new Date(2026, 7, 10)}
                to={new Date(2026, 7, 20)}
                onChange={onChange}
                numberOfMonths={1}
            />
        );

        await user.click(screen.getByRole("button", { name: /10\/08\/2026/ }));
        await user.click(dayCell(new Date(2026, 7, 5)));

        expect(onChange).not.toHaveBeenCalled();
    });

    it("commits the range the user actually clicked, on the second click", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();

        render(
            <DateRangePicker
                from={new Date(2026, 7, 10)}
                to={new Date(2026, 7, 20)}
                onChange={onChange}
                numberOfMonths={1}
            />
        );

        await user.click(screen.getByRole("button", { name: /10\/08\/2026/ }));
        await user.click(dayCell(new Date(2026, 7, 5)));
        await user.click(dayCell(new Date(2026, 7, 15)));

        expect(onChange).toHaveBeenCalledTimes(1);
        const [from, to] = onChange.mock.calls[0];
        expect(from.getDate()).toBe(5);
        expect(to.getDate()).toBe(15);
    });

    it("needs two clicks from an empty selection too, rather than committing a same-day range", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        // With no `from`, the calendar opens on the current month — pick days that always exist
        // there and are never rendered as outside days.
        const today = new Date();
        const start = new Date(today.getFullYear(), today.getMonth(), 10);
        const end = new Date(today.getFullYear(), today.getMonth(), 20);

        render(<DateRangePicker from={null} to={null} onChange={onChange} numberOfMonths={1} />);

        await user.click(screen.getByRole("button", { name: "—" }));
        await user.click(dayCell(start));
        expect(onChange).not.toHaveBeenCalled();

        await user.click(dayCell(end));
        expect(onChange).toHaveBeenCalledTimes(1);
        const [from, to] = onChange.mock.calls[0];
        expect(from.getDate()).toBe(10);
        expect(to.getDate()).toBe(20);
    });

    it("closes and reopens with a clean draft, so the next pick also takes two clicks", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();

        render(
            <DateRangePicker
                from={new Date(2026, 7, 10)}
                to={new Date(2026, 7, 20)}
                onChange={onChange}
                numberOfMonths={1}
            />
        );

        await user.click(screen.getByRole("button", { name: /10\/08\/2026/ }));
        await user.click(dayCell(new Date(2026, 7, 5)));
        await user.click(dayCell(new Date(2026, 7, 15)));
        expect(onChange).toHaveBeenCalledTimes(1);

        // Trigger label still reflects the props the parent was given, not the new pick.
        await user.click(screen.getByRole("button", { name: /10\/08\/2026/ }));
        await user.click(dayCell(new Date(2026, 7, 8)));
        expect(onChange).toHaveBeenCalledTimes(1);
    });
});

describe("DateOnlyRangePicker", () => {
    it("shows the range its `yyyy-MM-dd` props name", () => {
        render(<DateOnlyRangePicker from="2026-08-10" to="2026-08-20" onChange={vi.fn()} />);

        // Parsed at noon, not midnight: midnight-as-UTC would render 09/08 west of Greenwich.
        expect(screen.getByRole("button", { name: "10/08/2026 – 20/08/2026" })).toBeTruthy();
    });

    it("emits `yyyy-MM-dd` strings, the shape the callables and Firestore fields already take", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();

        render(
            <DateOnlyRangePicker
                from="2026-08-10"
                to="2026-08-20"
                onChange={onChange}
                numberOfMonths={1}
            />
        );

        await user.click(screen.getByRole("button", { name: /10\/08\/2026/ }));
        await user.click(dayCell(new Date(2026, 7, 5)));
        await user.click(dayCell(new Date(2026, 7, 15)));

        expect(onChange).toHaveBeenCalledTimes(1);
        expect(onChange).toHaveBeenCalledWith("2026-08-05", "2026-08-15");
    });

    it("opens on an empty range instead of treating an empty string as a date", () => {
        render(<DateOnlyRangePicker from="" to="" onChange={vi.fn()} placeholder="Date range" />);

        expect(screen.getByRole("button", { name: "Date range" })).toBeTruthy();
    });
});
