import { afterEach, describe, expect, it, vi } from "vitest";
import { toast } from "./toast";

describe("toast", () => {
  afterEach(() => {
    toast.clear();
    vi.useRealTimers();
  });

  it("shows a success notice that goes by itself", () => {
    vi.useFakeTimers();
    toast.show({ title: "Change applied" });
    expect(toast.all()).toMatchObject([{ title: "Change applied", tone: "success" }]);
    vi.advanceTimersByTime(6000);
    expect(toast.all()).toEqual([]);
  });

  it("keeps at most three", () => {
    for (let i = 0; i < 5; i++) toast.show({ title: `t${i}` });
    expect(toast.all().map((t) => t.title)).toEqual(["t2", "t3", "t4"]);
  });

  it("dismisses one by id", () => {
    const a = toast.show({ title: "a" });
    toast.show({ title: "b" });
    toast.dismiss(a);
    expect(toast.all().map((t) => t.title)).toEqual(["b"]);
  });
});
