# Graph-Brain POC: Implementation Checklist

**Start Date:** _______________
**Target Completion:** 2 weeks from start

---

## 📋 Pre-Flight Checklist

- [ ] Read `GRAPH_BRAIN_POC_ROADMAP.md` completely
- [ ] Create feature branch: `git checkout -b feat/graph-brain-poc`
- [ ] Backup current database: `cp chum.db chum.db.backup-$(date +%Y%m%d)`
- [ ] Review PR #3 planning traces implementation

---

## Week 1: Core Infrastructure

### Day 1-2: Database Schema (6 hours)

#### Schema Files
- [ ] Create `migrations/20260225_graph_brain_schema.sql`
- [ ] Add hypothesis_nodes table
- [ ] Add hypothesis_edges table
- [ ] Add cortex_memories table
- [ ] Test migration: `go run cmd/chum/main.go migrate`

#### Store Methods
- [ ] `internal/store/graph_nodes.go`:
  - [ ] `CreateRootNode(sessionID, problemState) (Node, error)`
  - [ ] `CreateChildNode(parentID, hypothesis, skill) (Node, error)`
  - [ ] `GetNode(nodeID) (Node, error)`
  - [ ] `GetNodeChildren(nodeID) ([]Node, error)`
  - [ ] `UpdateNodeStats(nodeID, visits, value, ucb) error`
  - [ ] `MarkNodeTerminal(nodeID, terminal bool) error`

- [ ] `internal/store/graph_edges.go`:
  - [ ] `CreateEdge(fromID, toID, skill, weight) error`
  - [ ] `GetEdge(fromID, toID) (Edge, error)`
  - [ ] `UpdateEdgeWeight(fromID, toID, weight) error`
  - [ ] `IncrementEdgeSuccess(fromID, toID) error`
  - [ ] `IncrementEdgeFailure(fromID, toID) error`

- [ ] `internal/store/cortex.go`:
  - [ ] `StoreSolutionPath(problemSig, path, tokens) error`
  - [ ] `QueryCortex(problemSig) ([]Memory, error)`
  - [ ] `UpdateMyelinationScore(problemSig) error`

#### Tests
- [ ] `internal/store/graph_nodes_test.go`
- [ ] `internal/store/graph_edges_test.go`
- [ ] `internal/store/cortex_test.go`
- [ ] Run: `go test ./internal/store -v`

**Checkpoint:** All store tests pass ✅

---

### Day 3: MCTS Algorithm (8 hours)

#### Core Algorithm
- [ ] Create `internal/temporal/mcts.go`
- [ ] Implement `SelectNodeByUCB(sessionID) (Node, error)`:
  - [ ] Load graph from store
  - [ ] Calculate UCB for each child
  - [ ] Select highest UCB
  - [ ] Handle unvisited nodes (always explore first)
- [ ] Implement `Backpropagate(nodeID, reward) error`:
  - [ ] Walk up parent chain
  - [ ] Update visits, value, UCB
  - [ ] Update edge weights
  - [ ] Decay failed edges

#### Helper Functions
- [ ] `calculateUCB(node, parent) float64`
- [ ] `updateWeight(oldWeight, reward, learningRate) float64`
- [ ] `getAncestors(nodeID) ([]Node, error)`
- [ ] `getFailedSiblings(nodeID) ([]Node, error)`

#### Tests
- [ ] `internal/temporal/mcts_test.go`:
  - [ ] Test UCB selection prefers high-value nodes
  - [ ] Test unvisited nodes explored first
  - [ ] Test backpropagation updates ancestors
  - [ ] Test edge decay on failure
- [ ] Run: `go test ./internal/temporal -run TestMCTS -v`

**Checkpoint:** MCTS selection works correctly ✅

---

### Day 4-5: Skills Catalog (6 hours)

#### Catalog Definition
- [ ] Create `internal/skills/catalog.yaml`
- [ ] Define 10 initial skills:
  - [ ] `grep_config` - Search config files
  - [ ] `check_temporal_attrs` - List Temporal search attributes
  - [ ] `test_provider_api` - Test API endpoint
  - [ ] `check_logs` - Tail logs for pattern
  - [ ] `verify_binary_symbol` - Check binary for string
  - [ ] `query_database` - Run SQLite query
  - [ ] `list_processes` - Check running processes
  - [ ] `check_disk_space` - Check disk usage
  - [ ] `test_network` - Test network connectivity
  - [ ] `inspect_env` - Check environment variables

