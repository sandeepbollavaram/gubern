---
name: gubern-security-fix
description: >
  How to fix a security bug in Gubern so the fix is proven and doesn't
  regress: reproduce it in a test first, keep the fix narrow, check every
  place with the same pattern, and report it honestly. Use for any fix to
  auth, approvals, path or command handling, secrets or the audit log.
license: Apache-2.0
metadata:
  project: gubern
---

# Fixing a security bug

1. **Reproduce first.** Write a test that shows the bad outcome (a file
   written outside the workspace, a denied endpoint reached, a secret in a
   log), not just an error code. Run it on the unfixed code and confirm it
   fails for the reason you expect.
2. **Fix narrowly.** Change the smallest thing that removes the cause. Don't
   weaken an existing check, test or approval to make something else pass.
3. **Prove it.** The same test passes with the fix. If the test can't run on
   the old code (it calls a new helper), say so and explain why the old code
   was vulnerable.
4. **Look for siblings.** Search for the same pattern elsewhere (another
   registry, another caller of the same helper, the same exec or path
   handling in a different package) and fix or note each one.
5. **Check the whole package**, then follow `gubern-dev-checks`.
6. **Write it up plainly.** The commit message says what an attacker or a
   misbehaving model could do before, what limits it (for example "the tool
   sends no API key"), and what the fix does. Don't overstate: if you only
   proved it in a unit test, don't claim it was exploited on a running server.

Never put real credentials, tokens or customer data in tests, logs or commit
messages.
