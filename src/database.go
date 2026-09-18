package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Client struct {
	ID         int64
	Name       string
	Country    string
	ClientUUID string
	Email      string
	Token      string
	CreatedAt  string
}

func OpenDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	query := `CREATE TABLE IF NOT EXISTS clients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		country TEXT NOT NULL,
		client_uuid TEXT NOT NULL,
		email TEXT NOT NULL,
		token TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`

	if _, err := db.Exec(query); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func AddClient(db *sql.DB, name, country, clientUUID, email, token string) (int64, error) {
	query := `INSERT INTO clients (name, country, client_uuid, email, token, created_at) VALUES (?, ?, ?, ?, ?, datetime('now'))`
	res, err := db.Exec(query, name, country, clientUUID, email, token)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func GetClients(db *sql.DB) ([]Client, error) {
	query := `SELECT id, name, country, client_uuid, email, token, created_at FROM clients ORDER BY id`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []Client
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.Name, &c.Country, &c.ClientUUID, &c.Email, &c.Token, &c.CreatedAt); err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	return clients, rows.Err()
}

func GetClientByID(db *sql.DB, id int64) (*Client, error) {
	query := `SELECT id, name, country, client_uuid, email, token, created_at FROM clients WHERE id=?`
	row := db.QueryRow(query, id)

	var c Client
	if err := row.Scan(&c.ID, &c.Name, &c.Country, &c.ClientUUID, &c.Email, &c.Token, &c.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func DeleteClient(db *sql.DB, id int64) (bool, error) {
	query := `DELETE FROM clients WHERE id=?`
	res, err := db.Exec(query, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func CountClients(db *sql.DB) (int, error) {
	query := `SELECT COUNT(*) FROM clients`
	row := db.QueryRow(query)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
