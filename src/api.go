package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ConnectRequest struct {
	Secret    string `json:"secret"`
	Domain    string `json:"domain"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

type SyncClient struct {
	ClientUUID string `json:"client_uuid"`
	Email      string `json:"email"`
}

type ConnectResponse struct {
	Status   string       `json:"status"`
	NodeID   int64        `json:"node_id"`
	APIToken string       `json:"api_token"`
	Clients  []SyncClient `json:"clients"`
	Message  string       `json:"message,omitempty"`
}

type WSMessage struct {
	Type       string       `json:"type"`
	NodeID     int64        `json:"node_id,omitempty"`
	ClientUUID string       `json:"client_uuid,omitempty"`
	Email      string       `json:"email,omitempty"`
	Flow       string       `json:"flow,omitempty"`
	Clients    []SyncClient `json:"clients,omitempty"`
	CPU        string       `json:"cpu,omitempty"`
	RAM        string       `json:"ram,omitempty"`
	Uptime     string       `json:"uptime,omitempty"`
	XrayStatus string       `json:"xray_status,omitempty"`
}

type NodeSession struct {
	nodeID int64
	conn   *websocket.Conn
	sendMu sync.Mutex
}

func (s *NodeSession) SendJSON(v interface{}) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.conn.WriteJSON(v)
}

type NodeHub struct {
	sessions map[int64]*NodeSession
	mu       sync.RWMutex
}

func NewNodeHub() *NodeHub {
	return &NodeHub{
		sessions: make(map[int64]*NodeSession),
	}
}

func (h *NodeHub) Register(nodeID int64, conn *websocket.Conn) *NodeSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.sessions[nodeID]; ok {
		_ = existing.conn.Close()
	}
	s := &NodeSession{nodeID: nodeID, conn: conn}
	h.sessions[nodeID] = s
	return s
}

func (h *NodeHub) Unregister(nodeID int64, s *NodeSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := h.sessions[nodeID]; ok && current == s {
		delete(h.sessions, nodeID)
	}
}

func (h *NodeHub) Disconnect(nodeID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[nodeID]; ok {
		_ = s.conn.Close()
		delete(h.sessions, nodeID)
	}
}

func (h *NodeHub) Broadcast(msg WSMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		go func(sess *NodeSession) {
			_ = sess.SendJSON(msg)
		}(s)
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewMasterAPIMux(cfg *Config, db *sql.DB, hub *NodeHub) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/connect/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		token := strings.TrimPrefix(r.URL.Path, "/connect/")
		token = strings.Trim(token, "/")
		if token == "" {
			http.Error(w, "Missing connect token", http.StatusBadRequest)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1048576)
		var req ConnectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		req.Domain = strings.TrimSpace(req.Domain)
		req.Name = strings.TrimSpace(req.Name)
		req.PublicKey = strings.TrimSpace(req.PublicKey)
		req.ShortID = strings.TrimSpace(req.ShortID)

		if req.Domain == "" || strings.ContainsAny(req.Domain, " \t\r\n/\\") || req.PublicKey == "" || req.ShortID == "" {
			http.Error(w, "Invalid parameters", http.StatusBadRequest)
			return
		}
		req.Name = strings.ReplaceAll(strings.ReplaceAll(req.Name, "\r", ""), "\n", "")

		valid, err := ValidateAndUseConnectToken(db, token, req.Secret)
		if err != nil || !valid {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token/secret"})
			return
		}

		apiToken, err := generateToken(32)
		if err != nil {
			http.Error(w, "Failed to generate api token", http.StatusInternalServerError)
			return
		}

		nodeID, err := AddNode(db, req.Name, req.Domain, req.PublicKey, req.ShortID, apiToken, false)
		if err != nil {
			http.Error(w, "Failed to register node in database", http.StatusInternalServerError)
			return
		}

		_ = RegenerateAllSubscriptions(db, cfg.SubDir)

		dbClients, err := GetClients(db)
		var syncClients []SyncClient
		if err == nil {
			for _, c := range dbClients {
				syncClients = append(syncClients, SyncClient{
					ClientUUID: c.ClientUUID,
					Email:      c.Email,
				})
			}
		}

		resp := ConnectResponse{
			Status:   "ok",
			NodeID:   nodeID,
			APIToken: apiToken,
			Clients:  syncClients,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/internal/notify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)
		var msg WSMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "Invalid body", http.StatusBadRequest)
			return
		}
		if msg.Type == "node_delete" && msg.NodeID > 0 {
			hub.Disconnect(msg.NodeID)
		} else {
			hub.Broadcast(msg)
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/node/ws", func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" {
			http.Error(w, "Missing token", http.StatusUnauthorized)
			return
		}

		node, err := GetNodeByToken(db, token)
		if err != nil || node == nil || node.Status != "active" {
			http.Error(w, "Unauthorized node", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sess := hub.Register(node.ID, conn)
		defer func() {
			hub.Unregister(node.ID, sess)
			_ = conn.Close()
		}()

		dbClients, err := GetClients(db)
		if err == nil {
			var sc []SyncClient
			for _, c := range dbClients {
				sc = append(sc, SyncClient{
					ClientUUID: c.ClientUUID,
					Email:      c.Email,
				})
			}
			_ = sess.SendJSON(WSMessage{
				Type:    "sync",
				Clients: sc,
			})
		}

		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		for {
			var msg WSMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			switch msg.Type {
			case "telemetry":
				now := time.Now().Format("2006-01-02 15:04:05")
				if len(msg.CPU) > 255 {
					msg.CPU = msg.CPU[:255]
				}
				if len(msg.RAM) > 255 {
					msg.RAM = msg.RAM[:255]
				}
				if updated, _ := UpdateNodeTelemetry(db, node.ID, "active", now, msg.CPU, msg.RAM); !updated {
					_ = conn.Close()
					return
				}
			case "ping":
				_ = sess.SendJSON(WSMessage{Type: "pong"})
			}
		}
	})
	return mux
}

func StartMasterAPIServer(cfg *Config) error {
	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("ошибка открытия базы данных: %w", err)
	}
	defer db.Close()

	hub := NewNodeHub()
	handler := NewMasterAPIMux(cfg, db, hub)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.DaemonPort)
	log.Printf("[Wast API] Сервер запущен на %s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return server.ListenAndServe()
}

func runNodeWSClient(cfg *Config) error {
	if cfg.MasterURL == "" || cfg.NodeToken == "" {
		return errors.New("master_url или node_token не заданы")
	}

	baseURL := strings.TrimRight(cfg.MasterURL, "/")
	var wsURL string
	if strings.HasPrefix(baseURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(baseURL, "https://") + "/api/node/ws"
	} else if strings.HasPrefix(baseURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(baseURL, "http://") + "/api/node/ws"
	} else {
		wsURL = "wss://" + baseURL + "/api/node/ws"
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.NodeToken)
	header.Set("User-Agent", "Wast-Node/2.0")

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Printf("[Wast Node] Успешно подключено к основному серверу по WebSocket (%s)\n", wsURL)

	done := make(chan struct{})
	defer close(done)

	var sendMu sync.Mutex
	sendJSON := func(v interface{}) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(v)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				tMsg := WSMessage{
					Type:       "telemetry",
					CPU:        GetCPUInfo(),
					RAM:        GetRAMInfo(),
					Uptime:     GetUptime(),
					XrayStatus: GetServiceStatus("xray"),
				}
				if err := sendJSON(tMsg); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	for {
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		switch msg.Type {
		case "sync":
			currentXray, err := GetXrayClients(cfg.InboundTag)
			if err == nil {
				targetEmails := make(map[string]bool)
				for _, sc := range msg.Clients {
					targetEmails[sc.Email] = true
					if !currentXray[sc.Email] {
						_ = XrayAddUser(cfg, sc.ClientUUID, sc.Email, "xtls-rprx-vision")
					}
				}
				for email := range currentXray {
					if !targetEmails[email] {
						_ = XrayRemoveUser(cfg, email)
					}
				}
			}
		case "user_add":
			if msg.ClientUUID != "" && msg.Email != "" {
				flow := msg.Flow
				if flow == "" {
					flow = "xtls-rprx-vision"
				}
				_ = XrayAddUser(cfg, msg.ClientUUID, msg.Email, flow)
			}
		case "user_delete":
			if msg.Email != "" {
				_ = XrayRemoveUser(cfg, msg.Email)
			}
		case "ping":
			_ = sendJSON(WSMessage{Type: "pong"})
		}
	}
}

func RunNodeAgent(cfg *Config) {
	log.Printf("[Wast Node] Запуск службы постоянной синхронизации (WebSocket) с %s\n", cfg.MasterURL)
	backoff := 1 * time.Second
	for {
		err := runNodeWSClient(cfg)
		if err != nil {
			log.Printf("[Wast Node] Соединение прервано: %v. Повторное подключение через %v...\n", err, backoff)
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 15*time.Second {
			backoff = 15 * time.Second
		}
	}
}
