package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var validTokenRegex = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

func GetXrayConfigPath() string {
	if env := os.Getenv("XRAY_CONFIG"); env != "" {
		return env
	}
	return "/usr/local/etc/xray/config.json"
}

func UpdateXrayConfigAdd(clientUUID, email, flow, inboundTag string) error {
	p := GetXrayConfigPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}

	inbounds, ok := root["inbounds"].([]interface{})
	if !ok {
		return nil
	}

	for _, ib := range inbounds {
		ibMap, ok := ib.(map[string]interface{})
		if !ok || ibMap["tag"] != inboundTag {
			continue
		}

		settings, ok := ibMap["settings"].(map[string]interface{})
		if !ok {
			settings = make(map[string]interface{})
			ibMap["settings"] = settings
		}

		var clients []interface{}
		if existing, ok := settings["clients"].([]interface{}); ok {
			clients = existing
		}

		exists := false
		for _, c := range clients {
			cMap, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if cMap["id"] == clientUUID || cMap["email"] == email {
				exists = true
				break
			}
		}

		if !exists {
			clients = append(clients, map[string]interface{}{
				"id":    clientUUID,
				"email": email,
				"flow":  flow,
			})
			settings["clients"] = clients
		}
		break
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, out, 0644)
}

func UpdateXrayConfigRemove(email, inboundTag string) error {
	p := GetXrayConfigPath()
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}

	inbounds, ok := root["inbounds"].([]interface{})
	if !ok {
		return nil
	}

	for _, ib := range inbounds {
		ibMap, ok := ib.(map[string]interface{})
		if !ok || ibMap["tag"] != inboundTag {
			continue
		}

		settings, ok := ibMap["settings"].(map[string]interface{})
		if !ok {
			continue
		}

		clients, ok := settings["clients"].([]interface{})
		if !ok {
			continue
		}

		var filtered []interface{}
		for _, c := range clients {
			cMap, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if cMap["email"] != email {
				filtered = append(filtered, c)
			}
		}
		settings["clients"] = filtered
		break
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, out, 0644)
}

func GetXrayClients(inboundTag string) (map[string]bool, error) {
	p := GetXrayConfigPath()
	result := make(map[string]bool)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return result, nil
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}

	inbounds, ok := root["inbounds"].([]interface{})
	if !ok {
		return result, nil
	}

	for _, ib := range inbounds {
		ibMap, ok := ib.(map[string]interface{})
		if !ok || ibMap["tag"] != inboundTag {
			continue
		}

		settings, ok := ibMap["settings"].(map[string]interface{})
		if !ok {
			continue
		}

		clients, ok := settings["clients"].([]interface{})
		if !ok {
			continue
		}

		for _, c := range clients {
			cMap, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if email, ok := cMap["email"].(string); ok && email != "" {
				result[email] = true
			}
		}
		break
	}
	return result, nil
}

func XrayAddUser(cfg *Config, clientUUID, email, flow string) error {
	_ = UpdateXrayConfigAdd(clientUUID, email, flow, cfg.InboundTag)

	payload := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      cfg.InboundTag,
				"listen":   "0.0.0.0",
				"port":     443,
				"protocol": "vless",
				"settings": map[string]interface{}{
					"clients": []interface{}{
						map[string]interface{}{
							"id":    clientUUID,
							"email": email,
							"flow":  flow,
						},
					},
					"decryption": "none",
				},
			},
		},
	}

	tmpFile, err := os.CreateTemp("", "wast-user-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	data, err := json.Marshal(payload)
	if err != nil {
		tmpFile.Close()
		return err
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	cmd := exec.Command(cfg.XrayBin, "api", "adu", fmt.Sprintf("--server=%s", cfg.APIAddr), tmpFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		sOut := string(out)
		if !strings.Contains(strings.ToLower(sOut), "already exists") {
			return fmt.Errorf("xray api error: %s", strings.TrimSpace(sOut))
		}
	}
	return nil
}

func XrayRemoveUser(cfg *Config, email string) error {
	_ = UpdateXrayConfigRemove(email, cfg.InboundTag)

	cmd := exec.Command(cfg.XrayBin, "api", "rmu", fmt.Sprintf("--server=%s", cfg.APIAddr), fmt.Sprintf("-tag=%s", cfg.InboundTag), email)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray api error: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func BuildVlessLink(domain, publicKey, shortID, clientUUID, country string) string {
	escapedCountry := url.PathEscape(country)
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=%s&fp=firefox&pbk=%s&sid=%s&spx=%%2F&type=tcp#%s",
		clientUUID,
		domain,
		domain,
		publicKey,
		shortID,
		escapedCountry,
	)
}

func BuildUserSubContent(clientUUID string, nodes []Node) string {
	var links []string
	for _, n := range nodes {
		if n.Status != "active" {
			continue
		}
		links = append(links, BuildVlessLink(n.Domain, n.PublicKey, n.ShortID, clientUUID, n.Name))
	}
	return strings.Join(links, "\n")
}

func WriteSubContent(subDir, token, content string) error {
	if !validTokenRegex.MatchString(token) {
		return errors.New("invalid subscription token")
	}

	if err := os.MkdirAll(subDir, 0755); err != nil {
		return err
	}

	filePath := filepath.Join(subDir, token+".txt")
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	return os.WriteFile(filePath, []byte(encoded), 0644)
}

func WriteSubFile(subDir, token, link string) error {
	return WriteSubContent(subDir, token, link)
}

func RemoveSubFile(subDir, token string) error {
	if !validTokenRegex.MatchString(token) {
		return errors.New("invalid subscription token")
	}

	filePath := filepath.Join(subDir, token+".txt")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(filePath)
}

func RegenerateAllSubscriptions(db *sql.DB, subDir string) error {
	clients, err := GetClients(db)
	if err != nil {
		return err
	}
	nodes, err := GetActiveNodes(db)
	if err != nil {
		return err
	}
	for _, c := range clients {
		content := BuildUserSubContent(c.ClientUUID, nodes)
		if err := WriteSubContent(subDir, c.Token, content); err != nil {
			return err
		}
	}
	return nil
}
