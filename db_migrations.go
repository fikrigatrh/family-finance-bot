package main

import (
	"database/sql"
	"fmt"
	"log"
)

type Migration struct {
	Version int
	Up      func(*sql.DB) error
}

var migrations = []Migration{
	{
		Version: 1,
		Up: func(db *sql.DB) error {
			_, err := db.Exec(`CREATE TABLE IF NOT EXISTS transactions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				type TEXT NOT NULL CHECK(type IN ('income', 'expense')),
				description TEXT NOT NULL,
				amount INTEGER NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`)
			return err
		},
	},
	// Add future migrations here:
	// {
	//     Version: 2,
	//     Up: func(db *sql.DB) error {
	//         _, err := db.Exec(`ALTER TABLE transactions ADD COLUMN category TEXT`)
	//         return err
	//     },
	// },
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

	// Get current version
	var currentVersion int
	err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&currentVersion)
	if err == sql.ErrNoRows {
		currentVersion = 0
	} else if err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Apply pending migrations
	for _, migration := range migrations {
		if migration.Version > currentVersion {
			log.Printf("Applying migration %d for %s", migration.Version, dbName)
			if err := migration.Up(db); err != nil {
				return fmt.Errorf("migration %d failed: %w", migration.Version, err)
			}

			if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration.Version); err != nil {
				return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
			}
			log.Printf("Successfully applied migration %d", migration.Version)
		}
	}

	return nil
}
