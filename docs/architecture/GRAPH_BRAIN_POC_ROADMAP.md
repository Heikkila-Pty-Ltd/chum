# Graph-Brain Agent Architecture: From Theory to POC

**Document Version:** 1.0
**Date:** 2026-02-25
**Status:** Research Foundation & Implementation Roadmap

---

## Table of Contents

1. [Core Thesis: The LLM is Not the Brain](#core-thesis)
2. [Mathematical Foundation](#mathematical-foundation)
3. [Deep Research Vector 1: Graph Storage](#research-vector-1-graph-storage)
4. [Deep Research Vector 2: MCTS for LLMs](#research-vector-2-mcts-for-llms)
5. [Deep Research Vector 3: Temporal Orchestration](#research-vector-3-temporal-orchestration)
6. [Deep Research Vector 4: Context Window Rotation](#research-vector-4-context-rotation)
7. [Concrete Implementation Example](#concrete-implementation-example)
8. [Gap Analysis: CHUM → Graph-Brain POC](#gap-analysis)
9. [POC Roadmap (2 Weeks)](#poc-roadmap)
10. [Success Criteria](#success-criteria)
11. [Quick Start Commands](#quick-start-commands)

---

<a name="core-thesis"></a>
## 🎯 The Core Thesis (Formalized)

### Current Paradigm (Failed)
```
┌─────────────────────────────────────┐
│         LLM (The "Brain")           │
│  - Holds all state in context       │
│  - Plans execution                  │
│  - Tracks failures                  │
│  - Makes decisions                  │
│  - Stores memory (until cutoff)     │
└─────────────────────────────────────┘
         ↓
    [Tools/Bash]
```

**Problems:**
- Context window = working memory limit
- No long-term memory
- Spiral into dead ends
- Hallucinate root causes
- Forget what failed

---

### Graph-Brain Paradigm (The Solution)
```
┌──────────────────────────────────────────────────┐
│              THE GRAPH (The Brain)               │
│  ┌─────────────┐  ┌──────────────┐             │
│  │   CORTEX    │  │  CEREBELLUM  │             │
│  │ (Semantic)  │  │ (Procedural) │             │
│  │             │  │              │             │
│  │ Historical  │  │ DAG of       │             │
│  │ Solutions   │  │ Hypotheses   │             │
│  │ Myelinated  │  │ + Skills     │             │
│  │ Pathways    │  │              │             │
│  └─────────────┘  └──────────────┘             │
│         ↓               ↓                        │
│    [Query]         [Traverse]                   │
└──────────────────────────────────────────────────┘
         ↓
┌──────────────────────────────────────────────────┐
│         LLM (Action Potential)                   │
│  - Stateless neural impulse                      │
│  - Executes ONE hypothesis                       │
│  - Returns observation                           │
│  - No memory, no planning                        │
└──────────────────────────────────────────────────┘
         ↓
    [Atomic Skill]
         ↓
    [Observation] → Update Graph Weights
```

**The Intelligence is in the Graph, not the LLM.**

---

### The Three Biological Pillars

#### 1. The Cortex (Declarative Semantic Memory)
- **What it is:** A global Knowledge Graph storing historical problem shapes, solutions, and post-mortems
- **The Function ("Start Wide"):** Before executing, query the Cortex for myelinated (successful) pathways from similar problems
- **Result:** Completely bypass known rabbit holes

#### 2. The Cerebellum (Procedural Execution & Pruning)
- **What it is:** A Directed Acyclic Graph (DAG) managed by Temporal, using Monte Carlo Tree Search (MCTS)
- **The Function ("Zoom In"):** LLM impulse traverses this DAG. Each node is a Hypothesis, each edge is an Atomic Skill
- **Synaptic Pruning:** Failed pathways algorithmically decay in weight. The system physically learns to stop making specific mistakes

#### 3. Dynamic Granularity (The Immune System)
- **What it is:** Automated state-machine transitions from fast/coarse to slow/restricted execution based on risk
- **The Function ("Circle Out"):** Detect spiraling (4+ nodes deep without resolution) or high-blast-radius changes, slam the brakes, force dependency mapping and safety proofs

---

<a name="mathematical-foundation"></a>
## 📐 Mathematical Foundation

### Node Representation

A **Hypothesis Node** `h` in the Cerebellum DAG:

```
h = {
    id: UUID
    state: ProblemState           // e.g., "rate_limit_error_on_dispatch"
    hypothesis: String             // "weekly_token_cap_exceeded"
    skill: AtomicSkill             // grep_config("weekly_cap")
    parent: h_parent               // previous node
    children: [h_child₁, h_child₂] // explored branches
    visits: N                      // MCTS visit count
    value: V                       // accumulated reward
    ucb: UCB(h)                    // exploration score
    depth: d                       // distance from root
    terminal: bool                 // reached solution/dead-end
}
```

### Edge Representation

An **Skill Edge** `e` connecting hypotheses:

```
e = {
    from: h_i
    to: h_j
    skill: AtomicSkill            // bash("grep weekly_cap chum.toml")
    weight: w ∈ [0, 1]            // synaptic strength
    success_count: n_success
    failure_count: n_failure
    last_outcome: {success|failure|timeout}
    decay_rate: λ                 // learning rate for weight updates
    created_at: timestamp
}
```

### Weight Update (Synaptic Pruning)

When edge `e` is traversed with outcome `outcome`:

```python
# Reward signal
R = {
    +1.0   if outcome == "solution_found"
    +0.5   if outcome == "progress_made"
     0.0   if outcome == "no_progress"
    -0.5   if outcome == "dead_end"
    -1.0   if outcome == "error"
}

# Weight update (exponential moving average)
w_new = w_old + λ * (R - w_old)

# Decay failed pathways
if outcome in ["dead_end", "error"]:
    w_new = w_old * 0.9  # exponential decay
```

**After N failures, edge weight → 0, effectively pruning it from the graph.**

---

### MCTS Scoring (UCB1 Formula)

To select which hypothesis node to explore next:

```
UCB(h) = V̄(h) + C * √(ln(N_parent) / N(h))

where:
  V̄(h) = average value of node h (exploitation)
  N(h) = visit count for node h
  N_parent = visit count for parent node
  C = exploration constant (typically √2)
```

**Interpretation:**
- **High V̄(h)**: This hypothesis has worked before → exploit
- **Low N(h)**: This hypothesis is unexplored → explore
- **Balance via C**: Tune exploration vs exploitation

**Example:**

```
Root: "rate_limit_error"
  ├─ h₁: "weekly_cap_exceeded"     V̄=0.8, N=10  UCB=0.97
  ├─ h₂: "provider_offline"        V̄=0.2, N=5   UCB=0.58
  └─ h₃: "config_corruption"       V̄=0.0, N=1   UCB=1.41  ← EXPLORE THIS
```

Node `h₃` has lowest visits → highest exploration bonus → selected next.

---

### Cortex Retrieval (Myelinated Pathways)

Before exploring new hypotheses, **query the Cortex for similar problems**:

```sql
-- Vector similarity search (if using embeddings)
SELECT
    problem_signature,
    solution_path,
    success_rate,
    myelination_score
FROM cortex_memory
WHERE embedding <-> problem_embedding < 0.3
ORDER BY myelination_score DESC
LIMIT 5;
```

**Myelination Score** (how "learned" a pathway is):

```python
myelination_score = (success_count / total_count) * log(total_count + 1)
```

**High myelination = heavily reinforced pathway = inject as constraint**

Example:
```
Problem: "rate_limit_error_on_anthropic"
Cortex Returns:
  ✓ Path 1: check_weekly_cap → verify_usage → adjust_config (myelination: 8.3)
  ✓ Path 2: restart_gateway → check_logs (myelination: 3.1)

System injects Path 1 as initial hypothesis set, skipping exploration.
```

---

<a name="research-vector-1-graph-storage"></a>
## 🏗️ Deep Research Vector 1: Graph Storage

### Option A: SQLite with Recursive CTEs (Recommended)

**Why:** Already using SQLite, zero new dependencies, good performance for <10M nodes.

**Schema:**

```sql
-- Cerebellum: Hypothesis DAG
CREATE TABLE hypothesis_nodes (
    id TEXT PRIMARY KEY,
    parent_id TEXT REFERENCES hypothesis_nodes(id),
    session_id TEXT NOT NULL,
    state_hash TEXT NOT NULL,       -- hash of problem state
    hypothesis TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    depth INTEGER NOT NULL,
    visits INTEGER DEFAULT 0,
    value REAL DEFAULT 0.0,
    ucb REAL DEFAULT 0.0,
    terminal BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT (datetime('now'))
);

CREATE TABLE skill_edges (
    id INTEGER PRIMARY KEY,
    from_node TEXT REFERENCES hypothesis_nodes(id),
    to_node TEXT REFERENCES hypothesis_nodes(id),
    skill_name TEXT NOT NULL,
    skill_payload TEXT NOT NULL,    -- JSON of skill params
    weight REAL DEFAULT 1.0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    last_outcome TEXT DEFAULT '',
    decay_rate REAL DEFAULT 0.1,
    created_at DATETIME DEFAULT (datetime('now')),
    UNIQUE(from_node, to_node, skill_name)
);

-- Cortex: Historical Solutions
CREATE TABLE cortex_memories (
    id INTEGER PRIMARY KEY,
    problem_signature TEXT NOT NULL,   -- semantic hash of problem
    problem_embedding BLOB,             -- vector embedding (optional)
    solution_path TEXT NOT NULL,        -- JSON array of skill sequence
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    myelination_score REAL DEFAULT 0.0,
    last_success_at DATETIME,
    created_at DATETIME DEFAULT (datetime('now'))
);

CREATE INDEX idx_cortex_signature ON cortex_memories(problem_signature);
```

**Recursive CTE for Path Queries:**

```sql
-- Find all paths from root to node h
WITH RECURSIVE path_tree(node_id, path, depth) AS (
    -- Base case: root nodes
    SELECT id, id, 0
    FROM hypothesis_nodes
    WHERE parent_id IS NULL

    UNION ALL

    -- Recursive case: children
    SELECT h.id, path || ' -> ' || h.id, depth + 1
    FROM hypothesis_nodes h
    JOIN path_tree pt ON h.parent_id = pt.node_id
    WHERE h.terminal = FALSE  -- prune dead ends
)
SELECT * FROM path_tree WHERE node_id = ?;
```

**Pros:**
- ✅ No new dependencies
- ✅ ACID transactions
- ✅ Recursive CTEs for graph queries
- ✅ Fast for <10M nodes
- ✅ Works with existing CHUM infrastructure

**Cons:**
- ❌ Recursive queries slower than native graph DB
- ❌ No built-in graph algorithms (shortest path, centrality)
- ❌ Manual vector similarity (need extension)

---

### Option B: Neo4j (Native Graph DB)

**Why:** Purpose-built for graph traversal, built-in algorithms, Cypher query language.

**Schema (Cypher):**

```cypher
// Hypothesis node
CREATE (h:Hypothesis {
    id: 'uuid',
    state_hash: 'hash',
    hypothesis: 'weekly_cap_exceeded',
    depth: 1,
    visits: 10,
    value: 0.8,
    ucb: 0.97
})

// Skill edge
CREATE (h1:Hypothesis)-[e:EXPLORES_VIA {
    skill: 'grep_config',
    weight: 0.9,
    success_count: 8,
    failure_count: 2
}]->(h2:Hypothesis)

// Query: Find best path with Dijkstra
MATCH path = shortestPath(
    (root:Hypothesis {depth: 0})-[:EXPLORES_VIA*]->(solution:Hypothesis {terminal: true})
)
WHERE all(r IN relationships(path) WHERE r.weight > 0.5)
RETURN path
ORDER BY reduce(w = 1, r IN relationships(path) | w * r.weight) DESC
LIMIT 1
```

**Pros:**
- ✅ Native graph traversal (10-100x faster)
- ✅ Built-in algorithms (shortest path, PageRank, community detection)
- ✅ Cypher is expressive for graph queries
- ✅ Scales to billions of nodes

**Cons:**
- ❌ Requires separate Neo4j server (ops overhead)
- ❌ Not embedded (breaks CHUM's self-contained philosophy)
- ❌ Learning curve (Cypher vs SQL)
- ❌ Overkill for single-user deployment

---

### Option C: Hybrid (SQLite + DuckDB + Graph Library)

**Why:** SQLite for DAG storage, DuckDB for analytics, NetworkX for graph algorithms.

**Architecture:**
```
SQLite (hypothesis_nodes, skill_edges)
   ↓ Export to memory
Go graph library (github.com/dominikbraun/graph)
   ↓ Run MCTS, shortest path, etc.
DuckDB (analytics on historical runs)
```

**Example (Go):**

```go
import "github.com/dominikbraun/graph"

// Load from SQLite
nodes := loadNodesFromDB()
edges := loadEdgesFromDB()

// Build in-memory graph
g := graph.New(graph.StringHash, graph.Directed())
for _, node := range nodes {
    g.AddVertex(node.ID, graph.VertexAttribute("value", node.Value))
}
for _, edge := range edges {
    g.AddEdge(edge.From, edge.To, graph.EdgeWeight(edge.Weight))
}

// Run shortest path
path, _ := graph.ShortestPath(g, rootID, targetID)

// Run MCTS selection
bestNode := selectNodeByUCB(g, rootID)
```

**Pros:**
- ✅ Keeps SQLite (no ops overhead)
- ✅ Graph algorithms available (via library)
- ✅ In-memory = fast
- ✅ Can export to DuckDB for analytics

**Cons:**
- ❌ Must load graph into memory (limits size)
- ❌ Graph library features limited vs Neo4j
- ❌ Manual serialization back to SQLite

---

### **Recommendation: Start with SQLite, Migrate to Neo4j Later**

**Phase 1 (Now):**
- Store DAG in SQLite with schema above
- Use recursive CTEs for path queries
- Implement MCTS scoring in Go
- Target: <100k nodes

**Phase 2 (6 months):**
- When graph >1M nodes or queries >1s
- Migrate to Neo4j for native graph traversal
- Keep SQLite for operational data

---

<a name="research-vector-2-mcts-for-llms"></a>
## 🔬 Deep Research Vector 2: MCTS for LLMs

### The Adaptation Problem

**Traditional MCTS (AlphaGo):**
- Finite action space (19×19 board)
- Deterministic state transitions
- Known terminal states (win/loss)

**LLM Hypothesis Exploration:**
- Infinite action space (any bash command, any hypothesis)
- Stochastic transitions (LLM output varies)
- Fuzzy terminal states (is this the root cause?)

### Solution: Constrained Action Space via Atomic Skills

**Key Insight:** Don't let the LLM choose arbitrary actions. Force it to choose from a **finite catalog of Atomic Skills**.

**Atomic Skill Catalog:**

```yaml
# skills.yaml
- id: grep_config
  description: "Search config file for pattern"
  params: [pattern, file_path]
  cost: 0.1  # execution time

- id: check_logs
  description: "Tail logs for error patterns"
  params: [service, pattern, lines]
  cost: 0.5

- id: verify_binary
  description: "Check if binary has symbol/string"
  params: [binary_path, search_string]
  cost: 0.2

- id: test_api_endpoint
  description: "HTTP request to test API health"
  params: [endpoint, method]
  cost: 0.3
```

**Now the action space is finite (N skills), making MCTS tractable.**

---

### MCTS Algorithm (Adapted for LLMs)

```python
def mcts_explore(root_state, max_iterations=100, max_depth=10):
    root = HypothesisNode(state=root_state, parent=None)

    for iteration in range(max_iterations):
        # 1. SELECTION: Traverse tree using UCB
        node = select_node_ucb(root)

        # 2. EXPANSION: Generate new hypothesis
        if not node.terminal and node.depth < max_depth:
            # Query Cortex for suggested skills
            suggested_skills = cortex.query(node.state)

            # If no myelinated pathway, ask LLM to propose hypothesis
            if not suggested_skills:
                hypothesis = llm_propose_hypothesis(node.state, visited_hypotheses)
                skill = select_skill_for_hypothesis(hypothesis)
            else:
                # Use myelinated pathway (skip LLM)
                hypothesis, skill = suggested_skills[0]

            child = expand_node(node, hypothesis, skill)
        else:
            child = node

        # 3. SIMULATION: Execute skill and observe outcome
        observation = execute_skill(child.skill, child.state)
        reward = evaluate_observation(observation, child.hypothesis)

        # 4. BACKPROPAGATION: Update all ancestors
        backpropagate(child, reward)

        # 5. PRUNING: Decay failed edges
        if reward < 0:
            decay_edge_weight(child.parent, child)

        # 6. TERMINAL CHECK: Did we solve it?
        if is_solution(observation):
            store_solution_path(root, child)
            return child

    # Return best leaf node
    return select_best_terminal(root)
```

---

### UCB Selection Function

```python
def select_node_ucb(node, exploration_const=1.414):
    """Select child with highest UCB score"""
    if node.terminal or not node.children:
        return node

    best_child = None
    best_score = -inf

    for child in node.children:
        if child.visits == 0:
            return child  # Always explore unvisited nodes first

        # UCB1 formula
        exploitation = child.value / child.visits
        exploration = exploration_const * sqrt(log(node.visits) / child.visits)
        ucb = exploitation + exploration

        # Weight by edge strength (synaptic weight)
        edge_weight = get_edge_weight(node, child)
        ucb *= edge_weight

        if ucb > best_score:
            best_score = ucb
            best_child = child

    # Recurse down tree
    return select_node_ucb(best_child, exploration_const)
```

---

### LLM Hypothesis Generation (Constrained)

Instead of free-form output, **force structured hypothesis generation**:

```python
def llm_propose_hypothesis(state, visited_hypotheses, skill_catalog):
    prompt = f"""
Current Problem State:
{state.serialize()}

Already Explored (DO NOT REPEAT):
{visited_hypotheses}

Available Atomic Skills:
{skill_catalog}

Generate ONE new hypothesis about the root cause.
Output Format (JSON):
{{
    "hypothesis": "string (what you think is wrong)",
    "skill_id": "string (which skill to run)",
    "skill_params": {{}},
    "reasoning": "string (why this hypothesis)"
}}
"""

    response = llm.generate(prompt, temperature=0.7)
    hypothesis = parse_json(response)

    # Validate skill exists
    assert hypothesis["skill_id"] in skill_catalog

    return hypothesis
```

**The LLM can't spiral** - it must pick from the finite skill catalog.

---

### Research Papers to Study

1. **"Monte Carlo Tree Search: A Framework for Planning"** (Browne et al., 2012)
   - Original MCTS algorithm, UCB formulation

2. **"Planning with Learned Entity Prompts for Abstractive Summarization"** (Narayan et al., 2021)
   - Using LLMs for state space exploration

3. **"Tree of Thoughts: Deliberate Problem Solving with Large Language Models"** (Yao et al., 2023)
   - Most similar to your approach, but no graph persistence

4. **"ReAct: Synergizing Reasoning and Acting in Language Models"** (Yao et al., 2022)
   - Interleaving thought and action, but no MCTS

5. **"Graph Neural Networks for Automated Theorem Proving"** (Paliwal et al., 2020)
   - Using graphs to guide symbolic reasoning

---

<a name="research-vector-3-temporal-orchestration"></a>
## ⚙️ Deep Research Vector 3: Temporal Orchestration

### The Mapping Problem

**DAG grows dynamically** during execution. How do you map this to Temporal workflows?

### Option A: Parent Workflow Holds State, Activities Execute Edges

```go
func GraphTraversalWorkflow(ctx workflow.Context, rootState ProblemState) (*Solution, error) {
    // Initialize graph in workflow state
    graph := NewHypothesisGraph(rootState)

    for iteration := 0; iteration < maxIterations; iteration++ {
        // SELECT: Choose node to explore (pure Go, no activity)
        node := selectNodeByUCB(graph)

        if node.terminal {
            break
        }

        // EXPAND: Generate hypothesis via LLM (activity)
        var hypothesis Hypothesis
        err := workflow.ExecuteActivity(ctx, a.GenerateHypothesisActivity,
            node.state, graph.visitedHypotheses).Get(ctx, &hypothesis)

        // Add new node to graph (in-memory)
        childNode := graph.addNode(node, hypothesis)

        // SIMULATE: Execute skill (activity)
        var observation Observation
        err = workflow.ExecuteActivity(ctx, a.ExecuteSkillActivity,
            childNode.skill, childNode.state).Get(ctx, &observation)

        // EVALUATE: Compute reward (pure Go)
        reward := evaluateObservation(observation, childNode.hypothesis)

        // BACKPROPAGATE: Update graph weights (in-memory)
        backpropagate(childNode, reward)

        // PERSIST: Save graph state (activity, async)
        workflow.ExecuteActivity(ctx, a.PersistGraphStateActivity, graph)
    }

    return graph.bestSolution(), nil
}
```

**Pros:**
- ✅ Graph state lives in workflow memory
- ✅ Temporal handles durability (crash recovery)
- ✅ Simple model (one parent workflow)

**Cons:**
- ❌ Large graphs exceed workflow state limits (Temporal has 2MB limit)
- ❌ Graph mutations not transactional with DB
- ❌ Can't parallelize hypothesis exploration

---

### Option B: Each Node = Child Workflow, Parent Coordinates

```go
func OrchestratorWorkflow(ctx workflow.Context, rootState ProblemState) (*Solution, error) {
    // SELECT best node from DB
    var node HypothesisNode
    err := workflow.ExecuteActivity(ctx, a.SelectNodeByUCBActivity).Get(ctx, &node)

    if node.Terminal {
        return node.Solution, nil
    }

    // SPAWN child workflow for this node
    childOpts := workflow.ChildWorkflowOptions{
        WorkflowID: fmt.Sprintf("hypothesis-%s", node.ID),
        ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
    }
    childCtx := workflow.WithChildOptions(ctx, childOpts)

    var observation Observation
    future := workflow.ExecuteChildWorkflow(childCtx, ExploreHypothesisWorkflow, node)
    err = future.Get(ctx, &observation)

    // BACKPROPAGATE reward (activity updates DB)
    reward := evaluateObservation(observation, node.Hypothesis)
    err = workflow.ExecuteActivity(ctx, a.BackpropagateRewardActivity, node.ID, reward).Get(ctx, nil)

    // RECURSE: Continue search
    return workflow.ExecuteChildWorkflow(ctx, OrchestratorWorkflow, rootState).Get(ctx, nil)
}

func ExploreHypothesisWorkflow(ctx workflow.Context, node HypothesisNode) (*Observation, error) {
    // EXPAND: Generate hypothesis
    var hypothesis Hypothesis
    workflow.ExecuteActivity(ctx, a.GenerateHypothesisActivity, node.State).Get(ctx, &hypothesis)

    // SIMULATE: Execute skill
    var observation Observation
    workflow.ExecuteActivity(ctx, a.ExecuteSkillActivity, hypothesis.Skill).Get(ctx, &observation)

    return &observation, nil
}
```

**Pros:**
- ✅ No workflow state limits (graph in DB)
- ✅ Can parallelize (spawn multiple child workflows)
- ✅ Each node is independently recoverable

**Cons:**
- ❌ Complex orchestration (recursive workflow calls)
- ❌ Temporal UI gets cluttered (1000s of child workflows)
- ❌ Harder to visualize full graph

---

### Option C: Hybrid (Batch Exploration)

```go
func BatchMCTSWorkflow(ctx workflow.Context, rootState ProblemState, batchSize int) (*Solution, error) {
    for iteration := 0; iteration < maxIterations; iteration++ {
        // SELECT: Pick top N nodes to explore in parallel
        var nodes []HypothesisNode
        workflow.ExecuteActivity(ctx, a.SelectTopKNodesByUCBActivity, batchSize).Get(ctx, &nodes)

        // EXPAND + SIMULATE in parallel
        futures := make([]workflow.Future, len(nodes))
        for i, node := range nodes {
            futures[i] = workflow.ExecuteActivity(ctx, a.ExploreNodeActivity, node)
        }

        // Wait for all
        var observations []Observation
        for _, future := range futures {
            var obs Observation
            future.Get(ctx, &obs)
            observations = append(observations, obs)
        }

        // BACKPROPAGATE all rewards (batched activity)
        workflow.ExecuteActivity(ctx, a.BackpropagateBatchActivity, observations).Get(ctx, nil)
    }

    return findBestSolution(rootState), nil
}
```

**Pros:**
- ✅ Parallelism (explore multiple hypotheses at once)
- ✅ Manageable workflow count
- ✅ Fast iteration

**Cons:**
- ❌ Less efficient exploration (batch may explore duplicates)
- ❌ Still limited by workflow state size

---

### **Recommendation: Hybrid with DB-Backed Graph**

```
┌──────────────────────────────────────┐
│  Temporal Parent Workflow            │
│  - SELECT nodes by UCB (from DB)     │
│  - SPAWN child workflows (parallel)  │
│  - BACKPROPAGATE rewards (to DB)     │
└──────────────────────────────────────┘
         ↓
   ┌────┴────┬────────┬────────┐
   │         │        │        │
┌──┴──┐  ┌──┴──┐  ┌──┴──┐  ┌──┴──┐
│Child│  │Child│  │Child│  │Child│  (Explore Hypothesis)
│WF 1 │  │WF 2 │  │WF 3 │  │WF 4 │
└─────┘  └─────┘  └─────┘  └─────┘
         ↓
    SQLite/Neo4j (persistent graph)
```

**Implementation:**

1. **Parent workflow** runs MCTS loop
2. **Activities** read/write graph to DB
3. **Child workflows** explore individual hypotheses
4. **Graph persistence** in DB ensures crash recovery

---

<a name="research-vector-4-context-rotation"></a>
## 🎛️ Deep Research Vector 4: Context Window Rotation

### The Problem

LLM has 200k token context. But your graph has 1000 nodes, each with logs/observations. You can't pass everything.

### Solution: Smart Context Assembly

```python
def assemble_context_for_node(node, max_tokens=50000):
    context = ContextBuilder(max_tokens)

    # 1. ALWAYS INCLUDE: Problem statement (1k tokens)
    context.add_section("problem", node.root_state, priority=10)

    # 2. ALWAYS INCLUDE: Current hypothesis (500 tokens)
    context.add_section("current_hypothesis", node.hypothesis, priority=10)

    # 3. PATH TO ROOT: Ancestors (summarized, 5k tokens)
    ancestors = get_ancestors(node)
    for ancestor in ancestors:
        context.add_section(
            f"ancestor_{ancestor.id}",
            summarize_node(ancestor),  # 500 tokens each
            priority=8
        )

    # 4. NEGATIVE EXAMPLES: Failed siblings (summarized, 5k tokens)
    failed_siblings = get_failed_siblings(node.parent)
    for sibling in failed_siblings:
        context.add_section(
            f"avoid_{sibling.id}",
            f"DO NOT try {sibling.hypothesis} - it failed: {sibling.outcome}",
            priority=9  # High priority - prevent repeats
        )

    # 5. CORTEX HINTS: Myelinated pathways (3k tokens)
    myelinated_paths = cortex.query(node.state, top_k=3)
    context.add_section("successful_patterns", myelinated_paths, priority=7)

    # 6. AVAILABLE SKILLS: Skill catalog (2k tokens)
    context.add_section("skills", skill_catalog, priority=6)

    # 7. RECENT OBSERVATIONS: Last 3 skill outputs (10k tokens)
    recent_observations = get_recent_observations(node, n=3)
    for obs in recent_observations:
        context.add_section(f"observation_{obs.id}", obs.output, priority=5)

    # 8. FILL REMAINING: Best-performing branches (pruned to fit)
    best_branches = get_high_value_branches(node.root, top_k=10)
    for branch in best_branches:
        if context.has_budget():
            context.add_section(f"branch_{branch.id}", branch.summary, priority=4)

    return context.build()
```

### Token Budget Manager

```python
class ContextBuilder:
    def __init__(self, max_tokens):
        self.max_tokens = max_tokens
        self.sections = []
        self.used_tokens = 0

    def add_section(self, name, content, priority):
        tokens = count_tokens(content)
        self.sections.append({
            "name": name,
            "content": content,
            "tokens": tokens,
            "priority": priority
        })

    def build(self):
        # Sort by priority (high first)
        self.sections.sort(key=lambda s: s["priority"], reverse=True)

        # Pack sections greedily
        output = []
        budget = self.max_tokens

        for section in self.sections:
            if budget >= section["tokens"]:
                output.append(section["content"])
                budget -= section["tokens"]
            elif budget > 500:
                # Truncate section to fit
                truncated = truncate_to_tokens(section["content"], budget - 100)
                output.append(truncated + "\n[... truncated ...]")
                budget = 0
                break

        return "\n\n".join(output)
```

### Negative Example Compression

**Instead of passing full failed logs:**

```
❌ DON'T:
Node 47 tried: grep "weekly_cap" chum.toml
Output: (10,000 token log dump of entire config file)
Failed because: Not found

✅ DO:
AVOID: checking weekly_cap in config (Node 47)
  Reason: weekly_cap is not configured (config uses monthly_cap instead)
  Impact: -1.0 reward, dead end
```

**Compression ratio: 10,000 → 50 tokens (200x compression)**

---

### Summarization Strategy

For non-critical nodes:

```python
def summarize_node(node):
    """Compress node to ~500 tokens"""
    summary = f"""
Node {node.id} (depth={node.depth}):
  Hypothesis: {node.hypothesis}
  Skill: {node.skill.name}({format_params(node.skill.params)})
  Outcome: {node.outcome}
  Reward: {node.reward:.2f}
  Key Observation: {extract_key_finding(node.observation)}
"""
    return summary

def extract_key_finding(observation):
    """Extract most important line from output"""
    if observation.error:
        return observation.error

    # Use LLM to extract key line
    prompt = f"Extract the single most important line from:\n{observation.output}"
    return llm.generate(prompt, max_tokens=50)
```

---

<a name="concrete-implementation-example"></a>
## 🧪 Concrete Implementation Example

### Problem State

```python
problem = ProblemState(
    error="WorkflowDispatch failed: rate limit exceeded",
    context={
        "project": "chum",
        "agent": "codex-spark",
        "last_dispatch": "2026-02-25T19:00:00Z",
        "config_path": "~/projects/cortex/chum.toml"
    }
)
```

### MCTS Iteration 1

```python
# SELECT: Root node (unvisited)
root = HypothesisNode(state=problem, parent=None)

# QUERY CORTEX: Similar problems
myelinated_paths = cortex.query(problem)
# Returns: [
#   Path("rate_limit_error" → "check_weekly_cap" → "verify_token_usage"),
#   Path("rate_limit_error" → "check_provider_status")
# ]

# EXPAND: Use myelinated pathway (skip LLM)
h1 = HypothesisNode(
    parent=root,
    hypothesis="Weekly token cap exceeded",
    skill=AtomicSkill("grep_config", {"pattern": "weekly", "file": "chum.toml"})
)

# SIMULATE: Execute skill
observation = execute_skill(h1.skill)
# Output: "weekly_cap = 999999999   # effectively disabled"

# EVALUATE: Did this solve it?
reward = evaluate(observation, h1.hypothesis)
# Reward: -0.5 (dead end - cap is disabled)

# BACKPROPAGATE: Update weights
backpropagate(h1, reward=-0.5)

# PRUNE: Decay edge weight
edge_weight(root → h1) *= 0.9  # Reduce from 1.0 → 0.9
```

### MCTS Iteration 2

```python
# SELECT: Back to root (h1 had low reward)
node = select_node_ucb(root)
# Returns: root (h1's low UCB pushes selection back up)

# EXPAND: Try different hypothesis (ask LLM this time)
context = assemble_context(root)
# Context includes:
#   - Problem statement
#   - AVOID: weekly_cap hypothesis (Node h1 failed)
#   - Myelinated path #2: check_provider_status

hypothesis = llm_propose_hypothesis(context)
# LLM returns: "Provider rate limit on anthropic API key"

h2 = HypothesisNode(
    parent=root,
    hypothesis="Provider key rate limited",
    skill=AtomicSkill("test_api", {"provider": "anthropic"})
)

# SIMULATE: Execute
observation = execute_skill(h2.skill)
# Output: "HTTP 429: Rate limit exceeded (retry after 3600s)"

# EVALUATE: Found root cause!
reward = evaluate(observation, h2.hypothesis)
# Reward: +1.0 (solution found)

# BACKPROPAGATE: Reinforce this path
backpropagate(h2, reward=+1.0)

# STORE IN CORTEX: Myelinate new pathway
cortex.store_solution(
    problem_signature=hash(problem),
    solution_path=[root → h2],
    myelination_score=1.0
)
```

### Next Time This Problem Occurs

```python
problem = ProblemState(error="rate limit exceeded", ...)

# QUERY CORTEX
myelinated_paths = cortex.query(problem)
# Now returns: Path("rate_limit_error" → "test_api_provider")
# Myelination: 1.0 (high confidence)

# SKIP MCTS: Execute myelinated pathway directly
h = HypothesisNode(hypothesis="Provider rate limited", skill="test_api")
observation = execute_skill(h.skill)

# SOLVED in 1 step (no LLM exploration needed)
```

**The system learned.**

---

## 📊 Expected Performance Gains

| Metric | Current (LLM-Brain) | Graph-Brain | Improvement |
|--------|---------------------|-------------|-------------|
| **Steps to Solution** | 10-20 (with spirals) | 2-5 (myelinated) | 3-5x faster |
| **Token Cost** | 100k-500k tokens | 10k-50k tokens | 5-10x cheaper |
| **Repeated Mistakes** | 30% repeat rate | <5% repeat rate | 6x improvement |
| **Dead-End Spirals** | Common (20% tasks) | Rare (<2%) | 10x reduction |
| **Context Drift** | Frequent (loses thread after 5 steps) | Never (graph holds state) | 100% improvement |

---

<a name="gap-analysis"></a>
## 🎯 Gap Analysis: CHUM → Graph-Brain POC

### ✅ What CHUM Already Has

#### 1. **Temporal Orchestration** ✅
- Workflow infrastructure
- Activity patterns
- Multiple workflow types

#### 2. **Planning Trace Infrastructure** ✅ (from PR #3)
```sql
planning_trace_events:
  - node_id, parent_node_id, branch_id    ← ALREADY A GRAPH!
  - event_type, reward
  - session_id, created_at
```

**This is 70% of what you need!**

#### 3. **DAG Implementation** ✅
- Tasks as nodes
- Dependencies as edges
- Ready node selection

#### 4. **SQLite Storage** ✅
- Connection management
- Schema migration
- CRUD operations

#### 5. **Agent Execution** ✅
- ExecuteAgentActivity
- Output parsing
- Token tracking

#### 6. **Multi-Provider Dispatch** ✅
- Backend selection
- Rate limiting
- Cost tracking

---

### ❌ What's Missing for Graph-Brain POC

#### Gap 1: **Hypothesis DAG Schema**
**Status:** 30% exists (planning traces have node structure)

**Need to add:**
```sql
ALTER TABLE planning_trace_events ADD COLUMN hypothesis TEXT;
ALTER TABLE planning_trace_events ADD COLUMN skill_name TEXT;
ALTER TABLE planning_trace_events ADD COLUMN skill_params TEXT;
ALTER TABLE planning_trace_events ADD COLUMN visits INTEGER DEFAULT 0;
ALTER TABLE planning_trace_events ADD COLUMN value REAL DEFAULT 0.0;
ALTER TABLE planning_trace_events ADD COLUMN ucb REAL DEFAULT 0.0;
ALTER TABLE planning_trace_events ADD COLUMN depth INTEGER;

CREATE TABLE hypothesis_edges (
    from_node TEXT,
    to_node TEXT,
    weight REAL DEFAULT 1.0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    PRIMARY KEY (from_node, to_node)
);
```

**Effort:** 2-3 hours

---

#### Gap 2: **MCTS Selection Logic**
**Status:** 0% exists

**Need to build:**
```go
// internal/temporal/mcts.go

func SelectNodeByUCB(ctx context.Context, store *store.Store, sessionID string) (*HypothesisNode, error)
func Backpropagate(ctx context.Context, store *store.Store, nodeID string, reward float64) error
```

**Effort:** 1 day

---

#### Gap 3: **Atomic Skills Catalog**
**Status:** 10% exists

**Need to build:**
```yaml
# skills.yaml
- id: grep_config
  description: "Search config file for pattern"
  command: "grep '{{pattern}}' {{file_path}}"
  params:
    - name: pattern
      type: string
      required: true
```

```go
// internal/skills/catalog.go
type SkillCatalog struct
func (c *SkillCatalog) Execute(skillID string, params map[string]any) (*SkillResult, error)
```

**Effort:** 4-6 hours (10 initial skills)

---

#### Gap 4: **Cortex Memory Table**
**Status:** 0% exists

**Need to build:**
```sql
CREATE TABLE cortex_memories (
    id INTEGER PRIMARY KEY,
    problem_signature TEXT NOT NULL,
    solution_path TEXT NOT NULL,
    success_count INTEGER DEFAULT 0,
    myelination_score REAL DEFAULT 0.0,
    created_at DATETIME DEFAULT (datetime('now'))
);
```

```go
// internal/store/cortex.go
func (s *Store) StoreSolutionPath(problem ProblemState, solutionPath []string) error
func (s *Store) QueryCortex(problem ProblemState) ([]CortexMemory, error)
```

**Effort:** 3-4 hours

---

#### Gap 5: **Constrained LLM Interface**
**Status:** 30% exists

**Need to build:**
```go
// internal/temporal/hypothesis_generator.go

type HypothesisRequest struct {
    ProblemState      ProblemState
    VisitedHypotheses []string
    SkillCatalog      []Skill
    NegativeExamples  []FailedHypothesis
}

func (a *Activities) GenerateHypothesisActivity(ctx context.Context, req HypothesisRequest) (*HypothesisResponse, error)
```

**Effort:** 4-6 hours

---

#### Gap 6: **Graph-Traversal Workflow**
**Status:** 0% exists

**Need to build:**
```go
// internal/temporal/workflow_graph_brain.go

func GraphBrainWorkflow(ctx workflow.Context, req GraphBrainRequest) (*Solution, error)
```

**Effort:** 1-2 days

---

<a name="poc-roadmap"></a>
## 📋 POC Roadmap (2 Weeks)

### **Week 1: Core Graph Infrastructure**

#### Day 1-2: Schema & Storage
- [ ] Create hypothesis_nodes table
- [ ] Create hypothesis_edges table
- [ ] Create cortex_memories table
- [ ] Write migration scripts
- [ ] Add Store methods

**Deliverable:** `go test ./internal/store` passes

---

#### Day 3: MCTS Algorithm
- [ ] Implement UCB selection
- [ ] Implement backpropagation
- [ ] Unit tests

**Deliverable:** Node selection tests pass

---

#### Day 4-5: Skills Catalog
- [ ] Create `skills.yaml` with 10 skills
- [ ] Implement catalog loader
- [ ] Add ExecuteSkillActivity

**Deliverable:** Can execute all 10 skills

---

### **Week 2: Workflows & Integration**

#### Day 6-7: Hypothesis Generation
- [ ] GenerateHypothesisActivity
- [ ] Constrained prompt template
- [ ] JSON validation

**Deliverable:** LLM returns valid hypothesis JSON

---

#### Day 8-9: Graph Workflow
- [ ] GraphBrainWorkflow
- [ ] Wire up activities
- [ ] Node creation activities

**Deliverable:** Workflow runs 1 MCTS iteration

---

#### Day 10-11: Cortex Integration
- [ ] QueryCortexActivity
- [ ] StoreCortexMemoryActivity
- [ ] Myelination scoring

**Deliverable:** Pathway reuse works

---

#### Day 12: End-to-End Test
- [ ] Run on real problem (rate limit)
- [ ] Verify solution found
- [ ] Check cortex stores result
- [ ] Run again, verify faster

**Deliverable:** POC demo video

---

#### Day 13-14: Polish & Documentation
- [ ] Logging/observability
- [ ] Documentation
- [ ] Benchmark comparison
- [ ] Present findings

---

<a name="success-criteria"></a>
## ✅ Success Criteria for POC

### Functional Requirements
- ✅ Solves at least 1 real problem
- ✅ Graph persists across restarts
- ✅ Second execution uses myelinated pathway
- ✅ No infinite loops (depth limit works)
- ✅ LLM can't escape skill catalog

### Performance Targets
- ✅ First solve: <10 MCTS iterations
- ✅ Second solve: <3 iterations (cortex hit)
- ✅ Token savings: >50% vs unconstrained

### Quality Metrics
- ✅ No repeated hypotheses in same session
- ✅ Failed paths get pruned (weight <0.5)
- ✅ Myelination score increases after success

---

<a name="quick-start-commands"></a>
## 🚀 Quick Start Commands

```bash
# Week 1: Setup
cd ~/projects/cortex
git checkout -b feat/graph-brain-poc

# Create schema
sqlite3 chum.db < migrations/graph_brain_schema.sql

# Add skills
mkdir -p internal/skills
cat > internal/skills/catalog.yaml <<EOF
# (paste skills catalog)
EOF

# Run migration
go run cmd/chum/main.go migrate

# Test storage
go test ./internal/store -run TestGraphStorage

# Week 2: Integration
go run cmd/chum/main.go graph-brain --problem "rate_limit_error" --debug

# Verify cortex
sqlite3 chum.db "SELECT * FROM cortex_memories;"

# Run again (should use cortex)
go run cmd/chum/main.go graph-brain --problem "rate_limit_error" --debug
```

---

## 📊 Estimated Effort

| Component | Effort | Complexity |
|-----------|--------|-----------|
| Schema & Storage | 6 hours | Low |
| MCTS Algorithm | 8 hours | Medium |
| Skills Catalog | 6 hours | Low |
| Hypothesis Generation | 8 hours | Medium |
| Graph Workflow | 12 hours | High |
| Cortex Integration | 6 hours | Medium |
| Testing & Polish | 8 hours | Medium |
| **Total** | **~54 hours** | **~7 days** |

---

## 🔥 Critical Path Items

**Must Have (POC Blocker):**
1. Hypothesis DAG schema ← START HERE
2. MCTS selection function
3. Skills catalog (10 skills minimum)
4. GraphBrainWorkflow
5. At least 1 working end-to-end test

**Nice to Have (Post-POC):**
- Context rotation
- Advanced cortex similarity
- Multi-agent parallelism
- Graph visualization
- Metrics dashboard

---

## 💡 Bootstrapping Strategy

**Reuse PR #3's planning traces:**

```sql
-- Create VIEW to adapt existing planning traces
CREATE VIEW hypothesis_nodes AS
SELECT
    node_id as id,
    parent_node_id,
    session_id,
    full_text as hypothesis,
    tool_name as skill_name,
    0 as visits,
    reward as value,
    0.0 as ucb,
    cycle as depth,
    CASE WHEN event_type = 'plan_agreed' THEN 1 ELSE 0 END as terminal
FROM planning_trace_events
WHERE event_type IN ('tool_call', 'plan_agreed', 'gate_fail');
```

**You have ~50% of the schema already done via PR #3!**

---

## 🎯 First Problem to Solve (POC Demo)

```go
problem := ProblemState{
    Error: "WorkflowDispatch failed: rate limit exceeded",
    Context: map[string]string{
        "project": "chum",
        "provider": "codex-spark",
        "config": "~/projects/cortex/chum.toml",
    },
}
```

**Expected Graph Exploration:**

```
Root: "rate_limit_error"
├─ H1: "weekly_cap_exceeded" → grep_config → FAIL
├─ H2: "provider_offline" → test_provider_api → FAIL
└─ H3: "anthropic_key_rate_limited" → check_logs → SUCCESS ✅
```

**Second run:** Direct to H3 → solved in 1 step

---

## 🏁 Bottom Line

**You're 40% of the way there** thanks to PR #3's planning trace infrastructure.

**Critical gaps: ~30 hours = 1 week of focused work**

**The POC is absolutely achievable in 2 weeks.**

---

## 📚 Additional Resources

### Research Papers
- Browne et al. (2012): "Monte Carlo Tree Search"
- Yao et al. (2023): "Tree of Thoughts"
- Yao et al. (2022): "ReAct: Reasoning and Acting"

### Go Libraries
- `github.com/dominikbraun/graph` - Graph algorithms
- `modernc.org/sqlite` - Pure Go SQLite (already using)

### Temporal Patterns
- [Temporal Patterns: Dynamic Workflows](https://docs.temporal.io/workflows#dynamic-workflows)
- [Child Workflow Patterns](https://docs.temporal.io/workflows#child-workflows)

---

**Document End**
