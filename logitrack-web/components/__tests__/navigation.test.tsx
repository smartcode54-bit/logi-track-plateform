import React from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Navigation from "../navigation";
import { LanguageProvider } from "@/context/language";

const mockPush = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockPush, replace: vi.fn() }),
}));

vi.mock("../language-switcher", () => ({
  LanguageSwitcher: () => <div data-testid="language-switcher">LanguageSwitcher</div>,
}));

const mockUseAuth = vi.fn();
vi.mock("@/context/auth", () => ({
  useAuth: () => mockUseAuth(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

function renderWithProviders(ui: React.ReactElement) {
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

describe("Navigation", () => {
  beforeEach(() => {
    mockUseAuth.mockReturnValue({
      currentUser: null,
      loading: false,
      customClaims: null,
      login: vi.fn(),
      logout: vi.fn(),
    });
  });

  it("renders brand Logi-Track", () => {
    renderWithProviders(<Navigation />);
    expect(screen.getByText("Logi-Track")).toBeInTheDocument();
  });

  it("shows Sign In / login link when user is not logged in", () => {
    renderWithProviders(<Navigation />);
    const loginLink = screen.getByRole("link", { name: /sign in/i });
    expect(loginLink).toBeInTheDocument();
    expect(loginLink).toHaveAttribute("href", "/login");
  });

  it("shows Solutions and Pricing links when not logged in", () => {
    renderWithProviders(<Navigation />);
    expect(screen.getByRole("link", { name: /solutions/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /pricing/i })).toBeInTheDocument();
  });

  it("shows admin dashboard link when user is admin", async () => {
    const user = userEvent.setup();
    mockUseAuth.mockReturnValue({
      currentUser: { uid: "1", email: "a@b.co", displayName: "Admin" } as any,
      loading: false,
      customClaims: { admin: true },
      login: vi.fn(),
      logout: vi.fn(),
    });
    renderWithProviders(<Navigation />);
    const menuButton = screen.getByRole("button", { name: "A" });
    await user.click(menuButton);
    expect(screen.getByRole("menuitem", { name: /admin dashboard/i })).toBeInTheDocument();
  });
});
