# Graph-Brain: Quick Reference Card

One-page reference for key formulas, patterns, and code snippets during implementation.

---

## 🧮 Core Formulas

### UCB1 Selection
```
UCB(h) = V̄(h) + C * √(ln(N_parent) / N(h)) * edge_weight

where:
  V̄(h) = value / visits  (exploitation)
  C = √2                  (exploration constant)
  edge_weight ∈ [0, 1]    (synaptic strength)
```

### Weight Update (Backpropagation)
```python
# Reward signal
R = {
    +1.0:  solution_found
    +0.5:  progress_made
     0.0:  no_progress
    -0.5:  dead_end
    -1.0:  error
}

# EMA update
w_new = w_old + λ * (R - w_old)

# Decay on failure
if R < 0:
    w_new = w_old * 0.9
```

### Myelination Score
```python
myelination = (success_count / total_count) * log(total_count + 1)

# Threshold for automatic pathway selection
if myelination > 0.8:
    use_pathway_directly()  # skip LLM
```

---

## 🗄️ Schema Essentials

### Hypothesis Node
```sql
CREATE TABLE hypothesis_nodes (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    session_id TEXT NOT NULL,
    hypothesis TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    depth INTEGER NOT NULL,
    visits INTEGER DEFAULT 0,
    value REAL DEFAULT 0.0,
    ucb REAL DEFAULT 0.0,
    terminal BOOLEAN DEFAULT FALSE
);
```

### Skill Edge
```sql
CREATE TABLE hypothesis_edges (
    from_node TEXT,
    to_node TEXT,
    weight REAL DEFAULT 1.0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    PRIMARY KEY (from_node, to_node)
);
```

### Cortex Memory
```sql
CREATE TABLE cortex_memories (
    problem_signature TEXT NOT NULL,
    solution_path TEXT NOT NULL,        -- JSON array
    myelination_score REAL DEFAULT 0.0,
    success_count INTEGER DEFAULT 0
);
```

---

## 🔄 MCTS Pseudocode

```python
def mcts_iteration(root):
    # 1. SELECT
    node = select_node_ucb(root)

    # 2. EXPAND
    if not node.terminal and node.depth < MAX_DEPTH:
        # Query cortex first
        cortex_hits = query_cortex(node.state)
        if cortex_hits and cortex_hits[0].myelination > 0.8:
            hypothesis = cortex_hits[0]  # use myelinated path
        else:
            hypothesis = llm_generate_hypothesis(node)  # explore

        child = create_child_node(node, hypothesis)
    else:
        child = node

    # 3. SIMULATE
    observation = execute_skill(child.skill, child.params)

    # 4. EVALUATE
    reward = evaluate(observation, child.hypothesis)

    # 5. BACKPROPAGATE
    backpropagate(child, reward)

    # 6. TERMINAL CHECK
    if reward > 0.8:
        mark_terminal(child)
        store_cortex(child.root, child)
        return SOLVED

    return CONTINUE
```

---

## 🛠️ Code Patterns

### UCB Selection (Go)
```go
func SelectNodeByUCB(ctx context.Context, s *store.Store, sessionID string) (*Node, error) {
    root := s.GetRootNode(sessionID)
    return selectRecursive(root, s, 1.414)
}

func selectRecursive(node *Node, s *store.Store, C float64) *Node {
    if node.Terminal || len(node.Children) == 0 {
        return node
    }

    var bestChild *Node
    bestUCB := math.Inf(-1)

    for _, childID := range node.Children {
        child := s.GetNode(childID)
        if child.Visits == 0 {
            return child  // Always explore unvisited first
        }

        exploitation := child.Value / float64(child.Visits)
        exploration := C * math.Sqrt(math.Log(float64(node.Visits)) / float64(child.Visits))

        edge := s.GetEdge(node.ID, child.ID)
        ucb := (exploitation + exploration) * edge.Weight

        if ucb > bestUCB {
            bestUCB = ucb
            bestChild = child
        }
    }

    return selectRecursive(bestChild, s, C)
}
```

