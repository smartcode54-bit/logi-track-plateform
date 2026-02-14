import Navigation from "@/components/navigation";
import Image from "next/image";

export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="min-h-screen bg-background flex flex-col relative">
      <Navigation />
      <main className="flex-1 flex items-center justify-center px-4 sm:px-6 lg:px-8 relative z-10">
        {children}
      </main>
    </div>
  );
}

