#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

BASE_DIR=$(pwd)
CONFIG_DIR="$BASE_DIR/config"
RUNTIME_DIR="$BASE_DIR/runtime"
RESULT_DIR="$BASE_DIR/result"
API_DIR="$BASE_DIR/api"

# فایل‌های اصلی
DNS_DB="$RESULT_DIR/dns.txt"
ACTIVE_POOL="$RUNTIME_DIR/active.pool"
CLIENT_DB="$RUNTIME_DIR/clients.db"
DOMAINS_FILE="$CONFIG_DIR/domains.conf"
CLIENTS_FILE="$CONFIG_DIR/clients.conf"

MAX_LATENCY=800
ROTATE_INTERVAL=10
API_PORT=9180
WEB_PORT=9181

# ایجاد دایرکتوری‌ها اگر وجود ندارند
mkdir -p "$CONFIG_DIR" "$RUNTIME_DIR" "$RESULT_DIR" "$API_DIR"

# ایجاد فایل‌های پیش‌فرض اگر وجود ندارند
[[ ! -f "$DNS_DB" ]] && echo -e "8.8.8.8 50\n8.8.4.4 55\n1.1.1.1 40\n1.0.0.1 42" > "$DNS_DB"
[[ ! -f "$DOMAINS_FILE" ]] && echo -e "example.com\ngoogle.com\ncloudflare.com" > "$DOMAINS_FILE"
[[ ! -f "$CLIENTS_FILE" ]] && echo -e "0.0.0.0/0 DEFAULT" > "$CLIENTS_FILE"
[[ ! -f "$CLIENT_DB" ]] && touch "$CLIENT_DB"

log() {
  printf "[%s] %s\n" "$(date '+%H:%M:%S')" "$*" >&2
}

ip2int() {
  local a b c d
  IFS=. read -r a b c d <<< "$1"
  echo $(( (a<<24)+(b<<16)+(c<<8)+d ))
}

ip_in_subnet() {
  local ip subnet base mask
  ip="$1"
  subnet="$2"
  base="${subnet%/*}"
  mask="${subnet#*/}"
  (( $(ip2int "$ip") >> (32-mask) == $(ip2int "$base") >> (32-mask) ))
}

select_active_pool() {
  awk -v max="$MAX_LATENCY" '{if($2<=max)print $1,$2}' "$DNS_DB" | sort -k2 -n
}

rotate_pool() {
  local f="$1"
  local c
  c=$(wc -l < "$f")
  ((c<=1)) && return
  tail -n 1 "$f" > "$f.tmp"
  head -n $((c-1)) "$f" >> "$f.tmp"
  mv "$f.tmp" "$f"
}

emit_resolv() {
  local pool="$1"
  local out="$2"
  > "$out"
  while read -r ip lat; do
    echo "nameserver $ip" >> "$out"
  done < "$pool"
}

detect_client_tag() {
  local cip="$1"
  while read -r subnet tag; do
    [[ "$subnet" == "DEFAULT" ]] && echo "$tag" && return
    ip_in_subnet "$cip" "$subnet" && echo "$tag" && return
  done < "$CLIENTS_FILE"
}

dynamic_client_watch() {
  tail -F /var/log/syslog 2>/dev/null | \
  awk '/src=/{for(i=1;i<=NF;i++)if($i~"src="){split($i,a,"=");print a[2]}}' | \
  while read -r cip; do
    grep -q "$cip" "$CLIENT_DB" || echo "$cip" >> "$CLIENT_DB"
  done
}

runtime_loop() {
  while true; do
    select_active_pool > "$ACTIVE_POOL"
    rotate_pool "$ACTIVE_POOL"
    emit_resolv "$ACTIVE_POOL" "$RUNTIME_DIR/resolv.default"

    while read -r cip; do
      emit_resolv "$ACTIVE_POOL" "$RUNTIME_DIR/resolv.$cip"
    done < "$CLIENT_DB"

    sleep "$ROTATE_INTERVAL"
  done
}

api_server() {
  while true; do
    {
      read line
      echo "HTTP/1.1 200 OK"
      echo "Content-Type: application/json"
      echo
      echo "{"
      echo "\"dns_pool\":["
      awk '{printf "{\"ip\":\"%s\",\"latency\":%s},",$1,$2}' "$ACTIVE_POOL" | sed 's/,$//'
      echo "],"
      echo "\"clients\":["
      awk '{printf "\"%s\",",$1}' "$CLIENT_DB" | sed 's/,$//'
      echo "]"
      echo "}"
    } | nc -l "$API_PORT"
  done
}

web_panel() {
  while true; do
    {
      read line
      echo "HTTP/1.1 200 OK"
      echo "Content-Type: text/html"
      echo
      echo "<html><body>"
      echo "<h2>DNS Orchestrator</h2>"
      echo "<h3>Active DNS</h3><pre>"
      cat "$ACTIVE_POOL"
      echo "</pre><h3>Clients</h3><pre>"
      cat "$CLIENT_DB"
      echo "</pre></body></html>"
    } | nc -l "$WEB_PORT"
  done
}

log "Self-Initializing DNS Orchestrator started"
dynamic_client_watch &
runtime_loop &
api_server &
web_panel &

wait