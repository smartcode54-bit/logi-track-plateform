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

  it("shows only the current language, in its own words without flags", async () => {
    const { container } = renderSwitcher();
    const english = await screen.findByRole("button", { name: "Language: English" });
    expect(english).toHaveTextContent(/^English$/);
    expect(english).toHaveAttribute("lang", "en");
    expect(screen.queryByText("ไทย")).not.toBeInTheDocument();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).not.toMatch(/\b(EN|TH)\b|🇹🇭|🇺🇸/u);
  });

  it("names both menu options and marks the stored language as selected", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("th");
    const user = userEvent.setup();
    renderSwitcher();
    const trigger = await screen.findByRole("button", { name: "ภาษา: ไทย" });
    expect(trigger).toHaveTextContent(/^ไทย$/);
    await user.click(trigger);
    const thai = screen.getByRole("menuitemradio", { name: "ไทย" });
    const english = screen.getByRole("menuitemradio", { name: "English" });
    expect(thai).toHaveAttribute("aria-checked", "true");
    expect(thai).toHaveAttribute("lang", "th");
    expect(english).toHaveAttribute("aria-checked", "false");
    expect(english).toHaveAttribute("lang", "en");
    expect(screen.getByRole("group", { name: "ภาษา" })).toBeInTheDocument();
  });

  it("keeps the trigger and menu options as 44 px targets on a phone", async () => {
    const user = userEvent.setup();
    renderSwitcher();
    const trigger = await screen.findByRole("button", { name: "Language: English" });
    expect(trigger.className).toContain("max-md:h-11");
    expect(trigger.className).toContain("max-md:min-w-11");
    await user.click(trigger);
    for (const option of screen.getAllByRole("menuitemradio")) {
      expect(option.className).toContain("max-md:min-h-11");
    }
  });

  it("selects Thai, updates the compact label, persists and closes the menu", async () => {
    const user = userEvent.setup();
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    renderSwitcher();
    await user.click(await screen.findByRole("button", { name: "Language: English" }));
    await user.click(screen.getByRole("menuitemradio", { name: "ไทย" }));
    expect(await screen.findByRole("button", { name: "ภาษา: ไทย" })).toHaveTextContent(/^ไทย$/);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(setItem).toHaveBeenCalledWith("language", "th");
  });

  it("switches to English", async () => {
    const user = userEvent.setup();
    vi.spyOn(Storage.prototype, "getItem").mockReturnValue("th");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    renderSwitcher();
    await user.click(await screen.findByRole("button", { name: "ภาษา: ไทย" }));
    await user.click(screen.getByRole("menuitemradio", { name: "English" }));
    expect(await screen.findByRole("button", { name: "Language: English" })).toHaveTextContent(/^English$/);
    expect(setItem).toHaveBeenCalledWith("language", "en");
  });

  it("opens, selects and dismisses with the keyboard, returning focus to the trigger", async () => {
    const user = userEvent.setup();
    renderSwitcher();
    const trigger = await screen.findByRole("button", { name: "Language: English" });
    await user.tab();
    expect(trigger).toHaveFocus();
    await user.keyboard("{ArrowDown}{End}{Enter}");
    const thai = await screen.findByRole("button", { name: "ภาษา: ไทย" });
    expect(thai).toHaveFocus();
    await user.keyboard("{Enter}{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(thai).toHaveFocus();
  });

  it("leaves no flag image in public/ (nothing refers to one any more)", () => {
    const files = fs.readdirSync(path.join(WEB_ROOT, "public"));
    expect(files.filter((f) => /flag|round_icon/i.test(f))).toEqual([]);
  });
});
