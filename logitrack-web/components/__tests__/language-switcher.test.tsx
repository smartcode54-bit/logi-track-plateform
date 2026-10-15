import fs from "node:fs";
import path from "node:path";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LanguageProvider } from "@/context/language";
import { WEB_ROOT } from "@/test-utils/importGraph";
import { LanguageSwitcher } from "../language-switcher";

function renderSwitcher() {
  return render(
    <LanguageProvider>
      <LanguageSwitcher />
    </LanguageProvider>
  );
}

describe("LanguageSwitcher", () => {
  beforeEach(() => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("en");
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
  });

  it("names each language in its own words, text only: no flags (UI3E, DESIGN.md)", async () => {
    const { container } = renderSwitcher();
    const english = await screen.findByRole("button", { name: "English" });
    const thai = screen.getByRole("button", { name: "ไทย" });
    expect(english).toHaveTextContent(/^English$/);
    expect(thai).toHaveTextContent(/^ไทย$/);
    expect(english).toHaveAttribute("lang", "en");
    expect(thai).toHaveAttribute("lang", "th");
    expect(container.querySelector("img, svg")).toBeNull();
    expect(container.textContent).not.toMatch(/\b(EN|TH)\b|🇹🇭|🇺🇸/u);
  });

  it("says which language is on (aria-pressed), in a named group", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("th");
    renderSwitcher();
    expect(await screen.findByRole("button", { name: "ไทย" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "English" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("group", { name: "ภาษา" })).toBeInTheDocument();
  });

  it("is a 44 px target on a phone", async () => {
    renderSwitcher();
    for (const button of [await screen.findByRole("button", { name: "English" }), screen.getByRole("button", { name: "ไทย" })]) {
      expect(button.className).toContain("max-md:h-11");
      expect(button.className).toContain("max-md:min-w-11");
    }
  });

  it("switches to Thai", async () => {
    const user = userEvent.setup();
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    renderSwitcher();
    await user.click(await screen.findByRole("button", { name: "ไทย" }));
    expect(setItem).toHaveBeenCalledWith("language", "th");
  });

  it("switches to English", async () => {
    const user = userEvent.setup();
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("th");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    renderSwitcher();
    await user.click(await screen.findByRole("button", { name: "English" }));
    expect(setItem).toHaveBeenCalledWith("language", "en");
  });

  it("leaves no flag image in public/ (nothing refers to one any more)", () => {
    const files = fs.readdirSync(path.join(WEB_ROOT, "public"));
    expect(files.filter((f) => /flag|round_icon/i.test(f))).toEqual([]);
  });
});
