package main

import (
	"database/sql"
	"fmt"
	"log"
)

// Update Migration struct
type Migration struct {
	Version int
	Up      func(*sql.Tx) error // Now takes transaction
}

// Update your migrations
var migrations = []Migration{
	{
		Version: 1,
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS transactions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				type TEXT NOT NULL CHECK(type IN ('income', 'expense')),
				description TEXT NOT NULL,
				amount INTEGER NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`)
			return err
		},
	},
}

func migrateDatabase(db *sql.DB, dbName string) error {
	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Create migration tracking table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version - handle NULL
	var version int
	err := db.QueryRow(`
		SELECT COALESCE(MAX(version), 0) 
		FROM schema_migrations
	`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Apply pending migrations
	for _, migration := range migrations {
		if migration.Version > version {
			log.Printf("Applying migration %d for %s", migration.Version, dbName)

			tx, err := db.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin transaction: %w", err)
			}

			if err := migration.Up(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d failed: %w", migration.Version, err)
			}

			if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration.Version); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
			}

			log.Printf("Successfully applied migration %d", migration.Version)
		}
	}

	return nil
}
