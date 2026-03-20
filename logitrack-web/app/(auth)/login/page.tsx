"use client";

import { LoginForm } from "@/features/auth";

export default function LoginPage() {
  return (
    <div className="min-h-screen w-full relative flex items-center justify-center bg-background overflow-hidden">
      <div className="absolute inset-0 -z-10">
        <div className="h-full w-full bg-[url('/driver-app-bg.png')] bg-cover bg-center opacity-60" />
        <div className="absolute inset-0 bg-background/40" />
      </div>

      <div className="relative z-10 w-full flex justify-center px-4">
        <LoginForm />
      </div>
    </div>
  );
}

