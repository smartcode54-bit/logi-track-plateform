"use client";

/**
 * The last error boundary (UI3D, issue #143): a crash of the root layout or the `/app` shell itself. It replaces the
 * whole document, so it brings its own `<html>` and `<body>` and depends on nothing that may have failed: no
 * providers, no dictionary, no shell. Thai and English together (the chosen language is not known here), a retry, the
 * way to the start page, and the reference support needs. Page crashes inside the console stay in the shell
 * (app/app/error.tsx).
 */
import { useEffect } from "react";
import Link from "next/link";

import "./globals.css";
import { errorReference } from "@/lib/errorReference";
import { GLOBAL_ERROR_TEXT } from "@/lib/globalErrorText";

const LANGUAGES = ["th", "en"] as const;

export default function GlobalError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
    const reference = errorReference(error);

    useEffect(() => {
        console.error(error);
    }, [error]);

    return (
        <html lang="th">
            <body className="min-h-screen bg-background text-foreground">
                <main className="mx-auto flex min-h-screen max-w-xl flex-col justify-center gap-6 p-gutter">
                    <h1 className="flex flex-col gap-1 text-2xl font-semibold tracking-tight">
                        {LANGUAGES.map((lang) => (
                            <span key={lang} lang={lang}>
                                {GLOBAL_ERROR_TEXT[lang].title}
                            </span>
                        ))}
                    </h1>
                    {LANGUAGES.map((lang) => (
                        <p key={lang} lang={lang} className="text-sm leading-relaxed text-muted-foreground">
                            {GLOBAL_ERROR_TEXT[lang].body}
                        </p>
                    ))}
                    <div className="flex flex-wrap gap-2">
                        <button
                            type="button"
                            onClick={reset}
                            className="inline-flex h-9 items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground outline-none hover:bg-primary/90 focus-visible:ring-[3px] focus-visible:ring-ring/50"
                        >
                            {GLOBAL_ERROR_TEXT.th.retry} · <span lang="en">{GLOBAL_ERROR_TEXT.en.retry}</span>
                        </button>
                        <Link
                            href="/"
                            prefetch={false}
                            className="inline-flex h-9 items-center justify-center rounded-md border bg-background px-4 text-sm font-medium shadow-xs outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50"
                        >
                            {GLOBAL_ERROR_TEXT.th.home} · <span lang="en">{GLOBAL_ERROR_TEXT.en.home}</span>
                        </Link>
                    </div>
                    {reference ? (
                        <p className="text-xs text-muted-foreground">
                            {GLOBAL_ERROR_TEXT.th.reference} <span lang="en">{GLOBAL_ERROR_TEXT.en.reference}</span>{" "}
                            <span className="font-mono">{reference}</span>
                        </p>
                    ) : null}
                </main>
            </body>
        </html>
    );
}
