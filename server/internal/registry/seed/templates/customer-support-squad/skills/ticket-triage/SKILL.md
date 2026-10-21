---
name: ticket-triage
description: Classify a support issue by urgency and topic, and decide who should handle it next.
---

# Ticket triage

Use this skill when a new support issue is assigned to you.

1. Read the issue title, description and any comments.
2. Decide the **urgency**: `urgent` (service down, data loss, security),
   `high` (a core workflow is blocked), `normal` (a question or minor bug) or
   `low` (feature request, feedback).
3. Decide the **topic**: billing, account, integration, bug, how-to, other.
4. Post one comment in this shape:

```
Triage: <urgency> / <topic>
Summary: <one sentence in the customer's words>
Next: <reply | escalate to engineering | ask for details>
```

5. If the next step is a reply, hand the issue to the responder. If details
   are missing, ask the customer one precise question instead of guessing.

Never change the customer's description, and never close an issue yourself.
