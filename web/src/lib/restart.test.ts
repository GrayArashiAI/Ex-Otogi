import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { pendingRestart, type Section } from "./restart.ts";

const section = (id: string, applies: Section["applies"], revision: number, running: number): Section => ({
  id,
  applies,
  revision,
  running,
});

test("a section read at the start waits for a restart while its revision is newer than the running one", () => {
  const sections = [
    section("accounts", "restart", 3, 3),
    section("general", "now", 4, 4),
    section("process", "restart", 2, 1),
  ];
  assert.deepEqual(
    pendingRestart(sections).map((s) => s.id),
    ["process"],
  );
});

test("a first revision saved since the start waits too", () => {
  assert.equal(pendingRestart([section("accounts", "restart", 1, 0)]).length, 1);
});

test("nothing waits when every section runs its latest revision, or was never saved", () => {
  assert.deepEqual(pendingRestart([section("accounts", "restart", 0, 0), section("process", "restart", 5, 5)]), []);
});

test("a section applied as it is saved never waits", () => {
  assert.deepEqual(pendingRestart([section("general", "now", 2, 1)]), []);
});
