package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Client struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Country    string `json:"country"`
	ClientUUID string `json:"client_uuid"`
	Email      string `json:"email"`
	Token      string `json:"token"`
	CreatedAt  string `json:"created_at"`
}

type Node struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
	APIToken  string `json:"api_token"`
	IsLocal   bool   `json:"is_local"`
	Status    string `json:"status"`
	LastSeen  string `json:"last_seen"`
	CPUInfo   string `json:"cpu_info"`
	RAMInfo   string `json:"ram_info"`
	CreatedAt string `json:"created_at"`
}

func OpenDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}

	queryClients := `CREATE TABLE IF NOT EXISTS clients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		country TEXT NOT NULL,
		client_uuid TEXT NOT NULL,
		email TEXT NOT NULL,
		token TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`

	if _, err := db.Exec(queryClients); err != nil {
		db.Close()
		return nil, err
	}

	queryNodes := `CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		domain TEXT NOT NULL,
		public_key TEXT NOT NULL,
		short_id TEXT NOT NULL,
		api_token TEXT NOT NULL,
		is_local INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		last_seen TEXT NOT NULL,
		cpu_info TEXT NOT NULL DEFAULT '',
		ram_info TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);`

	if _, err := db.Exec(queryNodes); err != nil {
		db.Close()
		return nil, err
	}

	queryTokens := `CREATE TABLE IF NOT EXISTS connect_tokens (
		token TEXT PRIMARY KEY,
		secret TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		used INTEGER NOT NULL DEFAULT 0
	);`

	if _, err := db.Exec(queryTokens); err != nil {
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

func AddNode(db *sql.DB, name, domain, publicKey, shortID, apiToken string, isLocal bool) (int64, error) {
	isLocalInt := 0
	if isLocal {
		isLocalInt = 1
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	query := `INSERT INTO nodes (name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, '', '', ?)`
	res, err := db.Exec(query, name, domain, publicKey, shortID, apiToken, isLocalInt, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func GetNodes(db *sql.DB) ([]Node, error) {
	query := `SELECT id, name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at FROM nodes ORDER BY id`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var isLocalInt int
		if err := rows.Scan(&n.ID, &n.Name, &n.Domain, &n.PublicKey, &n.ShortID, &n.APIToken, &isLocalInt, &n.Status, &n.LastSeen, &n.CPUInfo, &n.RAMInfo, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.IsLocal = (isLocalInt == 1)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func GetActiveNodes(db *sql.DB) ([]Node, error) {
	query := `SELECT id, name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at FROM nodes WHERE status='active' ORDER BY id`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var isLocalInt int
		if err := rows.Scan(&n.ID, &n.Name, &n.Domain, &n.PublicKey, &n.ShortID, &n.APIToken, &isLocalInt, &n.Status, &n.LastSeen, &n.CPUInfo, &n.RAMInfo, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.IsLocal = (isLocalInt == 1)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func GetNodeByID(db *sql.DB, id int64) (*Node, error) {
	query := `SELECT id, name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at FROM nodes WHERE id=?`
	row := db.QueryRow(query, id)

	var n Node
	var isLocalInt int
	if err := row.Scan(&n.ID, &n.Name, &n.Domain, &n.PublicKey, &n.ShortID, &n.APIToken, &isLocalInt, &n.Status, &n.LastSeen, &n.CPUInfo, &n.RAMInfo, &n.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	n.IsLocal = (isLocalInt == 1)
	return &n, nil
}

func GetNodeByToken(db *sql.DB, token string) (*Node, error) {
	query := `SELECT id, name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at FROM nodes WHERE api_token=?`
	row := db.QueryRow(query, token)

	var n Node
	var isLocalInt int
	if err := row.Scan(&n.ID, &n.Name, &n.Domain, &n.PublicKey, &n.ShortID, &n.APIToken, &isLocalInt, &n.Status, &n.LastSeen, &n.CPUInfo, &n.RAMInfo, &n.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	n.IsLocal = (isLocalInt == 1)
	return &n, nil
}

func UpdateNodeTelemetry(db *sql.DB, id int64, status, lastSeen, cpuInfo, ramInfo string) (bool, error) {
	query := `UPDATE nodes SET status=?, last_seen=?, cpu_info=?, ram_info=? WHERE id=?`
	res, err := db.Exec(query, status, lastSeen, cpuInfo, ramInfo, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func DeleteNode(db *sql.DB, id int64) (bool, error) {
	query := `DELETE FROM nodes WHERE id=?`
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

func CreateConnectToken(db *sql.DB, token, secret, expiresAt string) error {
	query := `INSERT INTO connect_tokens (token, secret, expires_at, used) VALUES (?, ?, ?, 0)`
	_, err := db.Exec(query, token, secret, expiresAt)
	return err
}

func ValidateAndUseConnectToken(db *sql.DB, token, secret string) (bool, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	query := `UPDATE connect_tokens SET used=1 WHERE token=? AND secret=? AND used=0 AND expires_at >= ?`
	res, err := db.Exec(query, token, secret, now)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
