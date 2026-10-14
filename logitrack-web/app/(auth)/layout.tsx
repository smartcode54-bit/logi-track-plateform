import Navigation from "@/components/navigation";
import { AuthBackground } from "@/features/auth/components/AuthBackground";

export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    // `isolate`: the background's negative z-index stays inside this page, above its paper colour.
    <div className="relative isolate flex min-h-screen flex-col overflow-hidden bg-background">
      <Navigation />

      {/* The one background of the sign-in pages (UI3: the login page no longer paints its own copy). */}
      <AuthBackground />

      <main className="flex flex-1 items-center justify-center px-4 py-8 sm:px-6 lg:px-8">
        {children}
      </main>
    </div>
  );
}
