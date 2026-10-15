import { describe, expect, it } from "vitest";
import { userAgentLabel } from "./userAgent";

describe("userAgentLabel", () => {
    it.each([
        ["Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0 Safari/537.36 Edg/130.0", "Edge · Windows"],
        ["Mozilla/5.0 (Linux; Android 14) Chrome/130.0 Mobile Safari/537.36", "Chrome · Android"],
        ["Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) Version/17.0 Safari/605.1", "Safari · macOS"],
        ["Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Mobile/15 Safari/604.1", "Safari · iOS"],
        ["Mozilla/5.0 (iPad; CPU OS 17_0) CriOS/130.0 Mobile/15 Safari/604.1", "Chrome · iOS"],
        ["Mozilla/5.0 (iPhone) FxiOS/130.0 Mobile/15 Safari/604.1", "Firefox · iOS"],
        ["Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0", "Firefox · Linux"],
        ["Mozilla/5.0 (X11; CrOS x86_64) Chrome/130.0 Safari/537.36", "Chrome · ChromeOS"],
        ["Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36 OPR/110.0", "Opera · Windows"],
        ["Dart/3.5 (Android)", "ไม่ทราบเบราว์เซอร์ · Android"],
        ["unrecognised", "ไม่ทราบเบราว์เซอร์"],
        [null, "ไม่ทราบเบราว์เซอร์"],
        [undefined, "ไม่ทราบเบราว์เซอร์"],
    ])("parses %s without exposing the full user agent", (ua, expected) => {
        expect(userAgentLabel(ua, "ไม่ทราบเบราว์เซอร์")).toBe(expected);
    });
});
