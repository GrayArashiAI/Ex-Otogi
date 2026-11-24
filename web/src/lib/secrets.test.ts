import assert from "node:assert/strict";
import { test } from "node:test";
// Node runs this file as it is, so the import names the file.
import { choices, nameProblem, reference, referenceName, restartNote, type Secret, sectionOf } from "./secrets.ts";

const secrets: Secret[] = [
  { name: "JEV_KEY", source: "panel", references: ["decision"] },
  { name: "TODOU_TOKEN", source: "environment", references: ["todou"] },
];

test("a reference and its name turn into each other", () => {
  assert.equal(reference("JEV_KEY"), `\${JEV_KEY}`);
  assert.equal(referenceName(`\${JEV_KEY}`), "JEV_KEY");
  assert.equal(referenceName(reference("_a1")), "_a1");
});

test("what is not a reference names nothing", () => {
  for (const value of [undefined, "", "JEV_KEY", `\${1A}`, `\${A} `, `x\${A}`, 3]) {
    assert.equal(referenceName(value), "");
  }
});

test("a new secret's name must be a reference's, and not set in the panel", () => {
  assert.equal(nameProblem("", secrets), "Required");
  assert.match(nameProblem("1A", secrets) ?? "", /not starting with a digit/);
  assert.match(nameProblem("A-B", secrets) ?? "", /Letters/);
  assert.match(nameProblem("JEV_KEY", secrets) ?? "", /set already/);
  assert.equal(nameProblem("NEW_KEY", secrets), undefined);
});

test("a name only the environment has can be set in the panel", () => {
  assert.equal(nameProblem("TODOU_TOKEN", secrets), undefined);
});

test("a field's reference the list lacks is offered as missing", () => {
  assert.deepEqual(
    choices(secrets, "GONE").map((s) => [s.name, s.source]),
    [
      ["JEV_KEY", "panel"],
      ["TODOU_TOKEN", "environment"],
      ["GONE", "missing"],
    ],
  );
  assert.equal(choices(secrets, "JEV_KEY"), secrets);
  assert.equal(choices(secrets, ""), secrets);
});

test("a reference with system: names a section, and a module's ID names none", () => {
  assert.equal(sectionOf("system:llm"), "llm");
  assert.equal(sectionOf("chat"), undefined);
});

test("a secret's new value restarts the modules that refer to it, by name", () => {
  assert.equal(restartNote([], false), "No running module refers to it, so nothing restarts.");
  assert.equal(restartNote(["Decisions"], false), "Decisions restarts with it at once.");
  assert.equal(restartNote(["Decisions", "todou"], false), "Decisions and todou restart with it at once.");
});

test("a secret the providers refer to restarts the modules that use one, unnamed", () => {
  assert.equal(
    restartNote([], true),
    "The language model providers are rebuilt with it, and the running modules that use one restart.",
  );
  assert.equal(
    restartNote(["todou"], true),
    "todou restarts with it at once. The language model providers are rebuilt with it, and the running modules that use one restart.",
  );
});

test("a secret the platform accounts refer to waits for a restart", () => {
  assert.equal(
    restartNote([], false, true),
    "No running module refers to it, so nothing restarts. The platform accounts read it when the bot starts: the running bot keeps the old value until it is restarted.",
  );
  assert.match(restartNote(["todou"], false, true), /^todou restarts with it at once\. The platform accounts read it/);
});
