"use client";

import { Suspense } from "react";

import LoginForm from "@/features/auth/components/LoginForm";

/** The sign-in page; its background and navigation come from app/(auth)/layout.tsx. */
export default function LoginPage() {
  return (
    <div className="flex w-full justify-center">
      {/* useSearchParams (next, reason) needs a Suspense boundary on a prerendered page. */}
      <Suspense fallback={null}>
        <LoginForm />
      </Suspense>
    </div>
  );
}
