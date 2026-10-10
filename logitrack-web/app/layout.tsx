import type { Metadata } from "next";
import { Geist, Geist_Mono, Sarabun } from "next/font/google";
import "./globals.css";
import { Providers } from "./providers";

// Two families (developer-spec.md §10.11): Geist (sans + mono) for Latin text, Sarabun for Thai.
// The body uses `font-display` (app/globals.css): Geist first, Sarabun for the glyphs Geist lacks.
const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

const sarabun = Sarabun({
  weight: ["300", "400", "500", "700"],
  variable: "--font-sarabun",
  subsets: ["thai", "latin"],
});

export const metadata: Metadata = {
  title: "Logi Track",
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
    <html lang="en" suppressHydrationWarning>
      <body
        suppressHydrationWarning
        className={`${geistSans.variable} ${geistMono.variable} ${sarabun.variable} antialiased font-display`}
      >
        <script
          dangerouslySetInnerHTML={{
            __html: `
              (function() {
                try {
                  const theme = localStorage.getItem('theme');
                  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
                  const shouldBeDark = theme === 'dark' || (!theme && prefersDark);
                  if (shouldBeDark) {
                    document.documentElement.classList.add('dark');
                  } else {
                    document.documentElement.classList.remove('dark');
                  }
                } catch (e) {}
              })();
            `,
          }}
        />
        {/* TanStack Query, language, auth over ['me'], toasts (developer-spec.md §10.6). */}
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
