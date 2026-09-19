#!/usr/bin/env bash
set -euo pipefail

COLOR_WAST="\033[38;5;39m"
COLOR_NC="\033[0m"
COLOR_SUCCESS="\033[1;32m"
COLOR_ERROR="\033[1;31m"

log() {
    echo -e "${COLOR_WAST}[Wast]${COLOR_NC} $1"
}

log_success() {
    echo -e "${COLOR_SUCCESS}[Wast] $1${COLOR_NC}"
}

log_error() {
    echo -e "${COLOR_ERROR}[Wast] $1${COLOR_NC}"
}

run_dimmed() {
    local status
    "$@" 2>&1 | sed -e $'s/.*/\e[90m&\e[0m/'
    status=${PIPESTATUS[0]}
    if [ "$status" -ne 0 ]; then
        return "$status"
    fi
}

if [ "$EUID" -ne 0 ]; then
    log_error "Запусти скрипт от root"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="/opt/wast"

echo -e "${COLOR_WAST}=== Установка Wast ===${COLOR_NC}"
echo "Выберите роль для этого сервера:"
echo "  1) Основной сервер (Master) - управление, клиенты, подписки"
echo "  2) Нода (Node) - локация для подключения клиентов"
read -rp "Выберите [1/2] (по умолчанию 1): " INSTALL_ROLE_NUM
INSTALL_ROLE_NUM=${INSTALL_ROLE_NUM:-1}

if [ "$INSTALL_ROLE_NUM" = "1" ]; then
    ROLE="master"
    read -rp "Основной домен для подписок и управления (например, example.com): " MAIN_DOMAIN
    if [ -z "$MAIN_DOMAIN" ]; then
        log_error "Основной домен обязателен"
        exit 1
    fi

    read -rp "Домен локации для клиентов и заглушки (например, nl.example.com): " LOCATION_DOMAIN
    if [ -z "$LOCATION_DOMAIN" ]; then
        log_error "Домен локации обязателен"
        exit 1
    fi

    if [ "$MAIN_DOMAIN" = "$LOCATION_DOMAIN" ]; then
        log_error "Основной домен (под Cloudflare) и домен локации (прямой IP) не должны совпадать."
        log_error "Пример: основной example.com, локация nl.example.com"
        exit 1
    fi
    DOMAIN="$LOCATION_DOMAIN"