#### Catalog Engine
- [ ] Create `internal/skills/catalog.go`:
  - [ ] `type Skill struct`
  - [ ] `type SkillCatalog struct`
  - [ ] `LoadCatalog(path) (*SkillCatalog, error)`
  - [ ] `(c *SkillCatalog) GetSkill(id) (Skill, error)`
  - [ ] `(c *SkillCatalog) Execute(id, params) (*Result, error)`
  - [ ] `renderTemplate(cmd, params) string`

#### Activity Integration
- [ ] Add to `internal/temporal/activities.go`:
  - [ ] `ExecuteSkillActivity(ctx, skillID, params) (*SkillResult, error)`
  - [ ] Load catalog once at worker startup
  - [ ] Execute skill with timeout
  - [ ] Return structured result

#### Tests
- [ ] `internal/skills/catalog_test.go`:
  - [ ] Test catalog loading
  - [ ] Test template rendering
  - [ ] Test each skill execution
- [ ] Run: `go test ./internal/skills -v`

**Checkpoint:** All 10 skills execute successfully ✅

---

## Week 2: Workflow Integration

### Day 6-7: Hypothesis Generation (8 hours)

#### Request/Response Types
- [ ] Add to `internal/temporal/types.go`:
  - [ ] `type HypothesisRequest struct`
  - [ ] `type HypothesisResponse struct`
  - [ ] `type ProblemState struct`
  - [ ] `type FailedHypothesis struct`
  - [ ] `type CortexMemory struct`

#### Prompt Builder
- [ ] Create `internal/temporal/hypothesis_generator.go`:
  - [ ] `buildConstrainedPrompt(req) string`
  - [ ] Include problem state
  - [ ] Include negative examples (failed hypotheses)
  - [ ] Include cortex hints
  - [ ] Include skill catalog
  - [ ] Require JSON output format

#### Activity
- [ ] `GenerateHypothesisActivity(ctx, req) (*HypothesisResponse, error)`:
  - [ ] Build constrained prompt
  - [ ] Call LLM with structured output
  - [ ] Parse JSON response
  - [ ] Validate skill_id exists in catalog
  - [ ] Attempt JSON repair on parse failure
  - [ ] Return validated hypothesis

#### Tests
- [ ] `internal/temporal/hypothesis_generator_test.go`:
  - [ ] Test prompt includes all sections
  - [ ] Test JSON parsing
  - [ ] Test skill validation
  - [ ] Mock LLM for deterministic tests
- [ ] Run: `go test ./internal/temporal -run TestHypothesis -v`

**Checkpoint:** LLM returns valid hypothesis JSON ✅

---

### Day 8-9: Graph Workflow (12 hours)

#### Workflow Types
- [ ] Add to `internal/temporal/types.go`:
  - [ ] `type GraphBrainRequest struct`
  - [ ] `type Solution struct`

#### Node Management Activities
- [ ] Add to `internal/temporal/activities.go`:
  - [ ] `CreateRootNodeActivity(sessionID, problem) (*Node, error)`
  - [ ] `CreateChildNodeActivity(parentID, hypothesis) (*Node, error)`
  - [ ] `MarkNodeTerminalActivity(nodeID, terminal) error`
  - [ ] `GetBestSolutionActivity(sessionID) (*Solution, error)`

#### Selection Activities
- [ ] `SelectNodeByUCBActivity(ctx, sessionID) (*Node, error)`:
  - [ ] Call MCTS SelectNodeByUCB
  - [ ] Return selected node

#### Evaluation
- [ ] `evaluateObservation(observation, hypothesis) float64`:
  - [ ] Check for errors (-1.0)
  - [ ] Check for success indicators (+0.5)
  - [ ] Check for solution indicators (+1.0)
  - [ ] Default to neutral (0.0)

