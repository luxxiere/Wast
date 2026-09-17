#!/usr/bin/env python3
import base64
import json
import os
import secrets
import subprocess
import sys
import urllib.request
import uuid
from urllib.parse import quote

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PARENT_DIR = os.path.dirname(SCRIPT_DIR)
if SCRIPT_DIR not in sys.path:
    sys.path.insert(0, SCRIPT_DIR)
if PARENT_DIR not in sys.path:
    sys.path.insert(0, PARENT_DIR)

try:
    from src.config import cfg
    from src.database import (
        add_client as db_add_client,
        get_clients as db_get_clients,
        get_client_by_id as db_get_client_by_id,
        delete_client as db_delete_client,
        count_clients as db_count_clients,
        get_clients_for_sync as db_get_clients_for_sync
    )
    from src.api import (
        xray_add_user,
        xray_remove_user,
        xray_sync_users
    )
    from src.stats import (
        get_service_status,
        get_bbr_status,
        check_wast_status,
        get_cert_expiry,
        get_uptime,
        get_cpu_info,
        get_ram_info,
        get_disk_info
    )
except ImportError:
    from config import cfg
    from database import (
        add_client as db_add_client,
        get_clients as db_get_clients,
        get_client_by_id as db_get_client_by_id,
        delete_client as db_delete_client,
        count_clients as db_count_clients,
        get_clients_for_sync as db_get_clients_for_sync
    )
    from api import (
        xray_add_user,
        xray_remove_user,
        xray_sync_users
    )
    from stats import (
        get_service_status,
        get_bbr_status,
        check_wast_status,
        get_cert_expiry,
        get_uptime,
        get_cpu_info,
        get_ram_info,
        get_disk_info
    )


def country_flag(code: str) -> str:
    code = code.upper()
    return "".join(chr(0x1F1E6 + ord(c) - ord("A")) for c in code)


def detect_server_country() -> str:
    try:
        req = urllib.request.Request("http://ip-api.com/json/?lang=ru")
        with urllib.request.urlopen(req, timeout=5) as r:
            data = json.loads(r.read().decode())
        code = data.get("countryCode", "")
        name = data.get("country", "")
        if code and name:
            return f"{country_flag(code)} {name}"
    except Exception:
        pass
    return cfg.fallback_country


def build_vless_link(client_uuid: str, country: str) -> str:
    params = (
        "encryption=none&flow=xtls-rprx-vision&security=reality"
        f"&sni={cfg.domain}&fp=firefox&pbk={cfg.public_key}&sid={cfg.short_id}"
        "&spx=%2F&type=tcp"
    )
    return f"vless://{client_uuid}@{cfg.domain}:443?{params}#{quote(country)}"


def write_sub_file(token: str, link: str):
    os.makedirs(cfg.sub_dir, exist_ok=True)
    encoded = base64.b64encode(link.encode()).decode()
    path = os.path.join(cfg.sub_dir, f"{token}.txt")
    with open(path, "w", encoding="utf-8") as f:
        f.write(encoded)


def remove_sub_file(token: str):
    path = os.path.join(cfg.sub_dir, f"{token}.txt")
    if os.path.exists(path):
        os.remove(path)


def add_client():
    print("Добавить клиента\n")
    name = input("Имя: ").strip()
    if not name:
        print("\nИмя не может быть пустым")
        return
    detected = detect_server_country()
    country = input(f"Страна (Enter = '{detected}'): ").strip()
    if not country:
        country = detected
    client_uuid = str(uuid.uuid4())
    token = secrets.token_hex(8)
    email = f"{name}-{client_uuid[:8]}"

    if not xray_add_user(client_uuid, email):
        print("\nНе удалось добавить пользователя в Xray")
        return

    link = build_vless_link(client_uuid, country)
    write_sub_file(token, link)
    db_add_client(name, country, client_uuid, email, token)

    print(f"\nКлиент {name} успешно добавлен")
    print(f"Подписка:  {cfg.sub_base_url}/{token}")
    print(f"Ссылка:    {link}")


def list_clients():
    print("Список клиентов\n")
    rows = db_get_clients()
    if not rows:
        print("Клиентов пока нет")
        return
    for r in rows:
        print(f"[{r['id']}] {r['name']} ({r['country']})")
        print(f"    Подписка:  {cfg.sub_base_url}/{r['token']}")
        print(f"    Создан:    {r['created_at']}\n")


def delete_client():
    print("Удалить клиента\n")
    rows = db_get_clients()
    if not rows:
        print("Клиентов пока нет")
        return
    for r in rows:
        print(f"[{r['id']}] {r['name']} ({r['country']})")
    raw = input("\nID клиента для удаления: ").strip()
    if not raw.isdigit():
        print("\nНекорректный ID")
        return
    cid = int(raw)
    row = db_get_client_by_id(cid)
    if not row:
        print("\nКлиент не найден")
        return

    name = row["name"]
    email = row["email"]
    token = row["token"]

    xray_remove_user(email)
    remove_sub_file(token)
    db_delete_client(cid)
    print(f"\nКлиент {name} удален")


def sync_clients():
    print("Синхронизация с Xray\n")
    users = db_get_clients_for_sync()
    if not users:
        print("В базе нет клиентов для синхронизации")
        return
    success, errors = xray_sync_users(users)
    print(f"Успешно: {success}")
    if errors:
        print(f"Ошибок:  {errors}")


