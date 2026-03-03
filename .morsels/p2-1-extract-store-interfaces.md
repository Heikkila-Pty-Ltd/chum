---
title: "Extract store interfaces for testability"
status: open
priority: 2
type: refactor
labels:
  - architecture
  - refactor
  - dogfood
  - testability
estimate_minutes: 90
acceptance_criteria: |
  - internal/store/iface.go exists with Reader and Writer interfaces
  - Reader interface covers all read-only store methods used by consumers
  - Writer interface covers all write store methods used by consumers
  - At least 3 consumer packages (temporal, dispatch, api) use the interface type instead of *Store
  - go build ./... passes with zero errors
  - go test ./... passes with zero failures
  - Existing concrete Store struct satisfies both interfaces (compile-time check)
design: |
  ## Current State
  internal/store has fan-in 6 — six packages depend on the concrete *store.Store type.
  This makes unit testing consumers difficult because you can't mock the store without
  importing the real implementation.

  ## Target State
  Define interfaces in internal/store/iface.go:

  ```go
  type Reader interface {
      GetDispatch(id int64) (*Dispatch, error)
      ListDispatches(filter DispatchFilter) ([]Dispatch, error)
      GetLessonsByBead(beadID string) ([]Lesson, error)
      GetRecentDispatchHealth(project string, window time.Duration) (failures, total int, err error)
      GetBlock(project, blockType string) (*SafetyBlock, error)
      // ... other read methods
  }

  type Writer interface {
      RecordDispatch(d *Dispatch) error
      RecordDoDResult(r *DoDResult) error
      SetBlock(project, blockType string, until time.Time, reason string) error
      // ... other write methods
  }

  type Store interface {
      Reader
      Writer
  }
  ```

  Then update consumer signatures to accept the interface.

  ## Approach
  1. Audit all methods on *store.Store
  2. Categorize into read/write
  3. Create iface.go with interfaces
  4. Add compile-time interface satisfaction check
  5. Update consumer function signatures (start with temporal package)
  6. Verify tests pass

  ## Risk
  Low — this is additive. The concrete Store already implements all methods,
  we're just formalizing the contract.
