import { describe, expect, it } from "vitest";
import {
  appStatusSessionState,
  appSummarySessionState,
  isRunningSession,
  sessionStateDotClass,
} from "./session-status";

describe("session status helpers", () => {
  it("does not treat degraded sessions as running", () => {
    const state = appSummarySessionState({
      id: "main-123abc",
      name: "demo",
      app_root: "/tmp/demo",
      offline: true,
      sessionStatus: "degraded",
      sessionStatusReason: "app process 42 is not running",
    });

    expect(state).toBe("degraded");
    expect(isRunningSession(state)).toBe(false);
    expect(sessionStateDotClass(state)).toContain("bg-amber-400");
  });

  it("uses the classified session status before the legacy running flag", () => {
    expect(
      appStatusSessionState(
        {
          running: false,
          appID: "main-123abc",
          appRoot: "/tmp/demo",
          sessionStatus: "stale",
          compiling: false,
        },
        true,
      ),
    ).toBe("stale");
  });
});
