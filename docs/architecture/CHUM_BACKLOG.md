# CHUM Strategic Overview & Backlog

*This is a strategic, 10,000-foot view of the battlefield for CHUM, the distributed, asynchronous, self-healing execution engine using a Git-backed DAG (`beads`) and a deterministic durability layer (Temporal).*

## 1. The Battlefield: Who Else is Doing This?

Right now, the agentic AI space is violently bifurcating into two camps: **The Monoliths** and **The Frameworks**.

**The Monoliths (Cognition/Devin, Magic.dev, OpenHands)**
These are the VC darlings trying to build an "AI Software Engineer in a box."

* *How they do it:* They rely on massive compute, 100M+ token context windows, and proprietary IDEs. You give them a ticket, dump the entire codebase into their brain, and let them run wild in a sandbox for an hour.
* *Are they doing it better?* They have incredible UI/UX and massive capital. But they suffer from **Trajectory Drift**. If Devin takes a wrong turn at minute 5, it hallucinates for 55 minutes and you pay for the compute.
* *Why CHUM wins:* You pulled the planning *out* of the LLM's context window and put it on your hard drive via Steve Yegge's `beads`. By forcing the architecture into explicit, dependency-mapped micro-tasks *before* execution, your Sharks only execute perfectly scoped chum. It’s mathematically cleaner and prevents drift.

**The Frameworks (LangGraph, CrewAI, AutoGen)**
This is what 90% of open-source developers are building with.

* *How they do it:* They use Python state machines to pass messages between "Coder" and "Tester" agents in memory.
* *Are they doing it better?* Absolutely not. LangGraph is a toy state machine trying to do Temporal's job. If the Python script crashes or the Anthropic API times out, the state is corrupted. You are using Temporal, which was built to handle millions of asynchronous microservices without dropping a single byte of state.

## 2. The Pirate Ships: Who Could Snipe You?

If you want to know who is going to snipe you, look at who already owns the three pillars of your system: **The Graph, The Execution, and The Code.**

* **The #1 Threat: GitHub / Microsoft.** They have GitHub Issues (The Graph). They have GitHub Actions (Temporal/Execution). They have Copilot Workspaces (The Sharks). Right now, these three things are siloed. If the VP of Product at GitHub wakes up tomorrow and wires an async LLM orchestrator to auto-slice Issues into a DAG, triggering headless Copilot Agents to execute them via Actions—they have just built CHUM with 100 million built-in users.
* **The IDE Snipers (Cursor / Anysphere):** Cursor won the editor war. They are already moving toward "Background Agents" that work while you sleep. If they implement a hyper-kanban DAG instead of a linear chat window, they eat the local developer workflow.
* **The Infrastructure Snipers (DBOS):** DBOS (Database OS) is a new framework that provides "durable execution" directly inside Postgres transactions, without needing a separate Temporal server. If your Temporal infrastructure ever feels too heavy, DBOS is the exact lightweight equivalent someone else will use to build a competing engine.

## 3. Ideas to Steal: Upgrading the CHUM Bucket (Backlog)

To stay ahead of the snipers, you need to steal the best ideas from the bleeding edge and bolt them onto the factory floor.

* **Test-Time Compute (Monte Carlo Execution):** Steal this from the OpenAI o-series methodology. Compute is cheap. Instead of *one* Shark writing the code and *one* Codex reviewing it, have Temporal spawn **3 parallel Sharks** to write 3 different implementations of the same Bead simultaneously. Run all 3 through Semgrep and your test suite. The one that passes the fastest/cleanest gets merged; the other two are killed. Temporal excels at this "fan-out/fan-in" pattern.
* **Model Context Protocol (MCP):** This is Anthropic's open-source "USB-C of AI." Do not write custom API integrations for your Sharks. Embed an open-source MCP client in your Temporal Worker. Instantly, your Sharks can natively and securely read Jira, query AWS S3 buckets, pull from Figma, or run SQL on your production database using standardized servers.
* **Ephemeral Sandboxing (Firecracker microVMs):** OpenClaw is dangerous because it runs on a host machine. If a Shark hallucinates and runs `rm -rf /`, you are dead. Temporal must spawn a temporary Docker container or AWS Firecracker microVM for each Bead, mount the codebase, execute the Shark, extract the diff, and instantly incinerate the container. Total blast-radius containment.
* **AST-Driven Radar (Tree-sitter):** The Groombot shouldn't just read the `beads` JSON. It should use `tree-sitter` to generate a highly compressed, mathematical map of every function and class in your codebase (an AST Repo Map). This gives the Groombot spatial awareness of *where* the dependencies lie without burning tokens reading the actual logic.

## 4. Blue Sky & Blue Ocean: Beyond Software

This is the billion-dollar realization. You didn't just build an AI coding tool. **You built a Fault-Tolerant, Autonomous Task Resolution Engine.**

Software development is just the easiest place to test it because code is deterministic (it either compiles or it doesn't). But once CHUM is stable, you can swap out the Sharks and apply this engine to *any* industry that relies on complex, multi-step knowledge work. You abstract the `do_work()` function, and the factory can build anything.

* **Corporate Law & M&A (The Legal Factory):**
  * *The Epic:* "Audit this 40,000-page Merger & Acquisition data room."
  * *The Groombot:* Slices the M&A into a DAG of 500 individual clauses, IP checks, and HR liabilities.
  * *The Sharks:* Opus drafts the IP transfer clause. A specialized Legal-Codex agent acts as Opposing Counsel, looking for loopholes (Adversarial Loop). Temporal ensures every clause passes a conflict-check before merging the final PDF.

* **B2B Enterprise RFPs / Grant Writing (Your wheelhouse):**
  * *The Epic:* "Answer this 300-question technical RFP from a Fortune 500 bank."
  * *The Process:* Groombot parses the PDF, slices it into 300 Beads. Sharks query your company's internal Confluence/Codebase via MCP to answer each question. The Checker Shark enforces the company's "voice" and compliance rules. A 3-week human headache is solved in 14 minutes.

* **Self-Healing IT / Cyber Ops:**
  * *The Epic:* A CrowdStrike or Sentry alert fires in production.
  * *The Process:* The alert automatically triggers a Temporal workflow. A Shark spins up a sandbox, reproduces the bug from the stack trace, writes a SQL patch, tests it, and drops a Matrix message: *"Found the deadlock. Here is the patch. Reply 'Approve' to deploy."*

* **Pharmaceutical Research & Literature Synthesis:**
  * *The Epic:* "Find the correlation between X compound and Y side effect in the last 10 years of medical journals."
  * *The Process:* Groombot slices the research database by year and journal. Sharks read and extract data into structured SQLite tables. The Checker verifies the statistical P-values. Learner updates the global thesis.

## The Strategic Playbook

You are holding lightning in a bottle. The fact that you are using Temporal means your system has actual industrial durability, which 99% of "AI Agent" wrappers lack.

Keep it local, keep it ruthless, and dogfood it on your own revenue-generating work.

If GitHub or Cursor snipe the software engineering use-case in 6 months, it won't matter. You simply pivot the CHUM engine into Legal, Ops, or Finance, where the tech giants aren't looking, and you deploy a post-human workforce into an industry still billing by the hour.
