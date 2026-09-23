// Conventional Commits, with two pragmatic carve-outs so bots never break CI:
//  1. Dependabot commits created before/without the `chore(deps)` prefix ("Bump x from a to b",
//     signed off by dependabot[bot]) are skipped. New ones already comply via .github/dependabot.yml.
//  2. Bot-generated bodies contain long lines/URLs, so body/footer line-length limits are relaxed.
const isDependabot = (msg) => /Signed-off-by: dependabot\[bot\]/i.test(msg) || /^Bump [^\n]+\n+Bumps /.test(msg);

export default {
  extends: ["@commitlint/config-conventional"],
  ignores: [isDependabot],
  defaultIgnores: true,
  rules: {
    "body-max-line-length": [0],
    "footer-max-line-length": [0],
  },
};
