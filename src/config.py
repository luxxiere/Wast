import json
import os

CONFIG_PATH = os.getenv("WAST_CONFIG", "/opt/wast/config.json")


class Config:
    def __init__(self, path: str = CONFIG_PATH):
        self.config_path = path
        self.domain = ""
        self.public_key = ""
        self.short_id = ""
        self.inbound_tag = "vless-in"
        self.api_addr = "127.0.0.1:10085"
        self.xray_bin = "/usr/local/bin/xray"
        self.db_path = "/opt/wast/wast.db"
        self.sub_dir = "/var/www/wast/sub"
        self.fallback_country = "Default"
        self.sub_base_url = ""
        self.load()

    def load(self):
        if os.path.isfile(self.config_path):
            try:
                with open(self.config_path, "r", encoding="utf-8") as f:
                    data = json.load(f)
                self.domain = data.get("domain", self.domain)
                self.public_key = data.get("public_key", self.public_key)
                self.short_id = data.get("short_id", self.short_id)
                self.inbound_tag = data.get("inbound_tag", self.inbound_tag)
                self.api_addr = data.get("api_addr", self.api_addr)
                self.xray_bin = data.get("xray_bin", self.xray_bin)
                self.db_path = data.get("db_path", self.db_path)
                self.sub_dir = data.get("sub_dir", self.sub_dir)
                self.fallback_country = data.get("fallback_country", self.fallback_country)
                self.sub_base_url = data.get("sub_base_url", f"https://{self.domain}/sub" if self.domain else "")
            except Exception as e:
                print(f"Ошибка чтения конфигурации {self.config_path}: {e}")

    def save(self):
        data = {
            "domain": self.domain,
            "public_key": self.public_key,
            "short_id": self.short_id,
            "inbound_tag": self.inbound_tag,
            "api_addr": self.api_addr,
            "xray_bin": self.xray_bin,
            "db_path": self.db_path,
            "sub_dir": self.sub_dir,
            "sub_base_url": self.sub_base_url or (f"https://{self.domain}/sub" if self.domain else ""),
            "fallback_country": self.fallback_country
        }
        os.makedirs(os.path.dirname(self.config_path), exist_ok=True)
        with open(self.config_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)


cfg = Config()
