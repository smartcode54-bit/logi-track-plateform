// T50 (issue #70): with the `comms` flag on, the chat console reads and writes through /api/go only. Each of the
// four routes and the layout render their Go module, whose import graph (static and lazy, test-utils/importGraph
// .ts) holds no Firebase module: no listener on chats or messages, no read of every driver, no sendBroadcast or
// notifyChatMessageCreated callable (AC). The Firestore console keeps them (features/chat/legacy, removed with TW7).
import { readFileSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

import { firebaseImports, goPageModule, resolveImport, WEB_ROOT as root } from "@/test-utils/importGraph";

const ROUTES = ["app/app/chat/page.tsx", "app/app/chat/room/page.tsx", "app/app/chat/with-driver/page.tsx"].map((r) => path.join(root, r));
const LAYOUT = path.join(root, "app/app/chat/layout.tsx");

function goLayoutModule(): string {
    const m = /const GoLayout = dynamic\(\(\) => import\(["']([^"']+)["']\)/.exec(readFileSync(LAYOUT, "utf8"));
    if (!m) throw new Error("app/app/chat/layout.tsx: no GoLayout");
    const file = resolveImport(LAYOUT, m[1]);
    if (!file) throw new Error(`cannot resolve ${m[1]}`);
    return file;
}

describe("the Go chat console's import graph (T50)", () => {
    it("switches every route and the layout on the comms flag", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toContain('<DomainPage domain="comms"');
            expect(src, path.relative(root, r)).toMatch(/const FirestorePage = dynamic\(\(\) => import\("@\/features\/chat\/legacy\//);
        }
        const layout = readFileSync(LAYOUT, "utf8");
        expect(layout).toContain('useDomainPageSource("comms")');
        expect(layout).toMatch(/const FirestoreLayout = dynamic\(\(\) => import\("@\/features\/chat\/legacy\/LegacyChatLayout"\)/);
        expect(ROUTES.map((r) => path.relative(root, goPageModule(r)))).toEqual([
            "features/chat/components/ChatHomeGo.tsx",
            "features/chat/components/ChatRoomGo.tsx",
            "features/chat/components/ChatWithDriverGo.tsx",
        ]);
        expect(path.relative(root, goLayoutModule())).toBe("features/chat/components/ChatLayoutGo.tsx");
    });

    it.each([...ROUTES.map((r) => [path.relative(root, r), goPageModule(r)]), ["app/app/chat/layout.tsx", goLayoutModule()]])(
        "%s: the Go module imports no Firebase module, statically or lazily",
        (_name, entry) => {
            expect(firebaseImports(entry)).toEqual([]);
        }
    );

    it("the Firestore console still reaches Firestore (what the flag falls back to until TW7)", () => {
        for (const legacy of ["LegacyChatLayout.tsx", "LegacyChatRoomPage.tsx", "LegacyChatWithDriverPage.tsx", "LegacyBroadcastComposer.tsx"]) {
            expect(firebaseImports(path.join(root, "features/chat/legacy", legacy)).length, legacy).toBeGreaterThan(0);
        }
    });
});
