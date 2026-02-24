---
description: How to start an interactive sprint planning session with the CHUM turtle (chief/scrum master)
---

# Sprint Planning Ceremony

Use this API to start an interactive planning session for new work, reprioritisation, or exploring ideas. The turtle (chief/scrum master) walks through items one at a time.

## API Base

```
http://127.0.0.1:8900
```

## Start a Planning Session

```bash
curl -X POST http://127.0.0.1:8900/planning/start \
  -H "Content-Type: application/json" \
  -d '{
    "project": "golf-directory",
    "agent": "claude",
    "tier": "fast"
  }'
```

**Response:** Returns a `workflow_id` for the planning session.

## How the Session Works

The ceremony is signal-driven — each phase waits for your input before proceeding:

### 1. Backlog Presented
The turtle grooms and presents items ranked by priority. Highest priority first, one at a time.

### 2. Select an Item
Signal the workflow with your choice:
```bash
curl -X POST http://127.0.0.1:8900/workflows/{workflow_id}/signal \
  -H "Content-Type: application/json" \
  -d '{"signal": "item-selected", "data": "task-id-here"}'
```

### 3. Clarifying Questions (Sequential)
The turtle asks questions one at a time, each informed by your previous answer:
- What is the problem?
- Why is it important now?
- What options exist?
- Recommended approach and why

Answer each question:
```bash
curl -X POST http://127.0.0.1:8900/workflows/{workflow_id}/signal \
  -H "Content-Type: application/json" \
  -d '{"signal": "answer", "data": "Your answer here"}'
```

### 4. Summary
The turtle presents: **what** will be done, **why**, estimated **effort**, and **DoD checks**.

### 5. Greenlight
Approve to send it to the sharks, or loop back to re-groom:
```bash
# Approve — throws it to the sharks
curl -X POST http://127.0.0.1:8900/workflows/{workflow_id}/signal \
  -H "Content-Type: application/json" \
  -d '{"signal": "greenlight", "data": "GO"}'

# Reject — loops back for another planning cycle (up to 5)
curl -X POST http://127.0.0.1:8900/workflows/{workflow_id}/signal \
  -H "Content-Type: application/json" \
  -d '{"signal": "greenlight", "data": "REALIGN"}'
```

## When to Use This

- **New work** that needs scoping before execution
- **Reprioritisation** of existing backlog items
- **Exploring ideas** before committing resources
- **Complex tasks** the sharks can't solve autonomously

## NOT for Routine Dispatch

The daily 5 AM strategic groom handles autonomous work — scanning the backlog, creating/reprioritising tasks, and dispatching ready morsels to sharks. The planning ceremony is for **human-interactive** sessions only.
