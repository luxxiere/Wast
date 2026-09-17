import json
import os
import subprocess
import tempfile
from typing import List, Tuple

try:
    from src.config import cfg
except ImportError:
    from config import cfg


def xray_add_user(client_uuid: str, email: str, flow: str = "xtls-rprx-vision") -> bool:
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
    result = subprocess.run(
        [cfg.xray_bin, "api", "rmu", f"--server={cfg.api_addr}", f"-tag={cfg.inbound_tag}", email],
        capture_output=True,
        text=True
    )
    if result.returncode != 0 and result.stderr:
        print(f"[Xray API] Ошибка: {result.stderr.strip()}")
    return result.returncode == 0


def xray_sync_users(users: List[Tuple[str, str]]) -> Tuple[int, int]:
    success = 0
    errors = 0
    for client_uuid, email in users:
        if xray_add_user(client_uuid, email):
            success += 1
        else:
            errors += 1
    return success, errors