#### Main Workflow
- [ ] Create `internal/temporal/workflow_graph_brain.go`:
  - [ ] `GraphBrainWorkflow(ctx, req) (*Solution, error)`
  - [ ] Initialize session
  - [ ] Create root node
  - [ ] MCTS loop (max iterations):
    - [ ] SELECT node by UCB
    - [ ] Check if terminal
    - [ ] QUERY cortex for hints
    - [ ] If myelinated path exists (score > 0.8), use it
    - [ ] Else GENERATE hypothesis via LLM
    - [ ] CREATE child node
    - [ ] EXECUTE skill
    - [ ] EVALUATE observation
    - [ ] BACKPROPAGATE reward
    - [ ] If solution found (reward > 0.8):
      - [ ] Mark terminal
      - [ ] Store in cortex
      - [ ] Break
  - [ ] Return best solution

#### Worker Registration
- [ ] Update `internal/temporal/worker.go`:
  - [ ] Register GraphBrainWorkflow
  - [ ] Register all new activities

#### Tests
- [ ] `internal/temporal/workflow_graph_brain_test.go`:
  - [ ] Test workflow runs 1 iteration
  - [ ] Test terminal detection
  - [ ] Test max depth limit
  - [ ] Mock activities for unit tests
- [ ] Run: `go test ./internal/temporal -run TestGraphBrain -v`

**Checkpoint:** Workflow runs and completes ✅

---

### Day 10-11: Cortex Integration (6 hours)

#### Problem Signature
- [ ] Create `internal/store/problem_signature.go`:
  - [ ] `hashProblemSignature(problem) string`
  - [ ] Extract key features (error type, component, provider)
  - [ ] Create stable hash (SHA256)

#### Cortex Activities
- [ ] Add to `internal/temporal/activities.go`:
  - [ ] `QueryCortexActivity(ctx, problem) ([]CortexMemory, error)`:
    - [ ] Hash problem signature
    - [ ] Query cortex_memories table
    - [ ] Return top 5 by myelination score
  - [ ] `StoreCortexMemoryActivity(ctx, problem, path) error`:
    - [ ] Hash problem signature
    - [ ] Get solution path from root to terminal
    - [ ] Upsert cortex_memories
    - [ ] Update myelination score
    - [ ] Update success count

#### Integration
- [ ] Update `GraphBrainWorkflow`:
  - [ ] Call QueryCortexActivity before expansion
  - [ ] If myelination_score > 0.8, use pathway directly
  - [ ] Call StoreCortexMemoryActivity on success

#### Tests
- [ ] `internal/store/cortex_test.go`:
  - [ ] Test solution storage
  - [ ] Test query by signature
  - [ ] Test myelination scoring
  - [ ] Test score increases on repeat success
- [ ] Run: `go test ./internal/store -run TestCortex -v`

**Checkpoint:** Cortex stores and retrieves pathways ✅

---

### Day 12: End-to-End Test (6 hours)

#### Test Problem
- [ ] Create `test_problems/rate_limit_error.json`:
```json
{
  "error": "WorkflowDispatch failed: rate limit exceeded",
  "context": {
    "project": "chum",
    "provider": "codex-spark",
    "config": "~/projects/cortex/chum.toml"
  }
}
```

#### CLI Command
- [ ] Add to `cmd/chum/main.go`:
  - [ ] `graph-brain` subcommand
  - [ ] `--problem-file` flag
  - [ ] `--debug` flag (verbose logging)
  - [ ] `--max-iterations` flag (default 20)

#### Run Test
- [ ] `go run cmd/chum/main.go graph-brain --problem-file test_problems/rate_limit_error.json --debug`
- [ ] Verify logs show:
  - [ ] Root node created
  - [ ] MCTS iterations
  - [ ] Hypothesis generation
  - [ ] Skill execution
  - [ ] Backpropagation
  - [ ] Solution found
  - [ ] Cortex storage

#### Verify Database
- [ ] Check hypothesis_nodes: `sqlite3 chum.db "SELECT * FROM hypothesis_nodes;"`
- [ ] Check edges: `sqlite3 chum.db "SELECT * FROM hypothesis_edges;"`
- [ ] Check cortex: `sqlite3 chum.db "SELECT * FROM cortex_memories;"`

