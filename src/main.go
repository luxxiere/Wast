package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

func printHelp() {
	fmt.Println("Использование:")
	fmt.Println("  wast <команда> [аргументы]")
	fmt.Println()
	fmt.Println("Команды:")
	fmt.Println("  add user [имя]      Добавить пользователя")
	fmt.Println("  delete user [id]    Удалить пользователя")
	fmt.Println("  list user           Список всех пользователей")
	fmt.Println("  status              Состояние сервера и служб")
	fmt.Println("  uninstall           Полное удаление Wast")
	fmt.Println("  help                Показать справку")
}

func handleAddUser(cfg *Config, args []string) {
	requireRoot()

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

	link := BuildVlessLink(cfg.Domain, cfg.PublicKey, cfg.ShortID, clientUUID, cfg.Country)

	if err := WriteSubFile(cfg.SubDir, token, link); err != nil {
		_ = XrayRemoveUser(cfg, email)
		fmt.Fprintf(os.Stderr, "Не удалось сохранить файл подписки: %v\n", err)
		os.Exit(1)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		_ = XrayRemoveUser(cfg, email)
		_ = RemoveSubFile(cfg.SubDir, token)
		fmt.Fprintf(os.Stderr, "Ошибка подключения к базе данных: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if _, err := AddClient(db, name, cfg.Country, clientUUID, email, token); err != nil {
		_ = XrayRemoveUser(cfg, email)
		_ = RemoveSubFile(cfg.SubDir, token)
		fmt.Fprintf(os.Stderr, "Ошибка сохранения в базу данных: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Пользователь успешно добавлен!")
	fmt.Printf("Ссылка: %s/%s\n", strings.TrimRight(cfg.SubBaseURL, "/"), token)
	fmt.Printf("Vless: %s\n", link)
}

func handleDeleteUser(cfg *Config, args []string) {
	requireRoot()

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

	fmt.Printf("Клиент %s удален\n", client.Name)
}

func handleListUsers(cfg *Config) {
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

	for _, c := range clients {
		fmt.Printf("[%d] %s\n", c.ID, c.Name)
		fmt.Printf("  Подписка:  %s/%s\n", strings.TrimRight(cfg.SubBaseURL, "/"), c.Token)
		fmt.Printf("  Создан:    %s\n\n", c.CreatedAt)
	}
}

func handleStatus(cfg *Config) {
	stubMsg, subMsg := CheckWastStatus(cfg.Domain, cfg.SubDir)
	certExpiry := GetCertExpiry(cfg.Domain)

	fmt.Println("Состояние сервера")
	fmt.Println()
	fmt.Printf("Домен:           %s\n", cfg.Domain)
	fmt.Printf("Локация:         %s\n", cfg.Country)
	fmt.Printf("SSL сертификат:  %s\n", certExpiry)
	fmt.Println()
	fmt.Println("Службы:")
	fmt.Printf("  Nginx:         %s\n", GetServiceStatus("nginx"))
	fmt.Printf("  Xray:          %s\n", GetServiceStatus("xray"))
	fmt.Printf("  BBR:           %s\n", GetBBRStatus())
	fmt.Printf("  Заглушка:      %s\n", stubMsg)
	fmt.Printf("  Подписки:      %s\n", subMsg)
	fmt.Println()
	fmt.Println("Системные ресурсы:")
	fmt.Printf("  Uptime:        %s\n", GetUptime())
	fmt.Printf("  CPU Load:      %s\n", GetCPUInfo())
	fmt.Printf("  Память (RAM):  %s\n", GetRAMInfo())
	fmt.Printf("  Диск (ROM):    %s\n", GetDiskInfo())
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
		} else {
			handleDeleteUser(cfg, args[1:])
		}
	case "list":
		handleListUsers(cfg)
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
