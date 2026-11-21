import { describe, expect, it } from "vitest";
import { act, render } from "@testing-library/react";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./table";

function List({ rows, header = "Plate", stack = true }: { rows: string[]; header?: string; stack?: boolean }) {
    return (
        <Table stack={stack}>
            <TableHeader>
                <TableRow>
                    <TableHead>Truck</TableHead>
                    <TableHead>{header}</TableHead>
                    <TableHead />
                </TableRow>
            </TableHeader>
            <TableBody>
                {rows.length === 0 ? (
                    <TableRow>
                        <TableCell colSpan={3}>No trucks</TableCell>
                    </TableRow>
                ) : (
                    rows.map((row) => (
                        <TableRow key={row}>
                            <TableCell>{row}</TableCell>
                            <TableCell>{`${row}-plate`}</TableCell>
                            <TableCell>…</TableCell>
                        </TableRow>
                    ))
                )}
            </TableBody>
        </Table>
    );
}

const labels = (container: HTMLElement) =>
    Array.from(container.querySelectorAll("tbody tr")).map((row) => Array.from(row.querySelectorAll("td")).map((cell) => cell.dataset.label ?? null));

describe("Table stack", () => {
    it("labels each body cell with its column header, leaving the title column and blank headers unlabelled", () => {
        const { container } = render(<List rows={["A", "B"]} />);
        expect(container.querySelector("table")?.dataset.stack).toBe("mobile");
        expect(labels(container)).toEqual([
            [null, "Plate", null],
            [null, "Plate", null],
        ]);
    });

    it("follows new rows and a renamed header, and never labels a cell spanning the row", async () => {
        const { container, rerender } = render(<List rows={[]} />);
        expect(labels(container)).toEqual([[null]]);
        await act(async () => rerender(<List rows={["A"]} header="ทะเบียน" />));
        expect(labels(container)).toEqual([[null, "ทะเบียน", null]]);
    });

    it("leaves a plain table alone", () => {
        const { container } = render(<List rows={["A"]} stack={false} />);
        expect(container.querySelector("table")?.dataset.stack).toBeUndefined();
        expect(labels(container)).toEqual([[null, null, null]]);
    });
});
