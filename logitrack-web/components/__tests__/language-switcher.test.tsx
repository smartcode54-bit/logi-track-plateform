import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LanguageProvider } from "@/context/language";
import { LanguageSwitcher } from "../language-switcher";

vi.mock("next/image", () => ({
  default: ({ alt, src }: { alt: string; src: string }) => <img src={src} alt={alt} />,
}));

describe("LanguageSwitcher", () => {
  beforeEach(() => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue(null);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
  });

  it("renders EN and TH options", () => {
    render(
      <LanguageProvider>
        <LanguageSwitcher />
      </LanguageProvider>
    );
    expect(screen.getByRole("button", { name: /en/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /th/i })).toBeInTheDocument();
  });

  it("calls setLanguage when TH is clicked", async () => {
    const user = userEvent.setup();
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    render(
      <LanguageProvider>
        <LanguageSwitcher />
      </LanguageProvider>
    );
    await user.click(screen.getByRole("button", { name: /th/i }));
    expect(setItem).toHaveBeenCalledWith("language", "th");
  });

  it("calls setLanguage when EN is clicked", async () => {
    const user = userEvent.setup();
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("th");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    render(
      <LanguageProvider>
        <LanguageSwitcher />
      </LanguageProvider>
    );
    await user.click(screen.getByRole("button", { name: /en/i }));
    expect(setItem).toHaveBeenCalledWith("language", "en");
  });
});
