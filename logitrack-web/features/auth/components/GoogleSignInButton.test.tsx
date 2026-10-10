// T18 (Appendix C §C.4.10): GIS keeps one configuration per page (the last `initialize` wins), and the
// landing page mounts several buttons (the Hero's and one per open login dialog). The fake GIS below
// follows that rule: a click delivers a credential whose nonce is the configured one, to the configured
// callback.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, render, waitFor } from "@testing-library/react";

import { GoogleSignInButton, resetGoogleSignInForTests, type GoogleAccountsId } from "./GoogleSignInButton";
import { LanguageProvider } from "@/context/language";
import { fakeWeb, json } from "@/test-utils/fakeWeb";

type Config = Parameters<GoogleAccountsId["initialize"]>[0];

function fakeGis() {
    const state = { config: null as Config | null, initializes: 0, rendered: 0 };
    const gis: GoogleAccountsId = {
        initialize: (config) => {
            state.config = config;
            state.initializes += 1;
        },
        renderButton: () => {
            state.rendered += 1;
        },
    };
    window.google = { accounts: { id: gis } };
    return {
        state,
        /** The user completes the Google popup: GIS calls the page-wide callback. */
        click() {
            const c = state.config;
            if (!c) throw new Error("GIS not initialised");
            c.callback({ credential: `token(nonce=${c.nonce})` });
        },
    };
}

function Button({ onCredential }: { onCredential: (idToken: string, nonce: string) => Promise<void> }) {
    return (
        <LanguageProvider>
            <GoogleSignInButton onCredential={onCredential} />
        </LanguageProvider>
    );
}

let issued: string[] = [];
beforeEach(() => {
    resetGoogleSignInForTests();
    vi.stubEnv("NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID", "client-1.apps.googleusercontent.com");
    issued = [];
    const web = fakeWeb();
    web.on("GET", "/api/auth/google/nonce", () => {
        const nonce = `N${issued.length + 1}`;
        issued.push(nonce);
        return json(200, { data: { nonce, expiresIn: 300 } });
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    delete window.google;
});

describe("GoogleSignInButton", () => {
    it("hands the credential to the button still on screen after a dialog's button unmounts, with a fresh nonce", async () => {
        const gis = fakeGis();
        const hero = vi.fn(async () => undefined);
        const dialog = vi.fn(async () => {
            throw new Error("no_account");
        });
        render(<Button onCredential={hero} />);
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N1"));

        // A login dialog opens: its button owns the page-wide configuration while it is shown.
        const opened = render(<Button onCredential={dialog} />);
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N2"));

        // The dialog closes: the Hero button takes the configuration back with a new nonce of its own.
        opened.unmount();
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N3"));
        act(() => gis.click());
        await waitFor(() => expect(hero).toHaveBeenCalledTimes(1));
        expect(hero).toHaveBeenCalledWith("token(nonce=N3)", "N3");
        expect(dialog).not.toHaveBeenCalled();
    });

    it("a failed attempt refreshes the nonce of the button whose handler ran, so the next attempt never reuses a spent one", async () => {
        const gis = fakeGis();
        let fail = true;
        const seen: string[] = [];
        const hero = vi.fn(async (_token: string, nonce: string) => {
            seen.push(nonce);
            if (fail) throw new Error("account_disabled");
        });
        render(<Button onCredential={hero} />);
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N1"));
        act(() => gis.click());
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N2"));
        fail = false;
        act(() => gis.click());
        await waitFor(() => expect(hero).toHaveBeenCalledTimes(2));
        expect(seen).toEqual(["N1", "N2"]);
    });

    it("keeps the newest mounted button in charge while an older one renders again", async () => {
        const gis = fakeGis();
        const older = vi.fn(async () => {
            throw new Error("invalid_token");
        });
        const newer = vi.fn(async () => undefined);
        render(<Button onCredential={older} />);
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N1"));
        render(<Button onCredential={newer} />);
        await waitFor(() => expect(gis.state.config?.nonce).toBe("N2"));
        act(() => gis.click());
        await waitFor(() => expect(newer).toHaveBeenCalledWith("token(nonce=N2)", "N2"));
        expect(older).not.toHaveBeenCalled();
    });
});