else
    ROLE="node"
    read -rp "Домен ноды для клиентов и заглушки (например, de.example.com): " DOMAIN
    if [ -z "$DOMAIN" ]; then
        log_error "Домен ноды обязателен"
        exit 1
    fi

    read -rp "URL подключения (из команды 'wast connect' на основном сервере): " CONNECT_URL
    if [ -z "$CONNECT_URL" ]; then
        log_error "URL подключения обязателен"
        exit 1
    fi
    CONNECT_URL=$(echo "$CONNECT_URL" | tr -d '[:space:]')
    if [[ ! "$CONNECT_URL" =~ ^https?:// ]]; then
        CONNECT_URL="https://$CONNECT_URL"
    fi

    read -rp "Секретный ключ (secret key): " CONNECT_SECRET
    if [ -z "$CONNECT_SECRET" ]; then
        log_error "Секретный ключ обязателен"
        exit 1
    fi
    CONNECT_SECRET=$(echo "$CONNECT_SECRET" | tr -d '[:space:]')
fi

read -rp "Email для certbot (можно оставить пустым): " CERT_EMAIL

read -rp "Включить BBR? (Рекомендуется) [Y/n]: " INSTALL_BBR
INSTALL_BBR=${INSTALL_BBR:-Y}

log "Установка системных зависимостей..."
run_dimmed apt-get update -y
run_dimmed apt-get install -y curl unzip jq sqlite3 nginx certbot openssl ufw cron golang
systemctl enable cron >/dev/null 2>&1 || true
systemctl start cron >/dev/null 2>&1 || true

log "Определение локации сервера..."
COUNTRY="Default"
GEO_DATA=$(curl -fsSL --connect-timeout 5 "http://ip-api.com/json/?lang=ru" 2>/dev/null || true)
if [ -n "$GEO_DATA" ]; then
    CODE=$(echo "$GEO_DATA" | jq -r '.countryCode // empty' 2>/dev/null || true)
    NAME=$(echo "$GEO_DATA" | jq -r '.country // empty' 2>/dev/null || true)
    if [ "${#CODE}" -eq 2 ] && [ -n "$NAME" ]; then
        CODE=$(echo "$CODE" | tr '[:lower:]' '[:upper:]')
        C1=$(printf "%d" "'${CODE:0:1}")
        C2=$(printf "%d" "'${CODE:1:1}")
        F1=$(printf "%08X" $((0x1F1E6 + C1 - 65)))
        F2=$(printf "%08X" $((0x1F1E6 + C2 - 65)))
        FLAG=$(printf "\U$F1\U$F2")
        COUNTRY="$FLAG $NAME"
    fi
fi

log "Определена локация: $COUNTRY"
read -rp "Хотите изменить название локации? [y/N]: " CHANGE_COUNTRY
if [[ "$CHANGE_COUNTRY" =~ ^[YyДд]$ ]]; then
    read -rp "Введите название локации (например, 🇳🇱 Нидерланды или 🇩🇪 Германия 1): " USER_COUNTRY
    if [ -n "$USER_COUNTRY" ]; then
        COUNTRY="$USER_COUNTRY"
    fi
fi

if [[ "$INSTALL_BBR" =~ ^[YyДд]$ ]]; then
    log "Настройка алгоритма BBR..."
    modprobe tcp_bbr 2>/dev/null || true
    cat > /etc/sysctl.d/99-bbr.conf <<'EOF'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
EOF
    run_dimmed sysctl --system
fi

log "Загрузка Xray-core..."
XRAY_URL=$(curl -fsSL https://api.github.com/repos/XTLS/Xray-core/releases/latest \
    | jq -r '.assets[] | select(.name=="Xray-linux-64.zip") | .browser_download_url')
if [ -z "$XRAY_URL" ] || [ "$XRAY_URL" = "null" ]; then
    log_error "Не удалось получить ссылку на Xray-core"
    exit 1
fi
TMP_DIR=$(mktemp -d)
run_dimmed curl -fsSL "$XRAY_URL" -o "$TMP_DIR/xray.zip"
unzip -o "$TMP_DIR/xray.zip" -d "$TMP_DIR" >/dev/null
install -m 755 "$TMP_DIR/xray" /usr/local/bin/xray
rm -rf "$TMP_DIR"

mkdir -p /usr/local/etc/xray "$INSTALL_DIR" /var/www/wast/sub /var/log/xray

log "Копирование файлов проекта в $INSTALL_DIR..."
if [ -d "$SCRIPT_DIR/src" ]; then
    if [ "$SCRIPT_DIR" != "$INSTALL_DIR" ]; then
        cp -rf "$SCRIPT_DIR/src" "$INSTALL_DIR/"
        cp -rf "$SCRIPT_DIR/templates" "$INSTALL_DIR/"
        cp -rf "$SCRIPT_DIR/systemd" "$INSTALL_DIR/"
        cp -f "$SCRIPT_DIR/go.mod" "$INSTALL_DIR/"
        cp -f "$SCRIPT_DIR/go.sum" "$INSTALL_DIR/"
        cp -f "$SCRIPT_DIR/install.sh" "$INSTALL_DIR/"
    fi
else
    log "Загрузка репозитория Wast..."
    TMP_REPO=$(mktemp -d)
    curl -fsSL "https://github.com/luxxiere/Wast/archive/refs/heads/main.tar.gz" -o "$TMP_REPO/wast.tar.gz" || \
    curl -fsSL "https://github.com/luxxiere/Wast/archive/refs/heads/master.tar.gz" -o "$TMP_REPO/wast.tar.gz"
    tar -xzf "$TMP_REPO/wast.tar.gz" -C "$TMP_REPO"
    SRC_DIR=$(find "$TMP_REPO" -mindepth 1 -maxdepth 1 -type d | head -n 1)
    cp -rf "$SRC_DIR/src" "$INSTALL_DIR/"
    cp -rf "$SRC_DIR/templates" "$INSTALL_DIR/"
    cp -rf "$SRC_DIR/systemd" "$INSTALL_DIR/"
    cp -f "$SRC_DIR/go.mod" "$INSTALL_DIR/"
    cp -f "$SRC_DIR/go.sum" "$INSTALL_DIR/"
    cp -f "$SRC_DIR/install.sh" "$INSTALL_DIR/"
    rm -rf "$TMP_REPO"
fi

log "Генерация ключей Reality..."
KEY_OUTPUT=$(/usr/local/bin/xray x25519)
PRIVATE_KEY=$(echo "$KEY_OUTPUT" | grep -iE "private ?key" | awk '{print $NF}')
PUBLIC_KEY=$(echo "$KEY_OUTPUT" | grep -iE "public ?key" | awk '{print $NF}')
SHORT_ID=$(openssl rand -hex 8)

if [ -z "$PRIVATE_KEY" ] || [ -z "$PUBLIC_KEY" ]; then
    log_error "Не удалось сгенерировать ключи Reality"
    exit 1
fi

port_busy() {
    ss -ltn | awk '{print $4}' | grep -q ":$1\$"
}

issue_ssl_cert() {
    local d="$1"
    if [ -f "/etc/letsencrypt/live/$d/fullchain.pem" ]; then
        log "Используется существующий SSL сертификат для $d"
    else
        if port_busy 80 || port_busy 443; then
            systemctl stop nginx 2>/dev/null || true
            systemctl stop xray 2>/dev/null || true
        fi

        CERTBOT_ARGS=(certonly --standalone -d "$d" --agree-tos --non-interactive)
        if [ -z "$CERT_EMAIL" ]; then
            CERTBOT_ARGS+=(--register-unsafely-without-email)
        else
            CERTBOT_ARGS+=(--email "$CERT_EMAIL")
        fi

        log "Выпуск SSL сертификата для $d..."
        if ! run_dimmed certbot "${CERTBOT_ARGS[@]}"; then
            log_error "Внимание: не удалось получить сертификат для $d через Let's Encrypt"
            log "Создание временного самоподписанного сертификата для корректного запуска Nginx..."
            mkdir -p "/etc/letsencrypt/live/$d"
            openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
                -keyout "/etc/letsencrypt/live/$d/privkey.pem" \
                -out "/etc/letsencrypt/live/$d/fullchain.pem" \
                -subj "/CN=$d" >/dev/null 2>&1 || true
        fi
    fi
}

PROFILE_TITLE_B64=$(printf '%s' "Wast" | base64 -w0)

if [ "$ROLE" = "master" ]; then
    issue_ssl_cert "$LOCATION_DOMAIN"
    if [ "$MAIN_DOMAIN" != "$LOCATION_DOMAIN" ]; then
        issue_ssl_cert "$MAIN_DOMAIN"
    fi

    log "Создание конфигурации основного сервера..."
    cat > "$INSTALL_DIR/config.json" <<EOF
{
  "role": "master",
  "main_domain": "$MAIN_DOMAIN",
  "domain": "$LOCATION_DOMAIN",
  "public_key": "$PUBLIC_KEY",
  "short_id": "$SHORT_ID",
  "inbound_tag": "vless-in",
  "api_addr": "127.0.0.1:10085",
  "xray_bin": "/usr/local/bin/xray",
  "db_path": "$INSTALL_DIR/wast.db",
  "sub_dir": "/var/www/wast/sub",
  "country": "$COUNTRY",
  "sub_base_url": "https://$MAIN_DOMAIN/sub",
  "daemon_port": 8080,
  "sync_interval": 5
}
EOF

    log "Применение конфигурационных шаблонов..."
    cp -f "$INSTALL_DIR/templates/index.html" /var/www/wast/index.html
    
    sed -e "s|{{MAIN_DOMAIN}}|$MAIN_DOMAIN|g" \
        -e "s|{{LOCATION_DOMAIN}}|$LOCATION_DOMAIN|g" \
        -e "s|{{DAEMON_PORT}}|8080|g" \
        -e "s|{{PROFILE_TITLE_B64}}|$PROFILE_TITLE_B64|g" \
        "$INSTALL_DIR/templates/nginx-master.conf" > /etc/nginx/sites-available/wast.conf

    sed -e "s|{{DOMAIN}}|$LOCATION_DOMAIN|g" \
        -e "s|{{PRIVATE_KEY}}|$PRIVATE_KEY|g" \
        -e "s|{{PUBLIC_KEY}}|$PUBLIC_KEY|g" \
        -e "s|{{SHORT_ID}}|$SHORT_ID|g" \
        -e "s|{{INBOUND_TAG}}|vless-in|g" \
        "$INSTALL_DIR/templates/xray.json" > /usr/local/etc/xray/config.json

    cp -f "$INSTALL_DIR/systemd/xray.service" /etc/systemd/system/xray.service
    cp -f "$INSTALL_DIR/systemd/wast-api.service" /etc/systemd/system/wast-api.service

    log "Сборка Wast CLI..."
    (cd "$INSTALL_DIR" && run_dimmed go build -ldflags="-s -w" -o /usr/local/bin/wast ./src)
    chmod +x /usr/local/bin/wast

    log "Инициализация базы данных и локации..."
    sqlite3 "$INSTALL_DIR/wast.db" "CREATE TABLE IF NOT EXISTS clients (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, country TEXT NOT NULL, client_uuid TEXT NOT NULL, email TEXT NOT NULL, token TEXT NOT NULL, created_at TEXT NOT NULL);"
    sqlite3 "$INSTALL_DIR/wast.db" "CREATE TABLE IF NOT EXISTS nodes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, domain TEXT NOT NULL, public_key TEXT NOT NULL, short_id TEXT NOT NULL, api_token TEXT NOT NULL, is_local INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'active', last_seen TEXT NOT NULL, cpu_info TEXT NOT NULL DEFAULT '', ram_info TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);"
    sqlite3 "$INSTALL_DIR/wast.db" "CREATE TABLE IF NOT EXISTS connect_tokens (token TEXT PRIMARY KEY, secret TEXT NOT NULL, expires_at TEXT NOT NULL, used INTEGER NOT NULL DEFAULT 0);"
    sqlite3 "$INSTALL_DIR/wast.db" "INSERT OR IGNORE INTO nodes (id, name, domain, public_key, short_id, api_token, is_local, status, last_seen, cpu_info, ram_info, created_at) VALUES (1, '$COUNTRY', '$LOCATION_DOMAIN', '$PUBLIC_KEY', '$SHORT_ID', 'local', 1, 'active', datetime('now'), '', '', datetime('now'));"

    run_dimmed /usr/local/bin/xray run -test -config /usr/local/etc/xray/config.json
    rm -f /etc/nginx/sites-enabled/default
    ln -sf /etc/nginx/sites-available/wast.conf /etc/nginx/sites-enabled/wast.conf
    run_dimmed nginx -t

    systemctl daemon-reload
    systemctl enable nginx >/dev/null 2>&1 || true
    systemctl enable xray >/dev/null 2>&1 || true
    systemctl enable wast-api >/dev/null 2>&1 || true
    systemctl restart nginx
    systemctl restart xray
    systemctl restart wast-api

else
    issue_ssl_cert "$DOMAIN"

    log "Сборка Wast CLI..."
    (cd "$INSTALL_DIR" && run_dimmed go build -ldflags="-s -w" -o /usr/local/bin/wast ./src)
    chmod +x /usr/local/bin/wast

    log "Подключение к основному серверу..."
    REGISTER_PAYLOAD=$(jq -n \
        --arg sec "$CONNECT_SECRET" \
        --arg dom "$DOMAIN" \
        --arg name "$COUNTRY" \
        --arg pbk "$PUBLIC_KEY" \
        --arg sid "$SHORT_ID" \
        '{secret: $sec, domain: $dom, name: $name, public_key: $pbk, short_id: $sid}')

    CONNECT_RESP=$(curl -fsSL -X POST -H "Content-Type: application/json" -d "$REGISTER_PAYLOAD" "$CONNECT_URL" 2>/dev/null || true)
    STATUS=$(echo "$CONNECT_RESP" | jq -r '.status // empty' 2>/dev/null || true)
    if [ "$STATUS" != "ok" ]; then
        log_error "Ошибка регистрации на основном сервере: $CONNECT_RESP"
        log_error "Проверьте правильность URL подключения и секретного ключа."
        exit 1
    fi

    NODE_ID=$(echo "$CONNECT_RESP" | jq -r '.node_id')
    API_TOKEN=$(echo "$CONNECT_RESP" | jq -r '.api_token')
    MASTER_URL=$(echo "$CONNECT_URL" | sed -E 's|^(https?://[^/]+).*|\1|')

    log "Создание конфигурации ноды..."
    cat > "$INSTALL_DIR/config.json" <<EOF
{
  "role": "node",
  "domain": "$DOMAIN",
  "master_url": "$MASTER_URL",
  "node_token": "$API_TOKEN",
  "node_id": $NODE_ID,
  "public_key": "$PUBLIC_KEY",
  "short_id": "$SHORT_ID",
  "inbound_tag": "vless-in",
  "api_addr": "127.0.0.1:10085",
  "xray_bin": "/usr/local/bin/xray",
  "db_path": "$INSTALL_DIR/wast.db",
  "sub_dir": "/var/www/wast/sub",
  "country": "$COUNTRY",
  "sync_interval": 5
}
EOF

    log "Применение конфигурационных шаблонов..."
    cp -f "$INSTALL_DIR/templates/index.html" /var/www/wast/index.html

    sed -e "s|{{DOMAIN}}|$DOMAIN|g" \
        "$INSTALL_DIR/templates/nginx-node.conf" > /etc/nginx/sites-available/wast.conf

    sed -e "s|{{DOMAIN}}|$DOMAIN|g" \
        -e "s|{{PRIVATE_KEY}}|$PRIVATE_KEY|g" \
        -e "s|{{PUBLIC_KEY}}|$PUBLIC_KEY|g" \
        -e "s|{{SHORT_ID}}|$SHORT_ID|g" \
        -e "s|{{INBOUND_TAG}}|vless-in|g" \
        "$INSTALL_DIR/templates/xray.json" > /usr/local/etc/xray/config.json

    cp -f "$INSTALL_DIR/systemd/xray.service" /etc/systemd/system/xray.service
    cp -f "$INSTALL_DIR/systemd/wast-node.service" /etc/systemd/system/wast-node.service

    run_dimmed /usr/local/bin/xray run -test -config /usr/local/etc/xray/config.json
    rm -f /etc/nginx/sites-enabled/default
    ln -sf /etc/nginx/sites-available/wast.conf /etc/nginx/sites-enabled/wast.conf
    run_dimmed nginx -t

    systemctl daemon-reload
    systemctl enable nginx >/dev/null 2>&1 || true
    systemctl enable xray >/dev/null 2>&1 || true
    systemctl enable wast-node >/dev/null 2>&1 || true
    systemctl restart nginx
    systemctl restart xray
    systemctl restart wast-node
fi

log "Настройка автопродления SSL сертификата..."
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

log "Настройка сетевого экрана ufw..."
run_dimmed ufw allow OpenSSH
run_dimmed ufw allow 80/tcp
run_dimmed ufw allow 443/tcp
run_dimmed ufw --force enable

echo
log_success "Успешно установлено!"
if [ "$ROLE" = "master" ]; then
    echo -e "${COLOR_WAST}[Wast]${COLOR_NC} Управление: команда 'wast'"
    echo -e "${COLOR_WAST}[Wast]${COLOR_NC} Для подключения ноды используйте: 'wast connect'"
else
    echo -e "${COLOR_WAST}[Wast]${COLOR_NC} Нода успешно подключена к основному серверу!"
    echo -e "${COLOR_WAST}[Wast]${COLOR_NC} Статус ноды: 'wast status'"
fi