### Backpropagation (Go)
```go
func Backpropagate(ctx context.Context, s *store.Store, nodeID string, reward float64) error {
    node := s.GetNode(nodeID)
    learningRate := 0.1

    for node != nil {
        // Update node stats
        node.Visits++
        node.Value += reward
        node.UCB = calculateUCB(node)
        s.UpdateNode(node)

        // Update edge weight
        if node.ParentID != "" {
            edge := s.GetEdge(node.ParentID, node.ID)
            newWeight := edge.Weight + learningRate * (reward - edge.Weight)

            if reward < 0 {
                newWeight *= 0.9  // decay on failure
                edge.FailureCount++
            } else {
                edge.SuccessCount++
            }

            edge.Weight = math.Max(0.01, newWeight)  // floor at 0.01
            s.UpdateEdge(edge)
        }

        node = s.GetNode(node.ParentID)
    }
    return nil
}
```

### Hypothesis Generation (Prompt Template)
```go
const hypothesisPromptTemplate = `
Problem State:
{{.Problem}}

Already Tried (DO NOT REPEAT):
{{range .FailedHypotheses}}
- {{.Hypothesis}}: {{.Outcome}}
{{end}}

Suggested from Cortex (proven patterns):
{{range .CortexHints}}
- {{.Description}} (confidence: {{.Myelination}})
{{end}}

Available Skills:
{{range .Skills}}
- {{.ID}}: {{.Description}}
{{end}}

Output Format (JSON only):
{
    "hypothesis": "what you think is wrong",
    "reasoning": "why you think this",
    "skill_id": "which skill to run",
    "skill_params": {"param": "value"},
    "confidence": 0.0-1.0
}
`
```

### Skill Catalog (YAML)
```yaml
- id: grep_config
  description: "Search config file for pattern"
  command: "grep '{{pattern}}' {{file_path}}"
  params:
    - name: pattern
      type: string
      required: true
    - name: file_path
      type: string
      required: true
  cost_tokens: 100
  timeout: 10s
```

### Skill Execution (Go)
```go
func (c *SkillCatalog) Execute(skillID string, params map[string]any) (*SkillResult, error) {
    skill := c.skills[skillID]

    // Render template
    cmd := renderTemplate(skill.Command, params)

    // Execute with timeout
    ctx, cancel := context.WithTimeout(context.Background(), skill.Timeout)
    defer cancel()

    output, err := exec.CommandContext(ctx, "bash", "-c", cmd).CombinedOutput()

    return &SkillResult{
        SkillID: skillID,
        Output:  string(output),
        Error:   err,
        Tokens:  skill.CostTokens,
    }, nil
}
```

---

## 🎯 Decision Trees

### When to Use Cortex vs LLM
```
Query Cortex
    ├─ myelination > 0.8? → Use pathway directly (skip LLM)
    ├─ myelination 0.5-0.8? → Use as hint in LLM prompt
    └─ myelination < 0.5? → Pure LLM exploration
```

### Edge Weight Thresholds
```
weight > 0.7  → Strong pathway, prefer
weight 0.4-0.7 → Neutral, consider
weight < 0.4  → Weak pathway, deprioritize
weight < 0.1  → Nearly pruned, avoid
```

### Terminal Detection
```
reward > 0.8   → Solution found, mark terminal
reward 0.3-0.8 → Progress, continue
reward < 0.3   → Dead end, backtrack
```

---

## 🐛 Common Pitfalls

### Don't Do This ❌
```go
// Don't load entire graph into memory
graph := loadAllNodes()  // BAD - can be 100k+ nodes

// Don't allow unbounded skill params
cmd := fmt.Sprintf("bash -c %s", userInput)  // INJECTION RISK

// Don't forget edge decay
edge.Weight = newWeight  // Missing decay on failure

// Don't recompute UCB without updating visits
node.UCB = exploitation + exploration  // Stale without visit++
```

