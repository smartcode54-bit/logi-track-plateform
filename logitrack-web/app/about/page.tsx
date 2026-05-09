"use client";

import Link from "next/link";
import Image from "next/image";
import Navigation from "@/components/navigation";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { ArrowLeft, Zap, Shield, TrendingUp, LayoutDashboard } from "lucide-react";

export default function AboutPage() {
  const { t } = useLanguage();

  return (
    <div className="min-h-screen bg-background flex flex-col">
      <Navigation />
      <main className="flex-1">
        {/* Hero */}
        <section className="relative py-16 md:py-24 overflow-hidden">
          <div className="absolute inset-0 -z-10">
            <Image
              src="https://images.unsplash.com/photo-1506929562872-bb421503ef21?w=1920"
              alt="Journey"
              fill
              className="object-cover opacity-30"
              priority
              sizes="100vw"
            />
            <div className="absolute inset-0 bg-gradient-to-b from-background/90 via-background/80 to-background" />
          </div>
          <div className="container max-w-4xl mx-auto px-4 text-center">
            <h1 className="text-3xl md:text-4xl lg:text-5xl font-bold tracking-tight mb-6">
              {t("about.hero.title")}
            </h1>
            <blockquote className="text-lg md:text-xl text-muted-foreground font-medium border-l-4 border-primary pl-6 text-left max-w-2xl mx-auto">
              {t("about.hero.quote")}
            </blockquote>
          </div>
        </section>

        {/* Story */}
        <section className="py-16 md:py-20 border-t border-border/60">
          <div className="container max-w-5xl mx-auto px-4">
            <div className="grid md:grid-cols-2 gap-12 items-center">
              <div className="relative aspect-[4/3] rounded-2xl overflow-hidden bg-muted/30">
                <Image
                  src="https://images.unsplash.com/photo-1461749280684-dccba630e2f6?w=800"
                  alt="Custom development"
                  fill
                  className="object-cover"
                  sizes="(max-width: 768px) 100vw, 50vw"
                />
              </div>
              <div className="space-y-6">
                <p className="text-sm font-semibold text-primary uppercase tracking-wider">
                  {t("about.story.date")}
                </p>
                <p className="text-muted-foreground leading-relaxed">
                  {t("about.story.p1")}
                </p>
                <p className="text-muted-foreground leading-relaxed">
                  {t("about.story.answer")}
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* Why GORATECH */}
        <section className="py-16 md:py-20 bg-muted/30">
          <div className="container max-w-5xl mx-auto px-4">
            <h2 className="text-2xl md:text-3xl font-bold text-center mb-4">
              {t("about.why.title")}
            </h2>
            <p className="text-muted-foreground text-center max-w-2xl mx-auto mb-12">
              {t("about.why.origin")}
            </p>
            <div className="grid md:grid-cols-3 gap-8">
              <div className="flex flex-col items-center text-center">
                <div className="relative w-full aspect-video rounded-xl overflow-hidden bg-background mb-4">
                  <Image
                    src="https://images.unsplash.com/photo-1581091226825-a6a2a5aee158?w=600"
                    alt="Precision"
                    fill
                    className="object-cover"
                    sizes="(max-width: 768px) 100vw, 33vw"
                  />
                </div>
                <div className="flex items-center gap-2 mb-2">
                  <Zap className="h-5 w-5 text-primary" />
                  <h3 className="font-semibold text-lg">{t("about.why.precision.title")}</h3>
                </div>
                <p className="text-sm text-muted-foreground">{t("about.why.precision.desc")}</p>
              </div>
              <div className="flex flex-col items-center text-center">
                <div className="relative w-full aspect-video rounded-xl overflow-hidden bg-background mb-4">
                  <Image
                    src="https://images.unsplash.com/photo-1563013544-824ae1b704d3?w=600"
                    alt="Security"
                    fill
                    className="object-cover"
                    sizes="(max-width: 768px) 100vw, 33vw"
                  />
                </div>
                <div className="flex items-center gap-2 mb-2">
                  <Shield className="h-5 w-5 text-primary" />
                  <h3 className="font-semibold text-lg">{t("about.why.security.title")}</h3>
                </div>
                <p className="text-sm text-muted-foreground">{t("about.why.security.desc")}</p>
              </div>
              <div className="flex flex-col items-center text-center">
                <div className="relative w-full aspect-video rounded-xl overflow-hidden bg-background mb-4">
                  <Image
                    src="https://images.unsplash.com/photo-1551288049-bebda4e38f71?w=600"
                    alt="Scalability"
                    fill
                    className="object-cover"
                    sizes="(max-width: 768px) 100vw, 33vw"
                  />
                </div>
                <div className="flex items-center gap-2 mb-2">
                  <TrendingUp className="h-5 w-5 text-primary" />
                  <h3 className="font-semibold text-lg">{t("about.why.scale.title")}</h3>
                </div>
                <p className="text-sm text-muted-foreground">{t("about.why.scale.desc")}</p>
              </div>
            </div>
          </div>
        </section>

        {/* Closing */}
        <section className="py-16 md:py-20">
          <div className="container max-w-3xl mx-auto px-4 text-center">
            <p className="text-lg text-muted-foreground leading-relaxed">
              {t("about.closing")}
            </p>
          </div>
        </section>

        {/* Hand-crafted dashboard showcase */}
        <section className="py-16 md:py-20 bg-muted/30 border-t border-border/60">
          <div className="container max-w-5xl mx-auto px-4">
            <div className="flex flex-col items-center text-center mb-10">
              <div className="flex items-center gap-2 mb-2">
                <LayoutDashboard className="h-6 w-6 text-primary" />
                <h2 className="text-2xl font-bold">{t("about.dashboard.title")}</h2>
              </div>
              <p className="text-muted-foreground max-w-xl">
                {t("about.dashboard.desc")}
              </p>
            </div>
            <div className="relative w-full aspect-video max-w-4xl mx-auto rounded-2xl overflow-hidden border border-border bg-muted/50 shadow-xl">
              {/* Placeholder: replace src with your dashboard screenshot e.g. /dashboard-preview.png */}
              <Image
                src="https://images.unsplash.com/photo-1551288049-bebda4e38f71?w=1200"
                alt="Platform dashboard"
                fill
                className="object-cover"
                sizes="(max-width: 1200px) 100vw, 1200px"
              />
            </div>
          </div>
        </section>

        {/* CTA */}
        <section className="py-12 border-t border-border/60">
          <div className="container max-w-3xl mx-auto px-4 text-center">
            <Button variant="outline" size="lg" className="gap-2 cursor-pointer" onClick={() => window.history.back()}>
              <ArrowLeft className="h-4 w-4" />
              {t("about.cta.back")}
            </Button>
          </div>
        </section>
      </main>
    </div>
  );
}
