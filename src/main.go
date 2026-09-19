package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func generateRandomString(n int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[num.Int64()]
	}
	return string(b), nil
}

func generateUUID() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", err
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:]), nil
}

func generateToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func requireRoot() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "Запусти команду от root")
		os.Exit(1)
	}
}

func notifyDaemon(msg WSMessage, daemonPort int) {
	if daemonPort <= 0 {
		daemonPort = 8080
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/internal/notify", daemonPort)
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err == nil {
		resp.Body.Close()
	}
}

func printHelp() {
	fmt.Println("Использование:")
	fmt.Println("  wast <команда> [аргументы]")
	fmt.Println()
	fmt.Println("Команды:")
	fmt.Println("  add user [имя]        Добавить пользователя")
	fmt.Println("  delete user [id]      Удалить пользователя")
	fmt.Println("  list user(s)          Список пользователей с VLESS ссылками всех локаций")
	fmt.Println("  connect               Получить данные для подключения новой ноды")
	fmt.Println("  nodes                 Список всех подключенных нод и их статус")
	fmt.Println("  delete node [id]      Удалить ноду")
	fmt.Println("  status                Состояние сервера и служб")
	fmt.Println("  uninstall             Полное удаление Wast")
	fmt.Println("  help                  Показать справку")
}

func handleAddUser(cfg *Config, args []string) {
	requireRoot()

	if cfg.Role == "node" {
		fmt.Fprintln(os.Stderr, "Эта машина работает в режиме ноды. Добавление пользователей выполняется на основном сервере.")
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)
	var name string

	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		name = strings.TrimSpace(args[0])
	} else {
		fmt.Print("Имя (можно оставить пустым): ")
		input, _ := reader.ReadString('\n')
		name = strings.TrimSpace(input)
	}

	if name == "" {
		rnd, err := generateRandomString(8)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Ошибка генерации имени: %v\n", err)
			os.Exit(1)
		}
		name = rnd
	}

	clientUUID, err := generateUUID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка генерации UUID: %v\n", err)
		os.Exit(1)
	}

	token, err := generateToken(8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка генерации токена: %v\n", err)
		os.Exit(1)
	}

	shortUUID := clientUUID
	if len(shortUUID) > 8 {
		shortUUID = shortUUID[:8]
	}
	email := fmt.Sprintf("%s-%s", name, shortUUID)

	if err := XrayAddUser(cfg, clientUUID, email, "xtls-rprx-vision"); err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось добавить пользователя в Xray: %v\n", err)
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		_ = XrayRemoveUser(cfg, email)
		fmt.Fprintf(os.Stderr, "Ошибка подключения к базе данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	clientID, err := AddClient(db, name, cfg.Country, clientUUID, email, token)
	if err != nil {
		_ = XrayRemoveUser(cfg, email)
		fmt.Fprintf(os.Stderr, "Ошибка сохранения в базу данных: %v\n", err)
		os.Exit(1)
	}

	nodes, err := GetActiveNodes(db)
	var subContent string
	if err == nil && len(nodes) > 0 {
		subContent = BuildUserSubContent(clientUUID, nodes)
	} else {
		subContent = BuildVlessLink(cfg.Domain, cfg.PublicKey, cfg.ShortID, clientUUID, cfg.Country)
	}

	if err := WriteSubContent(cfg.SubDir, token, subContent); err != nil {
		_ = XrayRemoveUser(cfg, email)
		_, _ = DeleteClient(db, clientID)
		fmt.Fprintf(os.Stderr, "Не удалось сохранить файл подписки: %v\n", err)
		os.Exit(1)
	}

	notifyDaemon(WSMessage{
		Type:       "user_add",
		ClientUUID: clientUUID,
		Email:      email,
		Flow:       "xtls-rprx-vision",
	}, cfg.DaemonPort)

	fmt.Println("Пользователь успешно добавлен!")
	fmt.Printf("Подписка: %s/%s\n", strings.TrimRight(cfg.SubBaseURL, "/"), token)
	fmt.Println("VLESS ссылки:")
	if len(nodes) > 0 {
		for _, n := range nodes {
			link := BuildVlessLink(n.Domain, n.PublicKey, n.ShortID, clientUUID, n.Name)
			fmt.Printf("  %s: %s\n", n.Name, link)
		}
	} else {
		link := BuildVlessLink(cfg.Domain, cfg.PublicKey, cfg.ShortID, clientUUID, cfg.Country)
		fmt.Printf("  %s: %s\n", cfg.Country, link)
	}
}

