---
name: pr-review
description: Review a pull request for correctness, scope and tests, and report findings in a fixed shape.
---

# Pull request review

Use this skill when an issue links a pull request you are asked to review.

1. Read the issue first: what was the change supposed to do?
2. Read the whole diff before commenting. Note anything that does not serve
   the issue's goal.
3. Look, in this order, for: behaviour that breaks existing callers, missing
   error handling, missing or weakened tests, and changes outside the stated
   scope.
4. Post one comment:

```
Review: <approve | request changes>
Blocking:
- <file:line> <what is wrong and why it matters>
Suggestions:
- <optional improvements, each one line>
Tests: <what was run, or what is missing>
```

Report only what you verified in the diff. If you could not run the tests,
say so instead of guessing their result.
