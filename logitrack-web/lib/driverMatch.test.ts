import { describe, expect, it } from "vitest";
import { matchImportDriver, normalizePhone, type MatchableDriver } from "./driverMatch";

const drivers: MatchableDriver[] = [
    { id: "d1", fullNameTh: "ทิวา พันธนา", mobile: "081-469-6727", status: "Active" },
    { id: "d2", fullNameTh: "สมคิด โชคลาภดี", mobile: "0899999999" },
    { id: "d3", fullNameTh: "ดุษฎี เรือนเย็น", mobile: "0615034157", status: "On-Duty" },
    { id: "d4", fullNameTh: "คนเก่า ลาออก", mobile: "0811111111", status: "Inactive" },
    { id: "d5", firstName: "Somchai", lastName: "Jaidee", mobile: "0822222222" },
];

describe("normalizePhone", () => {
    it("reduces common Thai phone spellings to local digits", () => {
        expect(normalizePhone("081-469-6727")).toBe("0814696727");
        expect(normalizePhone("+66 81 469 6727")).toBe("0814696727");
        expect(normalizePhone(814696727)).toBe("0814696727"); // Excel number cell, leading zero lost
        expect(normalizePhone("")).toBe("");
        expect(normalizePhone(undefined)).toBe("");
    });
});

describe("matchImportDriver", () => {
    it("matches by phone first", () => {
        const m = matchImportDriver(drivers, "0814696727", "ชื่อพิมพ์ผิด");
        expect(m).toEqual({ kind: "matched", driver: drivers[0], by: "phone" });
    });

    it("falls back to the Thai name, ignoring extra spaces", () => {
        const m = matchImportDriver(drivers, "", "  สมคิด   โชคลาภดี ");
        expect(m).toMatchObject({ kind: "matched", by: "name", driver: { id: "d2" } });
    });

    it("falls back to the name when the phone is not on file", () => {
        const m = matchImportDriver(drivers, "0800000000", "ดุษฎี เรือนเย็น");
        expect(m).toMatchObject({ kind: "matched", by: "name", driver: { id: "d3" } });
    });

    it("matches a latin firstName lastName case-insensitively", () => {
        expect(matchImportDriver(drivers, undefined, "somchai jaidee")).toMatchObject({ kind: "matched", driver: { id: "d5" } });
    });

    it("skips inactive drivers", () => {
        expect(matchImportDriver(drivers, "0811111111", "คนเก่า ลาออก")).toEqual({ kind: "notFound" });
    });

    it("reports none when the sheet names no driver", () => {
        expect(matchImportDriver(drivers, "", "  ")).toEqual({ kind: "none" });
    });

    it("refuses to guess between two drivers", () => {
        const shared: MatchableDriver[] = [
            { id: "a", fullNameTh: "ก ข", mobile: "0812345678" },
            { id: "b", fullNameTh: "ค ง", mobile: "0812345678" },
        ];
        expect(matchImportDriver(shared, "0812345678", "")).toEqual({ kind: "ambiguous" });
        expect(matchImportDriver(shared, "0812345678", "ค ง")).toMatchObject({ kind: "matched", driver: { id: "b" } });
        const twins: MatchableDriver[] = [{ id: "x", fullNameTh: "สมชาย" }, { id: "y", fullNameTh: "สมชาย" }];
        expect(matchImportDriver(twins, "", "สมชาย")).toEqual({ kind: "ambiguous" });
    });
});
