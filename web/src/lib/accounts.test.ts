import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { added } from "./accounts.ts";

const was = { telegram: [{ account: "main", app_id: 1 }], qq: [{ account: "otogi", app_id: "9" }] };

test("an account under a name the saved settings did not have is added", () => {
  const now = { telegram: [{ account: "main", app_id: 1 }, { account: "second" }], qq: was.qq };
  assert.deepEqual(added(was, now, "telegram"), ["second"]);
  assert.deepEqual(added(was, now, "qq"), []);
});

test("accounts that keep their names are not added, whatever else changes, and a removed one is nothing", () => {
  assert.deepEqual(added(was, { telegram: [{ account: "main", app_id: 2 }] }, "telegram"), []);
  assert.deepEqual(added(was, { telegram: [{ account: "main" }] }, "qq"), []);
});

test("a name is added on the platform it is new to", () => {
  assert.deepEqual(added({ telegram: [{ account: "a" }] }, { qq: [{ account: "a" }] }, "qq"), ["a"]);
});

test("an account not yet named is not listed", () => {
  assert.deepEqual(added({}, { telegram: [{}] }, "telegram"), []);
});