func handleDeleteUser(cfg *Config, args []string) {
	requireRoot()

	if cfg.Role == "node" {
		fmt.Fprintln(os.Stderr, "Эта машина работает в режиме ноды. Удаление пользователей выполняется на основном сервере.")
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка подключения к базе данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	var idStr string
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		idStr = strings.TrimSpace(args[0])
	} else {
		clients, err := GetClients(db)
		if err == nil && len(clients) > 0 {
			for _, c := range clients {
				fmt.Printf("[%d] %s\n", c.ID, c.Name)
			}
			fmt.Println()
		}
		fmt.Print("ID клиента для удаления: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		idStr = strings.TrimSpace(input)
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintln(os.Stderr, "Некорректный ID")
		os.Exit(1)
	}

	client, err := GetClientByID(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка получения данных: %v\n", err)
		os.Exit(1)
	}
	if client == nil {
		fmt.Fprintln(os.Stderr, "Клиент не найден")
		os.Exit(1)
	}

	_ = XrayRemoveUser(cfg, client.Email)
	_ = RemoveSubFile(cfg.SubDir, client.Token)

	if _, err := DeleteClient(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка удаления из базы данных: %v\n", err)
		os.Exit(1)
	}

	notifyDaemon(WSMessage{
		Type:  "user_delete",
		Email: client.Email,
	}, cfg.DaemonPort)

	fmt.Printf("Клиент %s удален\n", client.Name)
}

func handleListUsers(cfg *Config) {
	if cfg.Role == "node" {
		fmt.Println("Эта машина работает в режиме ноды. Пользователи синхронизируются автоматически с основного сервера.")
		return
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка подключения к базе данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	clients, err := GetClients(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка чтения базы данных: %v\n", err)
		os.Exit(1)
	}

	if len(clients) == 0 {
		fmt.Println("Клиентов пока нет")
		return
	}

	nodes, err := GetActiveNodes(db)
	if err != nil {
		nodes = nil
	}

	for _, c := range clients {
		fmt.Printf("[%d] %s\n", c.ID, c.Name)
		fmt.Printf("  Подписка:  %s/%s\n", strings.TrimRight(cfg.SubBaseURL, "/"), c.Token)
		fmt.Println("  VLESS:")
		if len(nodes) > 0 {
			for _, n := range nodes {
				link := BuildVlessLink(n.Domain, n.PublicKey, n.ShortID, c.ClientUUID, n.Name)
				fmt.Printf("    %s: %s\n", n.Name, link)
			}
		} else {
			link := BuildVlessLink(cfg.Domain, cfg.PublicKey, cfg.ShortID, c.ClientUUID, cfg.Country)
			fmt.Printf("    %s: %s\n", cfg.Country, link)
		}
		fmt.Printf("  Создан:    %s\n\n", c.CreatedAt)
	}
}

func handleConnect(cfg *Config) {
	requireRoot()

	if cfg.Role == "node" {
		fmt.Fprintln(os.Stderr, "Эта машина работает в режиме ноды. Подключение новых нод выполняется на основном сервере.")
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка базы данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	rnd, err := generateRandomString(12)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка генерации токена: %v\n", err)
		os.Exit(1)
	}

	secretBytes, err := generateToken(16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка генерации секрета: %v\n", err)
		os.Exit(1)
	}
	secret := "wsec_" + secretBytes
	expiresAt := time.Now().Add(30 * time.Minute).Format("2006-01-02 15:04:05")

	if err := CreateConnectToken(db, rnd, secret, expiresAt); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка создания токена подключения: %v\n", err)
		os.Exit(1)
	}

	baseURL := "https://" + cfg.MainDomain
	if cfg.MainDomain == "" {
		baseURL = "https://" + cfg.Domain
	}

	fmt.Println("Данные для подключения новой ноды (действительны 30 минут):")
	fmt.Println()
	fmt.Printf("  URL подключения: %s/connect/%s\n", baseURL, rnd)
	fmt.Printf("  Секретный ключ:  %s\n", secret)
	fmt.Println()
	fmt.Println("Инструкция:")
	fmt.Println("  1. На сервере новой ноды запустите установку: bash install.sh")
	fmt.Println("  2. Выберите тип: '2) Нода'")
	fmt.Println("  3. Введите указанные выше URL подключения и секретный ключ.")
}

func handleNodes(cfg *Config) {
	if cfg.Role == "node" {
		fmt.Fprintln(os.Stderr, "Эта машина работает в режиме ноды. Список нод доступен на основном сервере.")
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка базы данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	nodes, err := GetNodes(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка чтения списка нод: %v\n", err)
		os.Exit(1)
	}

	if len(nodes) == 0 {
		fmt.Println("Ноды пока не подключены")
		return
	}

	fmt.Println("Список подключенных локаций и нод:")
	fmt.Println()
	fmt.Printf("%-4s %-25s %-22s %-10s %-25s %s\n", "ID", "Локация", "Домен", "Статус", "CPU / RAM", "Был в сети")
	fmt.Println(strings.Repeat("-", 100))

	for _, n := range nodes {
		roleTag := ""
		if n.IsLocal {
			roleTag = " (Master)"
		}
		name := n.Name + roleTag

		status := n.Status
		if !n.IsLocal && n.LastSeen != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", n.LastSeen); err == nil {
				if time.Since(t) > 90*time.Second {
					status = "offline"
				}
			}
		}

		perf := "-"
		if n.CPUInfo != "" || n.RAMInfo != "" {
			perf = fmt.Sprintf("%s / %s", n.CPUInfo, n.RAMInfo)
			if len(perf) > 24 {
				perf = perf[:24]
			}
		}

		fmt.Printf("%-4d %-25s %-22s %-10s %-25s %s\n", n.ID, name, n.Domain, status, perf, n.LastSeen)
	}
}

