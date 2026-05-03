import { describe, it, expect } from "vitest";
import { truncate } from "../src/popup/popup.js";

describe("truncate", () => {
  it("returns input unchanged when shorter than max", () => {
    expect(truncate("hello", 10)).toBe("hello");
  });

  it("truncates with ellipsis when longer than max", () => {
    expect(truncate("hello world", 8)).toBe("hello...");
  });

  it("handles edge case where input length equals max", () => {
    expect(truncate("hello", 5)).toBe("hello");
  });
});
