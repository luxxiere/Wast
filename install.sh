#!/usr/bin/env bash
set -euo pipefail

log() {
    echo "[Wast] $1"
}

if [ "$EUID" -ne 0 ]; then
    echo "Запусти скрипт от root"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="/opt/wast"

read -rp "Домен (например example.com): " DOMAIN
if [ -z "$DOMAIN" ]; then
    echo "Домен обязателен"
    exit 1
fi

read -rp "Email для certbot (можно оставить пустым): " CERT_EMAIL

read -rp "Включить BBR? (Рекомендуется) [Y/n]: " INSTALL_BBR
INSTALL_BBR=${INSTALL_BBR:-Y}

log "Устанавливаю зависимости"
apt-get update -y
apt-get install -y curl unzip jq sqlite3 python3 nginx certbot openssl ufw cron
systemctl enable cron
systemctl start cron

if [[ "$INSTALL_BBR" =~ ^[YyДд]$ ]]; then
    log "Включаю BBR"
    modprobe tcp_bbr 2>/dev/null || true
    sysctl -w net.core.default_qdisc=fq
    sysctl -w net.ipv4.tcp_congestion_control=bbr
    cat > /etc/sysctl.d/99-bbr.conf <<'EOF'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
EOF
    sysctl --system
fi

log "Скачиваю Xray-core"
XRAY_URL=$(curl -fsSL https://api.github.com/repos/XTLS/Xray-core/releases/latest \
    | jq -r '.assets[] | select(.name=="Xray-linux-64.zip") | .browser_download_url')
if [ -z "$XRAY_URL" ] || [ "$XRAY_URL" = "null" ]; then
    echo "Не удалось получить ссылку на Xray-core"
    exit 1
fi
TMP_DIR=$(mktemp -d)
curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip"
unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR" >/dev/null
install -m 755 "$TMP_DIR/xray" /usr/local/bin/xray
rm -rf "$TMP_DIR"

mkdir -p /usr/local/etc/xray "$INSTALL_DIR" /var/www/wast/sub /var/log/xray

log "Копирую файлы проекта в $INSTALL_DIR"
if [ -d "$SCRIPT_DIR/src" ]; then
    if [ "$SCRIPT_DIR" != "$INSTALL_DIR" ]; then
        cp -rf "$SCRIPT_DIR/src" "$INSTALL_DIR/"
        cp -rf "$SCRIPT_DIR/templates" "$INSTALL_DIR/"
        cp -rf "$SCRIPT_DIR/systemd" "$INSTALL_DIR/"
        cp -f "$SCRIPT_DIR/install.sh" "$INSTALL_DIR/"
    fi
else
    log "Скачиваю репозиторий Wast"
    TMP_REPO=$(mktemp -d)
    curl -fsSL "https://github.com/luxxiere/Wast/archive/refs/heads/main.tar.gz" -o "$TMP_REPO/wast.tar.gz" || \
    curl -fsSL "https://github.com/luxxiere/Wast/archive/refs/heads/master.tar.gz" -o "$TMP_REPO/wast.tar.gz"
    tar -xzf "$TMP_REPO/wast.tar.gz" -C "$TMP_REPO"
    SRC_DIR=$(find "$TMP_REPO" -mindepth 1 -maxdepth 1 -type d | head -n 1)
    cp -rf "$SRC_DIR/src" "$INSTALL_DIR/"
    cp -rf "$SRC_DIR/templates" "$INSTALL_DIR/"
    cp -rf "$SRC_DIR/systemd" "$INSTALL_DIR/"
    cp -f "$SRC_DIR/install.sh" "$INSTALL_DIR/"
    rm -rf "$TMP_REPO"
fi

log "Генерирую ключи Reality"
KEY_OUTPUT=$(/usr/local/bin/xray x25519)
PRIVATE_KEY=$(echo "$KEY_OUTPUT" | grep -iE "private ?key" | awk '{print $NF}')
PUBLIC_KEY=$(echo "$KEY_OUTPUT" | grep -iE "public ?key" | awk '{print $NF}')
SHORT_ID=$(openssl rand -hex 8)

