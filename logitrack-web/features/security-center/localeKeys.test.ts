// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

import securityEn from "@/context/locales/en/securityCenter";
import securityTh from "@/context/locales/th/securityCenter";
import usersEn from "@/context/locales/en/users";
import usersTh from "@/context/locales/th/users";

const ROOT = path.resolve(__dirname, "../..");
// These suffixes are selected from API values or fixed lists at runtime.
const DYNAMIC_PREFIXES = [
    "securityCenter.apiKeys.scopes.", "securityCenter.apiKeys.scopeHint.", "securityCenter.apiKeys.status.",
    "securityCenter.status.state.", "securityCenter.severity.", "securityCenter.securityEvents.types.",
    "securityCenter.roles.group.", "users.tenantRole.", "users.platformRole.", "users.accountStatus.", "users.account.locked.",
];

function sources(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
        const file = path.join(dir, entry.name);
        if (entry.isDirectory()) return ["locales", "__tests__", "test-utils"].includes(entry.name) ? [] : sources(file);
        return /\.(ts|tsx)$/.test(entry.name) && !/\.(test|spec)\./.test(entry.name) ? [file] : [];
    });
}

function referencedKeys() {
    const keys = new Set<string>();
    const templates = new Set<string>();
    for (const file of ["app", "components", "features", "hooks", "lib", "context"].flatMap((dir) => sources(path.join(ROOT, dir)))) {
        const tree = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
        const visit = (node: ts.Node) => {
            if (ts.isStringLiteralLike(node)) keys.add(node.text);
            if (ts.isTemplateExpression(node)) templates.add(node.head.text);
            ts.forEachChild(node, visit);
        };
        visit(tree);
    }
    return { keys, templates };
}

describe("Security and users locale keys", () => {
    it("keeps English and Thai keys in parity", () => {
        expect(Object.keys(securityEn).sort()).toEqual(Object.keys(securityTh).sort());
        expect(Object.keys(usersEn).sort()).toEqual(Object.keys(usersTh).sort());
    });

    it("has a production reference for every key, including the navigation catalogue", () => {
        const { keys, templates } = referencedKeys();
        expect(DYNAMIC_PREFIXES.filter((prefix) => !templates.has(prefix)), "stale dynamic allowances").toEqual([]);
        const dictionary = { ...securityEn, ...usersEn };
        const missing = [...keys].filter((key) => /^(securityCenter|users)\./.test(key) && !Object.hasOwn(dictionary, key));
        expect(missing, "referenced keys must remain defined").toEqual([]);
        const unused = Object.keys({ ...securityEn, ...usersEn }).filter((key) =>
            !keys.has(key) && !DYNAMIC_PREFIXES.some((prefix) => key.startsWith(prefix))
        );
        expect(unused).toEqual([]);
    });
});