#### Second Run (Myelinated Path)
- [ ] Run again: `go run cmd/chum/main.go graph-brain --problem-file test_problems/rate_limit_error.json --debug`
- [ ] Verify:
  - [ ] Cortex hit in logs
  - [ ] Solution in <5 iterations
  - [ ] Same solution as first run

#### Metrics
- [ ] Compare first vs second run:
  - [ ] Iterations: _____ vs _____
  - [ ] Total tokens: _____ vs _____
  - [ ] Time: _____ vs _____

**Checkpoint:** End-to-end test passes, second run faster ✅

---

### Day 13-14: Polish & Documentation (8 hours)

#### Logging
- [ ] Add structured logging to all activities
- [ ] Include node IDs, depths, UCB scores
- [ ] Log cortex hits vs misses
- [ ] Log myelination scores

#### Observability
- [ ] Add Prometheus metrics (optional):
  - [ ] `graph_brain_iterations_total`
  - [ ] `graph_brain_cortex_hits_total`
  - [ ] `graph_brain_solution_depth`
  - [ ] `graph_brain_tokens_used`

#### Documentation
- [ ] Update `README.md` with Graph-Brain section
- [ ] Create `docs/architecture/GRAPH_BRAIN_USAGE.md`:
  - [ ] How to add new skills
  - [ ] How to interpret results
  - [ ] Troubleshooting guide
- [ ] Add inline code comments

#### Demo Video
- [ ] Record screen capture:
  - [ ] Show problem definition
  - [ ] Run first time (exploration)
  - [ ] Show graph visualization (SQLite query)
  - [ ] Run second time (myelinated)
  - [ ] Show performance comparison

#### Presentation
- [ ] Create slides:
  - [ ] Architecture overview
  - [ ] Performance gains
  - [ ] Roadmap for production
- [ ] Present to team/stakeholders

**Checkpoint:** POC complete and documented ✅

---

## 🎯 Success Validation

### Functional ✅
- [ ] Solves rate limit problem
- [ ] Graph persists in database
- [ ] Second run uses cortex (myelinated path)
- [ ] No infinite loops observed
- [ ] LLM constrained to skill catalog

### Performance ✅
- [ ] First solve: <10 iterations
- [ ] Second solve: <3 iterations
- [ ] Token savings: >50%

### Quality ✅
- [ ] No repeated hypotheses in session
- [ ] Failed edges decay to <0.5
- [ ] Myelination score increases on repeat

---

## 🚀 Post-POC: Next Steps

### Immediate (Week 3)
- [ ] Test on 3 more real problems
- [ ] Add 10 more skills to catalog
- [ ] Implement context rotation
- [ ] Add graph visualization tool

### Short-term (Month 2)
- [ ] Integrate with existing DispatcherWorkflow
- [ ] Add multi-agent parallelism
- [ ] Implement advanced cortex similarity
- [ ] Build metrics dashboard

### Long-term (Month 3+)
- [ ] Migrate to Neo4j for large graphs
- [ ] Add hierarchical abstraction
- [ ] Implement transfer learning
- [ ] Build adversarial testing

---

## 📞 Help & Resources

### Stuck?
- Review `GRAPH_BRAIN_POC_ROADMAP.md` for detailed explanations
- Check `internal/temporal/planning_workflow.go` (PR #3) for similar patterns
- Test individual components in isolation first

### Key Files to Reference
- `internal/temporal/workflow.go` - Existing workflow patterns
- `internal/store/planning_trace.go` - Graph storage patterns (PR #3)
- `internal/temporal/activities.go` - Activity examples

### Testing Tips
- Use `--debug` flag for verbose output
- Check SQLite directly: `sqlite3 chum.db`
- Use Temporal UI to inspect workflow state
- Add logging liberally during development

---

**POC Status:**

- [ ] Week 1 Complete
- [ ] Week 2 Complete
- [ ] Demo Recorded
- [ ] Documentation Complete
- [ ] Ready for Production Planning

**Completion Date:** _______________

---

**Notes:**
