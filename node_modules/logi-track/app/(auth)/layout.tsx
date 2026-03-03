import Navigation from "@/components/navigation";

export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="min-h-screen flex flex-col relative bg-background overflow-hidden">
      {/* Top navigation bar */}
      <Navigation />

      {/* Background image for auth pages */}
      <div className="absolute inset-0 -z-10">
        <div className="h-full w-full bg-[url('/driver-app-bg.png')] bg-cover bg-center opacity-60" />
        <div className="absolute inset-0 bg-background/40" />
      </div>

      {/* Main auth content */}
      <main className="flex-1 flex items-center justify-center px-4 sm:px-6 lg:px-8 relative z-10">
        {children}
      </main>
    </div>
  );
}

