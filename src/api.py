import json
import os
import subprocess
import tempfile

try:
    from src.config import cfg
except ImportError:
    from config import cfg

XRAY_CONFIG_PATH = os.getenv("XRAY_CONFIG", "/usr/local/etc/xray/config.json")


def update_xray_config_add(client_uuid: str, email: str, flow: str = "xtls-rprx-vision"):
    if not os.path.isfile(XRAY_CONFIG_PATH):
        return
    try:
        with open(XRAY_CONFIG_PATH, "r", encoding="utf-8") as f:
            data = json.load(f)
        for ib in data.get("inbounds", []):
            if ib.get("tag") == cfg.inbound_tag:
                clients = ib.setdefault("settings", {}).setdefault("clients", [])
                if not any(c.get("id") == client_uuid or c.get("email") == email for c in clients):
                    clients.append({"id": client_uuid, "email": email, "flow": flow})
                break
        with open(XRAY_CONFIG_PATH, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
    except Exception as e:
        print(f"Ошибка обновления {XRAY_CONFIG_PATH}: {e}")


def update_xray_config_remove(email: str):
    if not os.path.isfile(XRAY_CONFIG_PATH):
        return
    try:
        with open(XRAY_CONFIG_PATH, "r", encoding="utf-8") as f:
            data = json.load(f)
        for ib in data.get("inbounds", []):
            if ib.get("tag") == cfg.inbound_tag:
                clients = ib.setdefault("settings", {}).setdefault("clients", [])
                ib["settings"]["clients"] = [c for c in clients if c.get("email") != email]
                break
        with open(XRAY_CONFIG_PATH, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
    except Exception as e:
        print(f"Ошибка обновления {XRAY_CONFIG_PATH}: {e}")


def xray_add_user(client_uuid: str, email: str, flow: str = "xtls-rprx-vision") -> bool:
    update_xray_config_add(client_uuid, email, flow)
    payload = {
        "inbounds": [
            {
                "tag": cfg.inbound_tag,
                "listen": "0.0.0.0",
                "port": 443,
                "protocol": "vless",
                "settings": {
                    "clients": [
                        {
                            "id": client_uuid,
                            "email": email,
                            "flow": flow
                        }
                    ],
                    "decryption": "none"
                }
            }
        ]
    }
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as f:
        json.dump(payload, f)
        path = f.name

    try:
        result = subprocess.run(
            [cfg.xray_bin, "api", "adu", f"--server={cfg.api_addr}", path],
            capture_output=True,
            text=True
        )
        if result.returncode != 0 and result.stderr:
            if "already exists" not in result.stderr.lower():
                print(f"[Xray API] Ошибка: {result.stderr.strip()}")
        return result.returncode == 0
    finally:
        if os.path.exists(path):
            os.unlink(path)


def xray_remove_user(email: str) -> bool:
    update_xray_config_remove(email)
    result = subprocess.run(
        [cfg.xray_bin, "api", "rmu", f"--server={cfg.api_addr}", f"-tag={cfg.inbound_tag}", email],
        capture_output=True,
        text=True
    )
    if result.returncode != 0 and result.stderr:
        print(f"[Xray API] Ошибка: {result.stderr.strip()}")
    return result.returncode == 0
