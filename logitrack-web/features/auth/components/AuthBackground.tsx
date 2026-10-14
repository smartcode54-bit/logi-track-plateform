/**
 * The background of the sign-in pages (UI3, issue #143), painted once by app/(auth)/layout.tsx. To change the
 * picture, replace the one file at `public/auth/sign-in-bg.webp` (WebP, about 75 % quality); nothing else refers
 * to it. A scrim in the page colour keeps it quiet behind the solid sign-in card in light and dark themes; no
 * blur or translucent panel sits over content.
 */
export const SIGN_IN_BACKGROUND_SRC = "/auth/sign-in-bg.webp";

export function AuthBackground() {
    return (
        <div className="pointer-events-none absolute inset-0 -z-10" aria-hidden data-testid="auth-background">
            {/* Decoration only; a plain img keeps next/image's runtime out of the sign-in route. */}
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={SIGN_IN_BACKGROUND_SRC} alt="" decoding="async" className="h-full w-full object-cover" />
            <div className="absolute inset-0 bg-background/80" />
        </div>
    );
}
