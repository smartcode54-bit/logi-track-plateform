"use client";

import Link from "next/link";
import { ArrowLeftIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useForgotPassword } from "../hooks/useForgotPassword";
import { AuthCardTitle } from "./AuthCardTitle";

export default function ForgotPasswordForm() {
  const { email, setEmail, loading, sent, error, handleSubmit, t } = useForgotPassword();

  return (
    <Card className="w-full max-w-md">
      <CardHeader className="gap-1">
        <AuthCardTitle>{t("auth.forgot.title")}</AuthCardTitle>
        <CardDescription>{t("auth.forgot.subtitle")}</CardDescription>
      </CardHeader>
      <CardContent>
        {sent ? (
          <p role="status" className="text-sm text-success">
            {t("auth.forgot.sent")}
          </p>
        ) : (
          <form onSubmit={handleSubmit}>
            <div className="flex flex-col gap-6">
              <div className="grid gap-2">
                <Label htmlFor="email">{t("auth.email")}</Label>
                <Input
                  id="email"
                  type="email"
                  autoComplete="email"
                  placeholder={t("auth.emailPlaceholder")}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  className="max-md:h-11"
                />
              </div>
              {error ? (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              ) : null}
              <Button type="submit" className="w-full max-md:h-11" disabled={loading || !email.trim()}>
                {loading ? t("auth.forgot.sending") : t("auth.forgot.submit")}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
      <CardFooter className="justify-center">
        <Button variant="link" asChild className="max-md:h-11">
          <Link href="/login">
            <ArrowLeftIcon aria-hidden /> {t("auth.backToLogin")}
          </Link>
        </Button>
      </CardFooter>
    </Card>
  );
}
