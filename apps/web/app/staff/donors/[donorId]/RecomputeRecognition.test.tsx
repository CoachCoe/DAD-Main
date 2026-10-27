import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { RecomputeRecognition } from "./RecomputeRecognition";

afterEach(() => vi.unstubAllGlobals());

it("records the reason and refreshes the donor after a successful repair", async () => {
  const fetch = vi.fn(() => Promise.resolve(Response.json({ recomputed: true })));
  vi.stubGlobal("fetch", fetch);
  const onComplete = vi.fn();
  render(<RecomputeRecognition donor="donor-1" onComplete={onComplete} />);
  fireEvent.click(screen.getByText("Repair recognition after a system error"));
  fireEvent.change(screen.getByLabelText("Reason for repair"), {
    target: { value: "Repair after system error DAD-123" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Recompute recognition" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Recognition recomputed from recorded facts");
  expect(fetch).toHaveBeenCalledWith(
    "/api/donor/staff/donors/donor-1/recognition/recompute",
    expect.objectContaining({ method: "POST", body: JSON.stringify({ reason: "Repair after system error DAD-123" }) }),
  );
  expect(onComplete).toHaveBeenCalledOnce();
});

it("retains the repair reason when authorization is refused", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(Response.json({ detail: "Permission denied" }, { status: 403 }))),
  );
  const onComplete = vi.fn();
  render(<RecomputeRecognition donor="donor-1" onComplete={onComplete} />);
  fireEvent.click(screen.getByText("Repair recognition after a system error"));
  fireEvent.change(screen.getByLabelText("Reason for repair"), {
    target: { value: "Repair after system error DAD-123" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Recompute recognition" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Permission denied");
  await waitFor(() => expect(screen.getByRole("button", { name: "Recompute recognition" })).toBeEnabled());
  expect(screen.getByLabelText("Reason for repair")).toHaveValue("Repair after system error DAD-123");
  expect(onComplete).not.toHaveBeenCalled();
});
