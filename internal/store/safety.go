package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SafetyBlock represents a persisted throttling or coordination guard.
type SafetyBlock struct {
	Scope        string
	BlockType    string
	BlockedUntil time.Time
	Reason       string
	Metadata     map[string]interface{}
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GetBlock returns a persisted safety block by scope and block type.
func (s *Store) GetBlock(scope, blockType string) (*SafetyBlock, error) {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	if scope == "" || blockType == "" {
		return nil, nil
	}

	var (
		blockedUntil time.Time
		reason       string
		metadataJSON sql.NullString
		createdAt    time.Time
		updatedAt    time.Time
	)

	if err := s.db.QueryRow(
		`SELECT blocked_until, reason, metadata, created_at, updated_at FROM safety_blocks WHERE scope = ? AND block_type = ?`,
		scope, blockType,
	).Scan(&blockedUntil, &reason, &metadataJSON, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get block: %w", err)
	}

	metadata := make(map[string]interface{})
	if metadataJSON.Valid && strings.TrimSpace(metadataJSON.String) != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &metadata); err != nil {
			return nil, fmt.Errorf("store: decode block metadata: %w", err)
		}
	}

	return &SafetyBlock{
		Scope:        scope,
		BlockType:    blockType,
		BlockedUntil: blockedUntil,
		Reason:       reason,
		Metadata:     metadata,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}

// SetBlock creates or updates a persisted safety block.
func (s *Store) SetBlock(scope, blockType string, blockedUntil time.Time, reason string) error {
	return s.SetBlockWithMetadata(scope, blockType, blockedUntil, reason, nil)
}

// SetBlockWithMetadata creates or updates a persisted safety block with metadata.
func (s *Store) SetBlockWithMetadata(scope, blockType string, blockedUntil time.Time, reason string, metadata map[string]interface{}) error {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	reason = strings.TrimSpace(reason)
	if scope == "" || blockType == "" {
		return fmt.Errorf("store: set block: scope and block_type are required")
	}

	if blockedUntil.IsZero() {
		blockedUntil = time.Now()
	}

	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("store: encode block metadata: %w", err)
	}

	_, err = s.db.Exec(
		`INSERT INTO safety_blocks (scope, block_type, blocked_until, reason, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(scope, block_type) DO UPDATE SET
		   blocked_until = excluded.blocked_until,
		   reason = excluded.reason,
		   metadata = excluded.metadata,
		   created_at = excluded.created_at,
		   updated_at = datetime('now')`,
		scope,
		blockType,
		blockedUntil.UTC().Format(time.DateTime),
		reason,
		string(metadataJSON),
	)
	if err != nil {
		return fmt.Errorf("store: set block: %w", err)
	}

	return nil
}

// RemoveBlock deletes a persisted safety block.
func (s *Store) RemoveBlock(scope, blockType string) error {
	scope = strings.TrimSpace(scope)
	blockType = strings.TrimSpace(blockType)
	if scope == "" || blockType == "" {
		return nil
	}

	if _, err := s.db.Exec(`DELETE FROM safety_blocks WHERE scope = ? AND block_type = ?`, scope, blockType); err != nil {
		return fmt.Errorf("store: remove block: %w", err)
	}
	return nil
}

// GetActiveBlocks returns all safety blocks whose blocked_until is in the future.
func (s *Store) GetActiveBlocks() ([]SafetyBlock, error) {
	rows, err := s.db.Query(
		`SELECT scope, block_type, blocked_until, reason, metadata, created_at, updated_at
		 FROM safety_blocks
		 WHERE blocked_until > datetime('now')
		 ORDER BY block_type, scope`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get active blocks: %w", err)
	}
	defer rows.Close()

	var blocks []SafetyBlock
	for rows.Next() {
		var (
			b            SafetyBlock
			metadataJSON sql.NullString
		)
		if err := rows.Scan(&b.Scope, &b.BlockType, &b.BlockedUntil, &b.Reason, &metadataJSON, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan active block: %w", err)
		}
		b.Metadata = make(map[string]interface{})
		if metadataJSON.Valid && strings.TrimSpace(metadataJSON.String) != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &b.Metadata); err != nil {
				return nil, fmt.Errorf("store: decode active block metadata: %w", err)
			}
		}
		blocks = append(blocks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate active blocks: %w", err)
	}
	return blocks, nil
}

// GetBlockCountsByType returns counts of active safety blocks grouped by block_type.
func (s *Store) GetBlockCountsByType() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT block_type, COUNT(*) FROM safety_blocks
		 WHERE blocked_until > datetime('now')
		 GROUP BY block_type
		 ORDER BY block_type`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get block counts by type: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var blockType string
		var count int
		if err := rows.Scan(&blockType, &count); err != nil {
			return nil, fmt.Errorf("store: scan block count: %w", err)
		}
		counts[blockType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate block counts: %w", err)
	}
	return counts, nil
}

// IsBeadValidating returns whether a bead is currently marked validating.
func (s *Store) IsBeadValidating(beadID string) (bool, error) {
	block, err := s.GetBlock(strings.TrimSpace(beadID), "bead_validating")
	if err != nil {
		return false, fmt.Errorf("store: check bead validating: %w", err)
	}
	if block == nil {
		return false, nil
	}
	return time.Now().Before(block.BlockedUntil), nil
}

// SetBeadValidating sets a validating block for the given bead until the provided time.
func (s *Store) SetBeadValidating(beadID string, until time.Time) error {
	return s.SetBlock(strings.TrimSpace(beadID), "bead_validating", until, "bead validating")
}

// ClearBeadValidating removes the validating block for the given bead.
func (s *Store) ClearBeadValidating(beadID string) error {
	return s.RemoveBlock(strings.TrimSpace(beadID), "bead_validating")
}
