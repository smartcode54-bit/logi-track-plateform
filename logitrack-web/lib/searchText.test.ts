import { describe, expect, it } from "vitest";

import { filterBySearch, foldSearchText, searchTerms } from "./searchText";

describe("foldSearchText", () => {
    it("folds case and Latin accents", () => {
        expect(foldSearchText("Café DÉPÔT")).toBe("cafe depot");
    });

    it("folds Thai tone marks but keeps vowel signs", () => {
        expect(foldSearchText("ห้วยขวาง")).toBe(foldSearchText("หวยขวาง"));
        expect(foldSearchText("ห้วยขวาง")).not.toBe(foldSearchText("หัวยขวาง"));
        // thanthakhat (การันต์) and maitaikhu fold too
        expect(foldSearchText("จันทร์")).toBe("จันทร");
        expect(foldSearchText("เป็น")).toBe("เปน");
    });

    it("reads Thai digits and full-width letters as ASCII", () => {
        expect(foldSearchText("ทะเบียน ๗๐-๑๒๓๔")).toBe("ทะเบียน 70-1234");
        expect(foldSearchText("ＳＰＫ")).toBe("spk");
    });

    it("collapses white space", () => {
        expect(foldSearchText("  J&T   EXPRESS\tบางปู ")).toBe("j&t express บางปู");
        expect(searchTerms("  ")).toEqual([]);
    });
});

describe("filterBySearch", () => {
    const hubs = ["SPK-GW J&T EXPRESS บางปู", "SOCE ศูนย์คัดแยกตะวันออก", "SPK890174 ห้วยขวาง10", "XSPK warehouse"];

    it("matches every word in any order, Thai and English", () => {
        expect(filterBySearch(hubs, "บางปู j&t", (s) => s)).toEqual(["SPK-GW J&T EXPRESS บางปู"]);
        expect(filterBySearch(hubs, "หวยขวาง", (s) => s)).toEqual(["SPK890174 ห้วยขวาง10"]);
    });

    it("puts rows with a word starting with the first term first, otherwise keeps order", () => {
        expect(filterBySearch(hubs, "spk", (s) => s)).toEqual(["SPK-GW J&T EXPRESS บางปู", "SPK890174 ห้วยขวาง10", "XSPK warehouse"]);
    });

    it("returns every item for a blank query", () => {
        expect(filterBySearch(hubs, " ", (s) => s)).toEqual(hubs);
    });
});
