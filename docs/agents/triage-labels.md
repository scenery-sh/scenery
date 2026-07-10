# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the `Status:` values used by local Markdown issues.

| Role in mattpocock/skills | Local `Status:` value | Meaning                                  |
| ------------------------- | --------------------- | ---------------------------------------- |
| `needs-triage`            | `needs-triage`        | Maintainer needs to evaluate this issue  |
| `needs-info`              | `needs-info`          | Waiting on reporter for more information |
| `ready-for-agent`         | `ready-for-agent`     | Fully specified, ready for an AFK agent  |
| `ready-for-human`         | `ready-for-human`     | Requires human implementation            |
| `wontfix`                 | `wontfix`             | Will not be actioned                     |

When a skill mentions a role (for example, "apply the AFK-ready triage label"), write the corresponding value from this table into the issue's `Status:` field.

Edit the middle column if this repo's status vocabulary changes.
