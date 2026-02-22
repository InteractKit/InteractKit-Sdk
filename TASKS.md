# Task Group Reference

A task group is a JSON file that breaks a conversation into steps. Each step is a **task**. The LLM sees all currently visible tasks at once and must complete each one fully before moving on. Tasks unlock automatically as their predecessors complete.

---

## Task Fields

| Field | Required | Description |
|---|---|---|
| `id` | yes | Unique key |
| `name` | yes | Short label |
| `instructions` | yes | What the LLM should do while this task is active |
| `variables` | no | Data to collect — task completes when all are filled |
| `visible_after_task_id` | no | Task is hidden until the referenced task completes |
| `disappear_after_task_id` | no | Task is removed once the referenced task completes |

## Variable Fields

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique across the whole group. Becomes the `set_<name>` tool. |
| `description` | yes | What is being collected |
| `enum` | no | Fixed list of valid values (use underscores, e.g. `new_customer`) |
| `regex` | no | Go regex the value must match |
| `example` | no | Example value shown to the LLM |

---

## Patterns

### Sequential — one task unlocks the next

```json
{ "id": "step_a" },
{ "id": "step_b", "visible_after_task_id": "step_a" },
{ "id": "step_c", "visible_after_task_id": "step_b" }
```

`step_a` starts immediately. `step_b` appears only after `step_a` completes. `step_c` only after `step_b`.

---

### Multi-task — several tasks visible at once

Give multiple tasks the same `visible_after_task_id`. They all unlock together. The LLM works through them one at a time, in config order.

```json
{ "id": "collect_name",  "visible_after_task_id": "greeting" },
{ "id": "collect_email", "visible_after_task_id": "greeting" },
{ "id": "collect_phone", "visible_after_task_id": "greeting" }
```

All three appear after `greeting` completes. The LLM works through them one at a time, in the order they appear in the config.

---

### Branching — mutually exclusive paths

Both tasks unlock at the same time. Each disappears once the other completes. The LLM picks the right path from context — when one path's variables are all collected, the other path vanishes.

```json
{ "id": "new_project",  "visible_after_task_id": "reason", "disappear_after_task_id": "existing_job" },
{ "id": "existing_job", "visible_after_task_id": "reason", "disappear_after_task_id": "new_project" }
```

If `new_project` completes, `existing_job` disappears, and vice versa.

---

### Dialogue-only — no data to collect

Omit `variables`. The task never auto-completes — the application must call `CompleteCurrentTask()` when the step is done (e.g. after reading back a summary). `CompleteCurrentTask` always completes the first visible task in config order.

```json
{ "id": "close", "name": "Confirm & Close", "instructions": "Summarise everything and close warmly.", "visible_after_task_id": "schedule" }
```

---

## Full Example

```json
{
  "namespace": "service_intake_v1",
  "base_instructions": "You are Sam, a service coordinator. Be warm and concise — never ask two things at once.",
  "initial_task_id": "greeting",
  "global_tools": [],
  "tasks": [
    {
      "id": "greeting",
      "name": "Greeting & Name",
      "instructions": "Greet the caller and get their name.",
      "variables": [
        { "name": "caller_name", "description": "The caller's full name.", "example": "Jane Smith" }
      ]
    },
    {
      "id": "reason",
      "name": "Reason for Call",
      "instructions": "Ask what brought them in and classify it.",
      "variables": [
        { "name": "call_reason", "description": "Why they are calling.", "enum": ["new_job", "existing_job", "other"] }
      ],
      "visible_after_task_id": "greeting"
    },
    {
      "id": "new_project",
      "name": "New Project Details",
      "instructions": "Collect the service type and property address for the new job.",
      "variables": [
        { "name": "service_type", "description": "Type of service.", "enum": ["interior", "exterior", "other"] },
        { "name": "address", "description": "Property address.", "regex": ".{5,}", "example": "123 Main St, Denver CO" }
      ],
      "visible_after_task_id": "reason",
      "disappear_after_task_id": "existing_job"
    },
    {
      "id": "existing_job",
      "name": "Existing Job Reference",
      "instructions": "Get a reference to look up the job — job number, start date, or address.",
      "variables": [
        { "name": "job_reference", "description": "Job number, start date, or address.", "regex": ".{3,}", "example": "Job #1234" }
      ],
      "visible_after_task_id": "reason",
      "disappear_after_task_id": "new_project"
    },
    {
      "id": "schedule",
      "name": "Schedule",
      "instructions": "Ask for a preferred day and time for the visit.",
      "variables": [
        { "name": "preferred_day", "description": "Preferred day or days.", "regex": ".{3,}", "example": "Monday or Tuesday" },
        { "name": "preferred_time", "description": "Preferred time of day.", "enum": ["morning", "afternoon", "evening", "flexible"] }
      ],
      "visible_after_task_id": "new_project"
    },
    {
      "id": "close",
      "name": "Confirm & Close",
      "instructions": "Read back a summary of everything collected and close warmly.",
      "visible_after_task_id": "schedule"
    }
  ]
}
```
