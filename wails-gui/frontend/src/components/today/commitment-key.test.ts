import { expect, it } from "vitest";
import { isCommitmentItemKey } from "./commitment-key";

it("recognizes current and legacy commitment keys", () => {
    expect(isCommitmentItemKey("commitments.79bc12")).toBe(true);
    expect(isCommitmentItemKey("commitments:legacy")).toBe(true);
    expect(isCommitmentItemKey("commitments-1")).toBe(true);
    expect(isCommitmentItemKey("workingToward.79bc12")).toBe(false);
    expect(isCommitmentItemKey(undefined)).toBe(false);
});