func handleDeleteNode(cfg *Config, args []string) {
	requireRoot()

	if cfg.Role == "node" {
		fmt.Fprintln(os.Stderr, "Эта машина работает в режиме ноды. Удаление нод выполняется на основном сервере.")
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка базы данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	var idStr string
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		idStr = strings.TrimSpace(args[0])
	} else {
		nodes, err := GetNodes(db)
		if err == nil && len(nodes) > 0 {
			for _, n := range nodes {
				tag := ""
				if n.IsLocal {
					tag = " (Master)"
				}
				fmt.Printf("[%d] %s (%s)%s\n", n.ID, n.Name, n.Domain, tag)
			}
			fmt.Println()
		}
		fmt.Print("ID ноды для удаления: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		idStr = strings.TrimSpace(input)
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintln(os.Stderr, "Некорректный ID")
		os.Exit(1)
	}

	node, err := GetNodeByID(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка получения данных ноды: %v\n", err)
		os.Exit(1)
	}
	if node == nil {
		fmt.Fprintln(os.Stderr, "Нода не найдена")
		os.Exit(1)
	}

	if node.IsLocal {
		fmt.Fprintln(os.Stderr, "Нельзя удалить локальную ноду основного сервера")
		os.Exit(1)
	}

	if _, err := DeleteNode(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка удаления ноды: %v\n", err)
		os.Exit(1)
	}

	_ = RegenerateAllSubscriptions(db, cfg.SubDir)
	notifyDaemon(WSMessage{Type: "node_delete", NodeID: id}, cfg.DaemonPort)

	fmt.Printf("Нода '%s' (ID %d) удалена. Все файлы подписок обновлены.\n", node.Name, node.ID)
}

func handleSync(cfg *Config) {
	if cfg.Role == "node" {
		fmt.Println("Синхронизация ноды выполняется автоматически в реальном времени через службу wast-node.")
		return
	}
	requireRoot()
	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка базы данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := RegenerateAllSubscriptions(db, cfg.SubDir); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка обновления подписок: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Все файлы подписок успешно перегенерированы.")
}

func handleDaemon(cfg *Config) {
	requireRoot()

	if cfg.Role == "node" {
		RunNodeAgent(cfg)
	} else {
		if err := StartMasterAPIServer(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Ошибка работы API сервера: %v\n", err)
			os.Exit(1)
		}
	}
}

func handleStatus(cfg *Config) {
	stubMsg, subMsg := CheckWastStatus(cfg.Domain, cfg.SubDir)
	certExpiry := GetCertExpiry(cfg.Domain)

	fmt.Println("Состояние сервера")
	fmt.Println()
	fmt.Printf("Роль:            %s\n", strings.ToUpper(cfg.Role))
	if cfg.Role == "node" {
		fmt.Printf("Основной сервер: %s\n", cfg.MasterURL)
	} else if cfg.MainDomain != "" {
		fmt.Printf("Основной домен:  %s\n", cfg.MainDomain)
	}
	fmt.Printf("Домен локации:   %s\n", cfg.Domain)
	fmt.Printf("Локация:         %s\n", cfg.Country)
	fmt.Printf("SSL сертификат:  %s\n", certExpiry)
	fmt.Println()
	fmt.Println("Службы:")
	fmt.Printf("  Nginx:         %s\n", GetServiceStatus("nginx"))
	fmt.Printf("  Xray:          %s\n", GetServiceStatus("xray"))
	if cfg.Role == "node" {
		fmt.Printf("  Wast Node:     %s\n", GetServiceStatus("wast-node"))
	} else {
		fmt.Printf("  Wast API:      %s\n", GetServiceStatus("wast-api"))
	}
	fmt.Printf("  BBR:           %s\n", GetBBRStatus())
	fmt.Printf("  Заглушка:      %s\n", stubMsg)
	if cfg.Role == "master" {
		fmt.Printf("  Подписки:      %s\n", subMsg)
	}
	fmt.Println()
	fmt.Println("Системные ресурсы:")
	fmt.Printf("  Uptime:        %s\n", GetUptime())
	fmt.Printf("  CPU Load:      %s\n", GetCPUInfo())
	fmt.Printf("  Память (RAM):  %s\n", GetRAMInfo())
	fmt.Printf("  Диск (ROM):    %s\n", GetDiskInfo())

	if cfg.Role == "master" {
		db, err := OpenDB(cfg.DBPath)
		if err == nil {
			nodes, err := GetActiveNodes(db)
			if err == nil && len(nodes) > 1 {
				fmt.Println()
				fmt.Println("Подключенные ноды:")
				for _, n := range nodes {
					if n.IsLocal {
						continue
					}
					status := n.Status
					if n.LastSeen != "" {
						if t, err := time.Parse("2006-01-02 15:04:05", n.LastSeen); err == nil {
							if time.Since(t) > 90*time.Second {
								status = "offline"
							}
						}
					}
					fmt.Printf("  [%d] %s (%s) - %s (был в сети: %s)\n", n.ID, n.Name, n.Domain, status, n.LastSeen)
				}
			}
			db.Close()
		}
	}
}

func handleUninstall(cfg *Config) {
	requireRoot()

	fmt.Println("Удаление Wast")
	fmt.Println()
	fmt.Print("Удалить xray, nginx конфиг, сертификат и wast полностью? Введи 'yes': ")
	reader := bufio.NewReader(os.Stdin)
	confirm, _ := reader.ReadString('\n')
	if strings.TrimSpace(confirm) != "yes" {
		fmt.Println("Отменено")
		return
	}

	_ = exec.Command("systemctl", "stop", "wast-api").Run()
	_ = exec.Command("systemctl", "disable", "wast-api").Run()
	_ = os.Remove("/etc/systemd/system/wast-api.service")

	_ = exec.Command("systemctl", "stop", "wast-node").Run()
	_ = exec.Command("systemctl", "disable", "wast-node").Run()
	_ = os.Remove("/etc/systemd/system/wast-node.service")

	_ = exec.Command("systemctl", "stop", "xray").Run()
	_ = exec.Command("systemctl", "disable", "xray").Run()
	_ = exec.Command("systemctl", "stop", "nginx").Run()
	_ = os.Remove("/etc/systemd/system/xray.service")
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = os.Remove("/usr/local/bin/xray")
	_ = os.RemoveAll("/usr/local/etc/xray")
	_ = os.RemoveAll("/var/log/xray")
	_ = os.Remove("/etc/nginx/sites-enabled/wast.conf")
	_ = os.Remove("/etc/nginx/sites-available/wast.conf")
	_ = os.RemoveAll("/var/www/wast")

	if cfg.Domain != "" {
		_ = exec.Command("certbot", "delete", "--cert-name", cfg.Domain, "--non-interactive").Run()
	}
	if cfg.MainDomain != "" && cfg.MainDomain != cfg.Domain {
		_ = exec.Command("certbot", "delete", "--cert-name", cfg.MainDomain, "--non-interactive").Run()
	}

	_ = exec.Command("systemctl", "start", "nginx").Run()

	crontabOut, err := exec.Command("crontab", "-l").Output()
	if err == nil {
		lines := strings.Split(string(crontabOut), "\n")
		var newLines []string
		for _, l := range lines {
			if !strings.Contains(l, "/opt/wast/renew.sh") && strings.TrimSpace(l) != "" {
				newLines = append(newLines, l)
			}
		}
		newCron := strings.Join(newLines, "\n")
		if len(newLines) > 0 {
			newCron += "\n"
		}
		cmd := exec.Command("crontab", "-")
		cmd.Stdin = strings.NewReader(newCron)
		_ = cmd.Run()
	}

	_ = exec.Command("ufw", "delete", "allow", "80/tcp").Run()
	_ = exec.Command("ufw", "delete", "allow", "443/tcp").Run()
	_ = os.Remove("/usr/local/bin/wast")
	_ = os.RemoveAll("/opt/wast")

	fmt.Println("Wast успешно удален.")
}

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка загрузки конфигурации: %v\n", err)
		os.Exit(1)
	}

	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return
	}

	cmd := strings.ToLower(args[0])
	switch cmd {
	case "add":
		if len(args) > 1 && (strings.ToLower(args[1]) == "user" || strings.ToLower(args[1]) == "client") {
			handleAddUser(cfg, args[2:])
		} else {
			handleAddUser(cfg, args[1:])
		}
	case "delete", "del", "rm":
		if len(args) > 1 && (strings.ToLower(args[1]) == "user" || strings.ToLower(args[1]) == "client") {
			handleDeleteUser(cfg, args[2:])
		} else if len(args) > 1 && strings.ToLower(args[1]) == "node" {
			handleDeleteNode(cfg, args[2:])
		} else {
			handleDeleteUser(cfg, args[1:])
		}
	case "list":
		if len(args) > 1 && (strings.ToLower(args[1]) == "node" || strings.ToLower(args[1]) == "nodes") {
			handleNodes(cfg)
		} else {
			handleListUsers(cfg)
		}
	case "nodes":
		handleNodes(cfg)
	case "connect":
		handleConnect(cfg)
	case "sync":
		handleSync(cfg)
	case "daemon":
		handleDaemon(cfg)
	case "status":
		handleStatus(cfg)
	case "uninstall":
		handleUninstall(cfg)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Неизвестная команда: %s\n\n", args[0])
		printHelp()
		os.Exit(1)
	}
}
