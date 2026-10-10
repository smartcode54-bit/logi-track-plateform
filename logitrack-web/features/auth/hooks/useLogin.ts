import { useState, useEffect } from "react";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { homeRouteOf } from "@/features/auth/api/homeRoute";

export function useLogin() {
  const { t } = useLanguage();
  const auth = useAuth();
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);

  // Signed in means a Go session (`['me']`): only then can the proxy.ts gate let `/app` through, so a
  // Firebase-only session no longer bounces between this page and the gate.
  const me = auth?.me ?? null;
  useEffect(() => {
    if (!auth?.loading && me) {
      router.replace(homeRouteOf(me));
    }
  }, [auth?.loading, me, router]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!auth) {
        throw new Error("Auth context not initialized");
      }
      await auth.login(email, password);
      // Redirect will be handled by the useEffect above after auth state updates
      toast.success("Logged in successfully");
    } catch (error: any) {
      console.error("Login error:", error);
      toast.error(error.message || "Failed to login");
    } finally {
      setLoading(false);
    }
  };

  return {
    email,
    setEmail,
    password,
    setPassword,
    loading,
    handleSubmit,
    t
  };
}
