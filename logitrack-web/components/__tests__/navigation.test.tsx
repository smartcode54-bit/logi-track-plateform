import React from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Navigation from "../navigation";
import { LanguageProvider } from "@/context/language";
import { configureSignOutNavigation } from "@/features/auth/hooks/useSignOut";

const mockPush = vi.fn();
let mockPathname = "/about";
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockPush, replace: vi.fn() }),
  usePathname: () => mockPathname,
}));

vi.mock("../language-switcher", () => ({
  LanguageSwitcher: () => <div data-testid="language-switcher">LanguageSwitcher</div>,
}));

const mockUseAuth = vi.fn();
vi.mock("@/context/auth", () => ({
  useAuth: () => mockUseAuth(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

/** Renders inside LanguageProvider and waits until its dictionary is loaded (it renders children then). */
async function renderWithProviders(ui: React.ReactElement) {
  const view = render(<LanguageProvider>{ui}</LanguageProvider>);
  await screen.findByText("LogiTrack Pro");
  return view;
}

const navigated: string[] = [];

describe("Navigation", () => {
  beforeEach(() => {
    mockPathname = "/about";
    navigated.length = 0;
    configureSignOutNavigation((url) => navigated.push(url));
    mockUseAuth.mockReturnValue({
      currentUser: null,
      loading: false,
      customClaims: null,
      login: vi.fn(),
      logout: vi.fn(),
    });
  });
  afterEach(() => {
    configureSignOutNavigation((url) => window.location.assign(url));
  });

  it("names the product LogiTrack Pro by GORATECH (UI3), not Logi-Track", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByText("LogiTrack Pro")).toBeInTheDocument();
    expect(screen.getByText("by GORATECH")).toBeInTheDocument();
    expect(screen.queryByText("Logi-Track")).not.toBeInTheDocument();
  });

  it("shows Sign In / login link when user is not logged in, except on the sign-in page itself", async () => {
    const { unmount } = await renderWithProviders(<Navigation />);
    const loginLink = screen.getByRole("link", { name: /sign in/i });
    expect(loginLink).toHaveAttribute("href", "/login");
    unmount();
    mockPathname = "/login";
    await renderWithProviders(<Navigation />);
    expect(screen.queryByRole("link", { name: /sign in/i })).not.toBeInTheDocument();
  });

  it("shows Solutions and Pricing links when not logged in", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByRole("link", { name: /solutions/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /pricing/i })).toBeInTheDocument();
  });

  it("names the theme button by what it does", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByRole("button", { name: "Switch to the dark theme" })).toBeInTheDocument();
  });

  it("shows admin dashboard link when user is admin, and signs out to the sign-in page", async () => {
    const user = userEvent.setup();
    const logout = vi.fn(async () => undefined);
    mockUseAuth.mockReturnValue({
      currentUser: { uid: "1", email: "a@b.co", displayName: "Admin" },
      loading: false,
      customClaims: { admin: true },
      login: vi.fn(),
      logout,
    });
    await renderWithProviders(<Navigation />);
    await user.click(screen.getByRole("button", { name: "Account menu for Admin" }));
    expect(screen.getByRole("menuitem", { name: /admin dashboard/i })).toBeInTheDocument();
    await user.click(screen.getByRole("menuitem", { name: "Logout" }));
    await waitFor(() => expect(navigated).toEqual(["/login"]));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(mockPush).not.toHaveBeenCalled();
  });
});