def clients_menu():
    while True:
        os.system("clear")
        cnt = db_count_clients()
        print("Управление клиентами\n")
        print(f"Клиентов в базе:     {cnt}\n")
        print("1) Список клиентов")
        print("2) Добавить клиента")
        print("3) Удалить клиента")
        print("Enter) Вернуться в предыдущее меню\n")
        choice = input("Выберите пункт: ").strip()
        if choice == "1":
            os.system("clear")
            list_clients()
            input("\nEnter) Вернуться в предыдущее меню")
        elif choice == "2":
            os.system("clear")
            add_client()
            input("\nEnter) Вернуться в предыдущее меню")
        elif choice == "3":
            os.system("clear")
            delete_client()
            input("\nEnter) Вернуться в предыдущее меню")
        elif choice == "" or choice == "0":
            return


def server_info():
    os.system("clear")
    stub_msg, sub_msg = check_wast_status(cfg.domain, cfg.sub_dir)
    cert_expiry = get_cert_expiry(cfg.domain)
    client_cnt = db_count_clients()

    print("Состояние сервера\n")
    print(f"Домен:           {cfg.domain}")
    print(f"SSL сертификат:  {cert_expiry}")
    print(f"Клиентов в базе: {client_cnt}\n")
    print("Службы:")
    print(f"  Nginx:         {get_service_status('nginx')}")
    print(f"  Xray:          {get_service_status('xray')}")
    print(f"  BBR:           {get_bbr_status()}")
    print(f"  Заглушка:      {stub_msg}")
    print(f"  Подписки:      {sub_msg}\n")
    print("Системные ресурсы:")
    print(f"  Uptime:        {get_uptime()}")
    print(f"  CPU Load:      {get_cpu_info()}")
    print(f"  Память (RAM):  {get_ram_info()}")
    print(f"  Диск (ROM):    {get_disk_info()}\n")
    input("Enter) Вернуться в предыдущее меню")


def full_uninstall():
    print("Удаление Wast\n")
    confirm = input("Удалить xray, nginx конфиг, сертификат и wast полностью? Введи 'yes': ").strip()
    if confirm != "yes":
        print("\nОтменено")
        input("\nEnter) Вернуться в предыдущее меню")
        return
    subprocess.run(["systemctl", "stop", "xray"], check=False)
    subprocess.run(["systemctl", "disable", "xray"], check=False)
    subprocess.run(["systemctl", "stop", "nginx"], check=False)
    subprocess.run(["rm", "-f", "/etc/systemd/system/xray.service"], check=False)
    subprocess.run(["systemctl", "daemon-reload"], check=False)
    subprocess.run(["rm", "-f", "/usr/local/bin/xray"], check=False)
    subprocess.run(["rm", "-rf", "/usr/local/etc/xray"], check=False)
    subprocess.run(["rm", "-rf", "/var/log/xray"], check=False)
    subprocess.run(["rm", "-f", "/etc/nginx/sites-enabled/wast.conf"], check=False)
    subprocess.run(["rm", "-f", "/etc/nginx/sites-available/wast.conf"], check=False)
    subprocess.run(["rm", "-rf", "/var/www/wast"], check=False)
    if cfg.domain:
        subprocess.run(["certbot", "delete", "--cert-name", cfg.domain, "--non-interactive"], check=False)
    subprocess.run(["systemctl", "start", "nginx"], check=False)

    crontab = subprocess.run(["crontab", "-l"], capture_output=True, text=True)
    if crontab.returncode == 0:
        new_cron = "\n".join(
            line for line in crontab.stdout.splitlines() if "/opt/wast/renew.sh" not in line
        )
        subprocess.run(["crontab", "-"], input=new_cron, text=True, check=False)

    subprocess.run(["ufw", "delete", "allow", "80/tcp"], check=False)
    subprocess.run(["ufw", "delete", "allow", "443/tcp"], check=False)
    subprocess.run(["rm", "-f", "/usr/local/bin/wast"], check=False)
    print("\nУдалено. Enter для выхода.")
    input()
    subprocess.run(["rm", "-rf", "/opt/wast"], check=False)
    sys.exit(0)


def main():
    if os.geteuid() != 0:
        print("Запусти скрипт от root")
        sys.exit(1)

    while True:
        os.system("clear")
        nginx_st = "запущен" if "active" in get_service_status("nginx") else "остановлен"
        xray_st = "запущен" if "active" in get_service_status("xray") else "остановлен"
        bbr_st = "включен" if "bbr" in get_bbr_status() else "выключен"
        cnt = db_count_clients()

        print("╔═══════════════════════════════╗")
        print("║        Wast by Luxiere        ║")
        print("╚═══════════════════════════════╝\n")
        print(f"Домен:           {cfg.domain}")
        print(f"Nginx / Xray:    {nginx_st} / {xray_st}")
        print(f"BBR:             {bbr_st}")
        print(f"Клиенты:         {cnt}\n")
        print("1) Управление клиентами")
        print("2) Состояние сервера")
        print("3) Синхронизировать с Xray")
        print("4) Удалить Wast")
        print("Enter) Выход\n")
        choice = input("Выберите пункт: ").strip()
        if choice == "1":
            clients_menu()
        elif choice == "2":
            server_info()
        elif choice == "3":
            os.system("clear")
            sync_clients()
            input("\nEnter) Вернуться в предыдущее меню")
        elif choice == "4":
            os.system("clear")
            full_uninstall()
        elif choice == "" or choice == "0":
            sys.exit(0)


if __name__ == "__main__":
    main()
