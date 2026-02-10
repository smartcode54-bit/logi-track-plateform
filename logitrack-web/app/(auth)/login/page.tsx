

"use client";

import { useState } from "react";
import ContinueWithGoogleButton from "@/components/continue-with-google-button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

export default function LoginPage() {
  const { t } = useLanguage();
  const auth = useAuth();
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!auth) {
        throw new Error("Auth context not initialized");
      }
      if (auth) {
        await auth.login(email, password);
      }
      // Redirect handled by AuthContext or separate logic, but usually we push to dashboard
      router.push("/admin/dashboard");
      toast.success("Logged in successfully");
    } catch (error: any) {
      console.error("Login error:", error);
      toast.error(error.message || "Failed to login");
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card className="w-full max-w-md shadow-lg border-muted/20">
      <CardHeader className="space-y-1">
        <CardTitle className="text-2xl font-bold">{t("auth.login.title") || "Login"}</CardTitle>
        <CardDescription>
          {t("auth.login.subtitle") || "Enter your email below to login to your account"}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="email">{t("auth.email") || "Email"}</Label>
            <Input
              id="email"
              type="email"
              placeholder="admin@cjexpress.co.th"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
            />
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="password">{t("auth.password") || "Password"}</Label>
              <Link
                href="/forgot-password"
                className="text-sm font-medium text-muted-foreground hover:text-primary transition-colors"
              >
                {t("auth.forgotPassword") || "Forgot your password?"}
              </Link>
            </div>
            <Input
              id="password"
              type="password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
            />
          </div>

          <Button type="submit" className="w-full bg-foreground text-background hover:bg-foreground/90 font-semibold" disabled={loading}>
            {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            {t("auth.login.title") || "Login"}
          </Button>
        </form>
      </CardContent>
      <CardFooter className="flex flex-col gap-4">
        <div className="relative w-full">
          <div className="absolute inset-0 flex items-center">
            <span className="w-full border-t" />
          </div>
          <div className="relative flex justify-center text-xs uppercase">
            <span className="bg-background px-2 text-muted-foreground">
              {t("auth.or") || "or"}
            </span>
          </div>
        </div>
        <ContinueWithGoogleButton />
      </CardFooter>
      <div className="p-6 pt-0 text-center">
        <p className="text-sm text-muted-foreground">
          {t("auth.dontHaveAccount") || "Don't have an account?"}{" "}
          <Link href="/waitlist" className="font-semibold text-foreground hover:underline">
            {t("nav.register") || "Join Wait List"}
          </Link>
        </p>
      </div>
    </Card>
  );
}