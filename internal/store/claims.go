package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ClaimLease tracks ownership locks so stale claims can be reconciled safely.
type ClaimLease struct {
	BeadID      string
	Project     string
	BeadsDir    string
	AgentID     string
	DispatchID  int64
	ClaimedAt   time.Time
	HeartbeatAt time.Time
}
// UpsertClaimLease records or refreshes a claim lease for a bead ownership lock.
func (s *Store) UpsertClaimLease(beadID, project, beadsDir, agentID string) error {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return fmt.Errorf("store: upsert claim lease: bead_id is required")
	}
	_, err := s.db.Exec(
		`INSERT INTO claim_leases (bead_id, project, beads_dir, agent_id, dispatch_id, claimed_at, heartbeat_at)
		 VALUES (?, ?, ?, ?, 0, datetime('now'), datetime('now'))
		 ON CONFLICT(bead_id) DO UPDATE SET
		   project=excluded.project,
		   beads_dir=excluded.beads_dir,
		   agent_id=excluded.agent_id,
		   heartbeat_at=datetime('now')`,
		beadID, strings.TrimSpace(project), strings.TrimSpace(beadsDir), strings.TrimSpace(agentID),
	)
	if err != nil {
		return fmt.Errorf("store: upsert claim lease: %w", err)
	}
	return nil
}

// AttachDispatchToClaimLease links a recorded dispatch to its claim lease and refreshes heartbeat.
func (s *Store) AttachDispatchToClaimLease(beadID string, dispatchID int64) error {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return fmt.Errorf("store: attach dispatch to claim lease: bead_id is required")
	}
	_, err := s.db.Exec(
		`UPDATE claim_leases SET dispatch_id = ?, heartbeat_at = datetime('now') WHERE bead_id = ?`,
		dispatchID, beadID,
	)
	if err != nil {
		return fmt.Errorf("store: attach dispatch to claim lease: %w", err)
	}
	return nil
}

// HeartbeatClaimLease refreshes heartbeat for an existing lease.
func (s *Store) HeartbeatClaimLease(beadID string) error {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE claim_leases SET heartbeat_at = datetime('now') WHERE bead_id = ?`, beadID)
	if err != nil {
		return fmt.Errorf("store: heartbeat claim lease: %w", err)
	}
	return nil
}

// DeleteClaimLease clears a lease record.
func (s *Store) DeleteClaimLease(beadID string) error {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM claim_leases WHERE bead_id = ?`, beadID)
	if err != nil {
		return fmt.Errorf("store: delete claim lease: %w", err)
	}
	return nil
}

// GetClaimLease loads a lease by bead ID.
func (s *Store) GetClaimLease(beadID string) (*ClaimLease, error) {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT bead_id, project, beads_dir, agent_id, dispatch_id, claimed_at, heartbeat_at FROM claim_leases WHERE bead_id = ?`,
		beadID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get claim lease: %w", err)
	}
	defer rows.Close()

	leases, err := scanClaimLeases(rows)
	if err != nil {
		return nil, err
	}
	if len(leases) == 0 {
		return nil, nil
	}
	return &leases[0], nil
}

// ListClaimLeases returns all active claim leases.
func (s *Store) ListClaimLeases() ([]ClaimLease, error) {
	rows, err := s.db.Query(
		`SELECT bead_id, project, beads_dir, agent_id, dispatch_id, claimed_at, heartbeat_at
		 FROM claim_leases ORDER BY heartbeat_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list claim leases: %w", err)
	}
	defer rows.Close()
	return scanClaimLeases(rows)
}

// GetExpiredClaimLeases returns leases whose heartbeat is older than now-ttl.
func (s *Store) GetExpiredClaimLeases(ttl time.Duration) ([]ClaimLease, error) {
	if ttl <= 0 {
		return nil, nil
	}
	cutoff := time.Now().Add(-ttl).UTC().Format(time.DateTime)
	rows, err := s.db.Query(
		`SELECT bead_id, project, beads_dir, agent_id, dispatch_id, claimed_at, heartbeat_at
		 FROM claim_leases WHERE heartbeat_at < ? ORDER BY heartbeat_at ASC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get expired claim leases: %w", err)
	}
	defer rows.Close()
	return scanClaimLeases(rows)
}
func scanClaimLeases(rows *sql.Rows) ([]ClaimLease, error) {
	var leases []ClaimLease
	for rows.Next() {
		var lease ClaimLease
		if err := rows.Scan(
			&lease.BeadID,
			&lease.Project,
			&lease.BeadsDir,
			&lease.AgentID,
			&lease.DispatchID,
			&lease.ClaimedAt,
			&lease.HeartbeatAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan claim lease: %w", err)
		}
		leases = append(leases, lease)
	}
	return leases, rows.Err()
}
