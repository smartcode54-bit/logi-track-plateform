import type { Metadata } from "next";
import { Geist, Geist_Mono, Sarabun } from "next/font/google";
import "./globals.css";
import { Providers } from "./providers";

// One sans pairing and one mono (developer-spec.md §10.11, DESIGN.md): Geist for Latin, Sarabun for Thai,
// Geist Mono for codes and figures. The variables sit on <html>, so `--font-sans` (app/globals.css: Geist
// first, Sarabun for the glyphs Geist lacks) resolves everywhere, portals included.
const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

const sarabun = Sarabun({
  weight: ["400", "500", "600", "700"],
  variable: "--font-sarabun",
  subsets: ["thai", "latin"],
});

export const metadata: Metadata = {
  // The product name (PRODUCT.md, Brand Commitments): LogiTrack Pro by GORATECH.
  title: "LogiTrack Pro by GORATECH",
  description: "Created by SmartCode",
  icons: {
    icon: "/icon.jpg",
    apple: "/icon.jpg",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    // next-themes (app/providers.tsx) sets the `dark` class before paint, hence suppressHydrationWarning.
    // `lang` starts as English and follows the chosen language once it loads (context/language.tsx).
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} ${sarabun.variable}`}
      suppressHydrationWarning
    >
      <body suppressHydrationWarning>
        {/* Theme, TanStack Query, language, auth over ['me'], the session stream, toasts (developer-spec.md §10.6). */}
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
