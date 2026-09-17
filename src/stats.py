import os
import shutil
import ssl
import subprocess
import urllib.request
from typing import Tuple


def get_service_status(service_name: str) -> str:
    try:
        res = subprocess.run(
            ["systemctl", "is-active", service_name],
            capture_output=True,
            text=True,
            timeout=2
        )
        status = res.stdout.strip()
        if status == "active":
            return "active (работает)"
        return f"inactive ({status})" if status else "не запущен"
    except Exception:
        return "неизвестно"


def get_bbr_status() -> str:
    try:
        with open("/proc/sys/net/ipv4/tcp_congestion_control", "r") as f:
            cc = f.read().strip()
        if "bbr" in cc:
            return "включен (bbr)"
        return f"выключен ({cc})"
    except Exception:
        return "неизвестно"


def check_wast_status(domain: str, sub_dir: str) -> Tuple[str, str]:
    stub_msg = "нет ответа"
    try:
        ctx = ssl._create_unverified_context()
        req = urllib.request.Request("https://127.0.0.1:8443/", headers={"Host": domain})
        with urllib.request.urlopen(req, context=ctx, timeout=2) as resp:
            if resp.status == 200:
                stub_msg = "доступна (200 OK)"
            else:
                stub_msg = f"код {resp.status}"
    except Exception:
        stub_msg = "ошибка подключения"

    sub_count = 0
    if os.path.isdir(sub_dir):
        sub_count = len([f for f in os.listdir(sub_dir) if f.endswith(".txt")])

    return stub_msg, f"активны ({sub_count} шт.)"


def get_cert_expiry(domain: str) -> str:
    cert_path = f"/etc/letsencrypt/live/{domain}/fullchain.pem"
    if not os.path.isfile(cert_path):
        return "отсутствует"
    try:
        res = subprocess.run(
            ["openssl", "x509", "-enddate", "-noout", "-in", cert_path],
            capture_output=True,
            text=True,
            timeout=2
        )
        out = res.stdout.strip()
        if "notAfter=" in out:
            return out.split("notAfter=", 1)[1].strip()
        return out
    except Exception:
        return "ошибка проверки"


def get_uptime() -> str:
    try:
        with open("/proc/uptime", "r") as f:
            sec = float(f.readline().split()[0])
        days = int(sec // 86400)
        hours = int((sec % 86400) // 3600)
        minutes = int((sec % 3600) // 60)
        parts = []
        if days > 0:
            parts.append(f"{days} дн.")
        if hours > 0:
            parts.append(f"{hours} ч.")
        parts.append(f"{minutes} мин.")
        return " ".join(parts)
    except Exception:
        return "недоступно"


def get_cpu_info() -> str:
    try:
        load1, load5, load15 = os.getloadavg()
        cores = os.cpu_count() or 1
        return f"{load1:.2f}, {load5:.2f}, {load15:.2f} (ядер: {cores})"
    except Exception:
        return "недоступно"


def get_ram_info() -> str:
    try:
        mem = {}
        with open("/proc/meminfo", "r") as f:
            for line in f:
                parts = line.split(":")
                if len(parts) == 2:
                    mem[parts[0].strip()] = int(parts[1].split()[0])
        total = mem.get("MemTotal", 0)
        avail = mem.get("MemAvailable", 0)
        used = total - avail
        pct = (used / total * 100) if total else 0
        return f"{used / 1024:.0f} MB / {total / 1024:.0f} MB ({pct:.1f}%)"
    except Exception:
        return "недоступно"


def get_disk_info() -> str:
    try:
        total, used, free = shutil.disk_usage("/")
        pct = (used / total * 100) if total else 0
        return f"{used / (1024**3):.1f} GB / {total / (1024**3):.1f} GB ({pct:.1f}%)"
    except Exception:
        return "недоступно"