### Do This Instead ✅
```go
// Load on-demand
node := store.GetNode(nodeID)  // Only what's needed

// Template validation
if !catalog.HasSkill(skillID) {
    return ErrInvalidSkill
}

// Always decay failed edges
if reward < 0 {
    edge.Weight *= 0.9
}

// Update visits before UCB
node.Visits++
node.UCB = calculateUCB(node)
```

---

## 📊 Monitoring Queries

### Graph Statistics
```sql
-- Node depth distribution
SELECT depth, COUNT(*) as count
FROM hypothesis_nodes
GROUP BY depth
ORDER BY depth;

-- Edge success rate
SELECT
    from_node,
    to_node,
    success_count,
    failure_count,
    ROUND(success_count * 1.0 / (success_count + failure_count), 2) as success_rate
FROM hypothesis_edges
WHERE success_count + failure_count > 0
ORDER BY success_rate DESC;

-- Cortex top pathways
SELECT
    problem_signature,
    myelination_score,
    success_count,
    solution_path
FROM cortex_memories
ORDER BY myelination_score DESC
LIMIT 10;
```

### Session Analysis
```sql
-- Session summary
SELECT
    session_id,
    COUNT(*) as total_nodes,
    MAX(depth) as max_depth,
    SUM(CASE WHEN terminal = 1 THEN 1 ELSE 0 END) as solutions_found
FROM hypothesis_nodes
GROUP BY session_id;

-- Failed hypotheses per session
SELECT
    h.session_id,
    h.hypothesis,
    e.failure_count
FROM hypothesis_nodes h
JOIN hypothesis_edges e ON e.to_node = h.id
WHERE e.failure_count > 0
ORDER BY e.failure_count DESC;
```

---

## 🎨 Visualization (Graphviz)

```python
def export_graph_to_dot(session_id):
    nodes = db.query("SELECT * FROM hypothesis_nodes WHERE session_id = ?", session_id)
    edges = db.query("SELECT * FROM hypothesis_edges WHERE from_node IN (...)")

    dot = "digraph G {\n"

    for node in nodes:
        color = "green" if node.terminal else ("red" if node.visits == 0 else "blue")
        dot += f'  "{node.id}" [label="{node.hypothesis}\\nUCB={node.ucb:.2f}" color={color}];\n'

    for edge in edges:
        style = "bold" if edge.weight > 0.7 else "dashed"
        dot += f'  "{edge.from_node}" -> "{edge.to_node}" [label="{edge.weight:.2f}" style={style}];\n'

    dot += "}\n"
    return dot

# Render: dot -Tpng graph.dot -o graph.png
```

---

## 🚨 Emergency Debug Commands

```bash
# Check if workflow is stuck
sqlite3 chum.db "SELECT session_id, MAX(depth), COUNT(*) FROM hypothesis_nodes GROUP BY session_id;"

# Find loops (repeated hypotheses)
sqlite3 chum.db "SELECT hypothesis, COUNT(*) FROM hypothesis_nodes GROUP BY hypothesis HAVING COUNT(*) > 1;"

# Inspect edge weights (detect if pruning is working)
sqlite3 chum.db "SELECT * FROM hypothesis_edges WHERE weight < 0.3;"

# Check cortex population
sqlite3 chum.db "SELECT problem_signature, myelination_score FROM cortex_memories ORDER BY myelination_score DESC;"

# Reset session (for testing)
sqlite3 chum.db "DELETE FROM hypothesis_nodes WHERE session_id = 'test-session';"
```

---

## 📞 Quick Wins

### Start Simple
1. Test MCTS selection with 3 nodes manually inserted
2. Test single skill execution before building catalog
3. Test backpropagation with hardcoded rewards
4. Use mock LLM (return fixed JSON) for initial testing

### Incremental Integration
1. Build graph storage → test in isolation
2. Add MCTS → test selection logic
3. Add skills → test execution
4. Add LLM → test hypothesis generation
5. Wire up workflow → end-to-end test

### Validation Checkpoints
- After each component: unit tests pass
- After integration: workflow runs 1 iteration
- After full build: solves test problem
- After cortex: second run uses myelinated path

---

**Keep this reference handy during implementation!**