if [ -z "$PRIVATE_KEY" ] || [ -z "$PUBLIC_KEY" ]; then
    echo "Не удалось сгенерировать ключи Reality"
    exit 1
fi

log "Создаю конфигурационный файл Wast"
cat > "$INSTALL_DIR/config.json" <<EOF
{
  "domain": "$DOMAIN",
  "public_key": "$PUBLIC_KEY",
  "short_id": "$SHORT_ID",
  "inbound_tag": "vless-in",
  "api_addr": "127.0.0.1:10085",
  "xray_bin": "/usr/local/bin/xray",
  "db_path": "$INSTALL_DIR/wast.db",
  "sub_dir": "/var/www/wast/sub",
  "fallback_country": "Default",
  "sub_base_url": "https://$DOMAIN/sub"
}
EOF

port_busy() {
    ss -ltn | awk '{print $4}' | grep -q ":$1\$"
}

if [ -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]; then
    log "Сертификат для $DOMAIN уже существует, использую его"
else
    if port_busy 80 || port_busy 443; then
        log "Порты заняты, освобождаю на время выпуска сертификата"
        systemctl stop nginx 2>/dev/null || true
        systemctl stop xray 2>/dev/null || true
    fi

    CERTBOT_ARGS=(certonly --standalone -d "$DOMAIN" --agree-tos --non-interactive)
    if [ -z "$CERT_EMAIL" ]; then
        CERTBOT_ARGS+=(--register-unsafely-without-email)
    else
        CERTBOT_ARGS+=(--email "$CERT_EMAIL")
    fi

    log "Получаю сертификат"
    certbot "${CERTBOT_ARGS[@]}"
fi

PROFILE_TITLE_B64=$(printf '%s' "Wast" | base64 -w0)

render_template() {
    local src="$1"
    local dst="$2"
    sed -e "s|{{DOMAIN}}|$DOMAIN|g" \
        -e "s|{{PRIVATE_KEY}}|$PRIVATE_KEY|g" \
        -e "s|{{PUBLIC_KEY}}|$PUBLIC_KEY|g" \
        -e "s|{{SHORT_ID}}|$SHORT_ID|g" \
        -e "s|{{INBOUND_TAG}}|vless-in|g" \
        -e "s|{{PROFILE_TITLE_B64}}|$PROFILE_TITLE_B64|g" \
        "$src" > "$dst"
}

log "Применяю шаблоны"
cp -f "$INSTALL_DIR/templates/index.html" /var/www/wast/index.html
render_template "$INSTALL_DIR/templates/nginx.conf" /etc/nginx/sites-available/wast.conf
render_template "$INSTALL_DIR/templates/xray.json" /usr/local/etc/xray/config.json
cp -f "$INSTALL_DIR/systemd/xray.service" /etc/systemd/system/xray.service

python3 -m json.tool /usr/local/etc/xray/config.json > /dev/null
/usr/local/bin/xray run -test -config /usr/local/etc/xray/config.json

rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/wast.conf /etc/nginx/sites-enabled/wast.conf
nginx -t

log "Настраиваю CLI"
chmod +x "$INSTALL_DIR/src/cli.py"
ln -sf "$INSTALL_DIR/src/cli.py" /usr/local/bin/wast

log "Пишу скрипт обновления сертификата"
cat > "$INSTALL_DIR/renew.sh" <<'EOF'
#!/usr/bin/env bash
set -e
systemctl stop nginx
systemctl stop xray
certbot renew --standalone --non-interactive
systemctl start nginx
systemctl start xray
EOF
chmod +x "$INSTALL_DIR/renew.sh"

TMP_CRON=$(mktemp)
crontab -l 2>/dev/null | grep -v "$INSTALL_DIR/renew.sh" > "$TMP_CRON" || true
echo "0 4 * * 0 $INSTALL_DIR/renew.sh >> /var/log/wast-renew.log 2>&1" >> "$TMP_CRON"
crontab "$TMP_CRON"
rm -f "$TMP_CRON"

log "Настраиваю ufw"
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

log "Запускаю сервисы"
systemctl daemon-reload
systemctl enable nginx
systemctl enable xray
systemctl restart nginx
systemctl restart xray

log "Успешно установлено!"
echo "Управление: команда 'wast'"