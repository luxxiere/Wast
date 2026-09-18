package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func GetServiceStatus(serviceName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName)
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return fmt.Sprintf("inactive (%s)", status)
		}
		return "не запущен"
	}

	if status == "active" {
		return "active (работает)"
	}
	if status != "" {
		return fmt.Sprintf("inactive (%s)", status)
	}
	return "не запущен"
}

func GetBBRStatus() string {
	data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return "неизвестно"
	}
	cc := strings.TrimSpace(string(data))
	if strings.Contains(cc, "bbr") {
		return "включен (bbr)"
	}
	return fmt.Sprintf("выключен (%s)", cc)
}

func CheckWastStatus(domain, subDir string) (string, string) {
	stubMsg := "нет ответа"
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         domain,
		},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   2 * time.Second,
	}

	req, err := http.NewRequest("GET", "https://127.0.0.1:8443/", nil)
	if err == nil {
		req.Host = domain
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode == 200 {
				stubMsg = "доступна (200 OK)"
			} else {
				stubMsg = fmt.Sprintf("код %d", resp.StatusCode)
			}
			resp.Body.Close()
		} else {
			stubMsg = "ошибка подключения"
		}
	}

	subCount := 0
	entries, err := os.ReadDir(subDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
				subCount++
			}
		}
	}

	return stubMsg, fmt.Sprintf("активны (%d шт.)", subCount)
}

func GetCertExpiry(domain string) string {
	certPath := filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem")
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "отсутствует"
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return "ошибка проверки"
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "ошибка проверки"
	}

	return cert.NotAfter.Format("Jan _2 15:04:05 2006 MST")
}

func GetUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "недоступно"
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "недоступно"
	}

	sec, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "недоступно"
	}

	totalSec := int64(sec)
	days := totalSec / 86400
	hours := (totalSec % 86400) / 3600
	minutes := (totalSec % 3600) / 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d дн.", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d ч.", hours))
	}
	parts = append(parts, fmt.Sprintf("%d мин.", minutes))

	return strings.Join(parts, " ")
}

func GetCPUInfo() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "недоступно"
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "недоступно"
	}

	cores := runtime.NumCPU()
	return fmt.Sprintf("%s, %s, %s (ядер: %d)", fields[0], fields[1], fields[2], cores)
}

func GetRAMInfo() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "недоступно"
	}

	var totalKB, availKB float64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valParts := strings.Fields(strings.TrimSpace(parts[1]))
		if len(valParts) == 0 {
			continue
		}
		v, _ := strconv.ParseFloat(valParts[0], 64)
		if key == "MemTotal" {
			totalKB = v
		} else if key == "MemAvailable" {
			availKB = v
		}
	}

	if totalKB == 0 {
		return "недоступно"
	}

	usedKB := totalKB - availKB
	pct := (usedKB / totalKB) * 100
	return fmt.Sprintf("%.0f MB / %.0f MB (%.1f%%)", usedKB/1024, totalKB/1024, pct)
}

func GetDiskInfo() string {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return "недоступно"
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bfree * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes

	totalGB := float64(totalBytes) / float64(1024*1024*1024)
	usedGB := float64(usedBytes) / float64(1024*1024*1024)
	pct := 0.0
	if totalBytes > 0 {
		pct = (float64(usedBytes) / float64(totalBytes)) * 100
	}

	return fmt.Sprintf("%.1f GB / %.1f GB (%.1f%%)", usedGB, totalGB, pct)
}
