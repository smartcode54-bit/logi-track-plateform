// T50 (issue #70), TW7: the chat console reads and writes through /api/go only. Each of the
// four routes and the layout render their Go module, whose import graph (static and lazy, test-utils/importGraph
// .ts) holds no Firebase module: no listener on chats or messages, no read of every driver, no sendBroadcast or
// notifyChatMessageCreated callable (AC). TW7 removed the Firestore console (features/chat/legacy) and the comms switch.
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
    it("renders only the Go module on every route and the layout (TW7: no Firestore console, no comms switch)", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toMatch(/const GoPage = dynamic\(/);
            expect(src, path.relative(root, r)).not.toMatch(/DomainPage|FirestorePage|useDomainPageSource|\/legacy\//);
        }
        const layout = readFileSync(LAYOUT, "utf8");
        expect(layout).not.toMatch(/DomainPage|FirestoreLayout|useDomainPageSource|\/legacy\//);
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
});
