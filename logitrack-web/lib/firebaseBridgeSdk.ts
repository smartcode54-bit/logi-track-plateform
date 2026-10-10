/**
 * The Firebase SDK calls of the bridge (lib/firebaseBridge.ts), loaded with `import()` on the first
 * sync. Named re-exports, not `import("firebase/auth")`: a dynamic import's namespace object marks
 * every export of the module as used, which kept all of `@firebase/auth` (about 20 KB gzip) in the
 * initial JS of every route that also loads `firebase/client` statically (developer-spec.md §10.11).
 */
export { auth } from "@/firebase/client";
export { getIdTokenResult, signInWithCustomToken, signOut } from "firebase/auth";
