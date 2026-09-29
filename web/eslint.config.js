import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

/*
 * Why this exists, and why it is small.
 *
 * It exists because of a blank page. A `useState` was added below the two
 * early returns that render the connection screen and the loading spinner,
 * the hook count changed between renders, and React threw #310 at anyone
 * with a live session. Nothing in the toolchain said a word: `tsc` is happy
 * with it, the tests render components that never hit that path, and the
 * fault only appears in a browser holding a connected session. The rule that
 * catches it in a second -- react-hooks/rules-of-hooks -- had no linter to
 * run in. There was even an `eslint-disable-next-line` comment in app.tsx
 * suppressing a rule that did not exist.
 *
 * It is small because a linter's value here is the handful of rules that
 * catch what neither the compiler nor the tests can. Style is not one of
 * those: this repository has a voice, formatting arguments are noise, and a
 * wall of warnings is how a linter gets `--max-warnings 999` in CI and stops
 * meaning anything. So: the recommended sets, the hooks rules, and nothing
 * invented on top.
 *
 * Not type-aware linting. The type-checked configurations need a program per
 * run and would roughly double what CI spends on the frontend, to find what
 * `tsc --noEmit` already finds -- and `typecheck` is a separate CI step
 * already.
 */
export default tseslint.config(
  {
    // Generated from api/openapi.yaml, and regenerating it is the only way
    // to change it. A lint failure here would be a bug report against
    // openapi-typescript that nobody in this repository can act on.
    ignores: ["dist", "src/lib/api.gen.ts"],
  },
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      // Named one by one rather than spreading the plugin's recommended
      // set. Version 7 of it also ships the React Compiler rules, and
      // turning those on reports fourteen more sites here -- thirteen
      // `set-state-in-effect` and one `static-components`. They are not
      // noise and they are not being dismissed; several are the kind of
      // effect that can drive a render loop, which is still an open
      // question in this product. Reviewing fourteen effects is its own
      // piece of work, and doing it under a linter that fails the build
      // meanwhile is how it would get done badly.

      // The one this was installed for. An error, never a warning: the
      // failure it prevents is a blank page, not a smell.
      "react-hooks/rules-of-hooks": "error",

      // A stale closure is the same class of bug as the one above -- the
      // jump palette's shortcut and its button disagreed about what was
      // open until `open` went into the dependency list -- but the honest
      // exceptions are real, and app.tsx already carries one with its
      // reasons written out. Warn, and make CI refuse new ones.
      "react-hooks/exhaustive-deps": "warn",

      // `_` marks a value deliberately unused, which is a statement rather
      // than an oversight.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  {
    // Tests reach for casts the application does not need, to build a
    // fixture that stands in for a server response.
    files: ["**/*.test.{ts,tsx}"],
    rules: { "@typescript-eslint/no-explicit-any": "off" },
  },
);
