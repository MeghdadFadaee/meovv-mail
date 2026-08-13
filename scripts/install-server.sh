#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPOSITORY_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
readonly NGINX_AVAILABLE="/etc/nginx/sites-available/meovv-mail"
readonly NGINX_ENABLED="/etc/nginx/sites-enabled/meovv-mail"
readonly CERTBOT_WEBROOT="/var/www/certbot"
readonly CERTBOT_HOOK="/etc/letsencrypt/renewal-hooks/deploy/meovv-mail"
readonly MAILCTL_BIN="/usr/local/bin/mailctl"
readonly MANAGED_MARKER="Managed by MEOVV Mail installer"
readonly MEOVV_RELEASE="0.1.0"

COMMAND=""
MAIL_HOSTNAME=""
ADMIN_EMAIL=""
BUNDLE_DIR="$REPOSITORY_DIR"
ASSUME_YES=false
CONFIGURE_DNS=false
DNS_ZONE=""
PUBLIC_IPV4=""
PUBLIC_IPV6=""
TEMP_CONTAINER=""
TEMP_FILES=()
CF_API_TOKEN=""
CF_ZONE_ID=""

log() {
    printf '\n\033[1;35m==>\033[0m %s\n' "$*"
}

warn() {
    printf '\033[1;33mWARNING:\033[0m %s\n' "$*" >&2
}

die() {
    printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [[ -n "$TEMP_CONTAINER" ]]; then
        docker rm -f "$TEMP_CONTAINER" >/dev/null 2>&1 || true
    fi
    local temporary_file
    for temporary_file in "${TEMP_FILES[@]}"; do
        rm -f -- "$temporary_file"
    done
}
trap cleanup EXIT

usage() {
    cat <<'EOF'
Install and operate MEOVV Mail prerequisites on Ubuntu 24.04/26.04 or Debian 13.

Usage:
  sudo ./scripts/install-server.sh install \
    --hostname mail.example.com \
    --email admin@example.com \
    [--configure-dns] [--dns-zone example.com] \
    [--public-ipv4 203.0.113.10] [--public-ipv6 2001:db8::10] \
    [--bundle-dir /opt/meovv-mail] [--yes]

  sudo ./scripts/install-server.sh finalize \
    [--bundle-dir /opt/meovv-mail] [--yes]

  sudo ./scripts/install-server.sh status \
    [--bundle-dir /opt/meovv-mail]

Commands:
  install   Install Docker, Nginx, and Certbot; initialize the bundle; obtain
            TLS; install proxy/renewal configuration; and start the appliance.
  finalize  After completing the browser wizard and creating a permanent
            administrator, register TLS with Stalwart and remove recovery access.
  status    Show service, certificate, and local endpoint status without changes.

This script does not modify PTR records, provider firewalls, SSH, or UFW.
Interactive installs can optionally configure supported Cloudflare DNS records;
DNS is unchanged when that option is declined.
For unattended DNS setup, use --configure-dns and provide the token through
CLOUDFLARE_API_TOKEN; the token is never written to disk.
EOF
}

parse_arguments() {
    [[ $# -gt 0 ]] || { usage; exit 2; }
    COMMAND="$1"
    shift

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --hostname)
                [[ $# -ge 2 ]] || die "--hostname requires a value"
                MAIL_HOSTNAME="$2"
                shift 2
                ;;
            --email)
                [[ $# -ge 2 ]] || die "--email requires a value"
                ADMIN_EMAIL="$2"
                shift 2
                ;;
            --bundle-dir)
                [[ $# -ge 2 ]] || die "--bundle-dir requires a value"
                BUNDLE_DIR="$2"
                shift 2
                ;;
            --configure-dns)
                CONFIGURE_DNS=true
                shift
                ;;
            --dns-zone)
                [[ $# -ge 2 ]] || die "--dns-zone requires a value"
                DNS_ZONE="$2"
                shift 2
                ;;
            --public-ipv4)
                [[ $# -ge 2 ]] || die "--public-ipv4 requires a value"
                PUBLIC_IPV4="$2"
                shift 2
                ;;
            --public-ipv6)
                [[ $# -ge 2 ]] || die "--public-ipv6 requires a value"
                PUBLIC_IPV6="$2"
                shift 2
                ;;
            --yes|-y)
                ASSUME_YES=true
                shift
                ;;
            --help|-h)
                usage
                exit 0
                ;;
            *)
                die "unknown argument: $1"
                ;;
        esac
    done

    case "$COMMAND" in
        install|finalize|status) ;;
        *) die "unknown command: $COMMAND" ;;
    esac

    [[ "$BUNDLE_DIR" = /* ]] || die "--bundle-dir must be an absolute path"
    [[ "$BUNDLE_DIR" != *$'\n'* ]] || die "--bundle-dir contains a newline"
    if [[ "$COMMAND" != "install" ]] && \
       { $CONFIGURE_DNS || [[ -n "$DNS_ZONE$PUBLIC_IPV4$PUBLIC_IPV6" ]]; }; then
        die "DNS options are valid only with the install command"
    fi
}

require_root() {
    [[ ${EUID:-$(id -u)} -eq 0 ]] || die "run this command with sudo or as root"
}

validate_hostname() {
    [[ "$MAIL_HOSTNAME" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$ ]] || \
        die "invalid fully-qualified hostname: $MAIL_HOSTNAME"
    [[ ${#MAIL_HOSTNAME} -le 253 ]] || die "hostname is too long"
    MAIL_HOSTNAME="${MAIL_HOSTNAME,,}"
}

validate_email() {
    [[ "$ADMIN_EMAIL" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || \
        die "invalid email address: $ADMIN_EMAIL"
}

require_bundle() {
    [[ -f "$BUNDLE_DIR/compose.yaml" ]] || die "compose.yaml not found in $BUNDLE_DIR"
    [[ -f "$BUNDLE_DIR/Dockerfile" ]] || die "Dockerfile not found in $BUNDLE_DIR"
    [[ -f "$BUNDLE_DIR/deploy/nginx/meovv-mail.conf.example" ]] || die "Nginx template is missing"
    [[ -x "$BUNDLE_DIR/deploy/certbot/deploy-hook.sh" ]] || die "Certbot hook is missing or not executable"
}

confirm_install() {
    $ASSUME_YES && return
    [[ -t 0 ]] || die "non-interactive use requires --yes"

    cat <<EOF

This will install or update Docker Engine, Nginx, and Certbot, write:
  $NGINX_AVAILABLE
  $CERTBOT_HOOK
and start MEOVV Mail from:
  $BUNDLE_DIR

It will not alter unrelated Nginx files, PTR, or firewall rules. You will be
offered optional Cloudflare DNS configuration separately; declining leaves DNS
unchanged.
EOF
    read -r -p "Continue? [y/N] " answer
    [[ "$answer" =~ ^[Yy]$ ]] || die "installation cancelled"
}

detect_platform() {
    [[ -r /etc/os-release ]] || die "/etc/os-release is unavailable"
    # This is a trusted operating-system file.
    # shellcheck disable=SC1091
    source /etc/os-release

    case "${ID:-}" in
        ubuntu)
            case "${VERSION_ID:-}" in
                24.04|26.04) ;;
                *) die "supported Ubuntu releases are 24.04 and 26.04; found ${VERSION_ID:-unknown}" ;;
            esac
            ;;
        debian)
            [[ "${VERSION_ID:-}" == "13" ]] || die "Debian 13 is required; found ${VERSION_ID:-unknown}"
            ;;
        *)
            die "supported systems are Ubuntu 24.04/26.04 and Debian 13; found ${ID:-unknown}"
            ;;
    esac

    OS_ID="$ID"
    OS_CODENAME="${VERSION_CODENAME:-}"
    [[ -n "$OS_CODENAME" ]] || die "the operating-system codename is unavailable"
    readonly OS_ID OS_CODENAME
}

install_base_packages() {
    log "Installing host packages"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends \
        ca-certificates \
        certbot \
        curl \
        dnsutils \
        gnupg \
        iproute2 \
        jq \
        nginx \
        openssl \
        python3-certbot-nginx
}

is_ipv4() {
    local address="$1" octet
    local -a octets=()
    [[ "$address" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
    local old_ifs="$IFS"
    IFS=.
    read -r -a octets <<< "$address"
    IFS="$old_ifs"
    for octet in "${octets[@]}"; do
        (( 10#$octet <= 255 )) || return 1
    done
}

is_ipv6() {
    [[ "$1" == *:* && "$1" =~ ^[0-9A-Fa-f:]+$ ]]
}

validate_dns_inputs() {
    [[ "$DNS_ZONE" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$ ]] || \
        die "invalid Cloudflare zone name: $DNS_ZONE"
    DNS_ZONE="${DNS_ZONE,,}"
    [[ "$MAIL_HOSTNAME" == "$DNS_ZONE" || "$MAIL_HOSTNAME" == *."$DNS_ZONE" ]] || \
        die "$MAIL_HOSTNAME is not inside the Cloudflare zone $DNS_ZONE"
    [[ -z "$PUBLIC_IPV4" ]] || is_ipv4 "$PUBLIC_IPV4" || die "invalid public IPv4 address: $PUBLIC_IPV4"
    [[ -z "$PUBLIC_IPV6" ]] || is_ipv6 "$PUBLIC_IPV6" || die "invalid public IPv6 address: $PUBLIC_IPV6"
    [[ -n "$PUBLIC_IPV4$PUBLIC_IPV6" ]] || die "automatic DNS setup requires a public IPv4 and/or IPv6 address"
}

discover_public_address() {
    local family="$1"
    curl "-$family" --fail --silent --show-error --max-time 8 \
        https://1.1.1.1/cdn-cgi/trace 2>/dev/null | sed -n 's/^ip=//p' | head -n 1
}

cloudflare_api() {
    local method="$1" path="$2" data="${3:-}"
    local arguments=(
        --fail-with-body
        --silent
        --show-error
        --request "$method"
        "https://api.cloudflare.com/client/v4$path"
    )
    if [[ -n "$data" ]]; then
        arguments+=(--data "$data")
    fi

    # Read the authorization header from stdin so the token does not appear in
    # the curl process arguments or get written to a temporary file.
    printf 'header = "Authorization: Bearer %s"\nheader = "Content-Type: application/json"\n' \
        "$CF_API_TOKEN" | curl --config - "${arguments[@]}"
}

require_cloudflare_success() {
    local response="$1" action="$2"
    if ! jq -e '.success == true' >/dev/null 2>&1 <<< "$response"; then
        local detail
        detail="$(jq -r '[.errors[]? | (.code|tostring) + ": " + .message] | join("; ")' <<< "$response" 2>/dev/null || true)"
        die "Cloudflare could not $action${detail:+: $detail}"
    fi
}

cloudflare_records() {
    local type="$1" name="$2" response
    response="$(cloudflare_api GET "/zones/$CF_ZONE_ID/dns_records?type=$type&name=$name&per_page=100")" || \
        die "Cloudflare DNS lookup failed for $name"
    require_cloudflare_success "$response" "read $type records for $name"
    printf '%s' "$response"
}

write_cloudflare_record() {
    local action="$1" record_id="$2" body="$3" response
    if [[ "$action" == "create" ]]; then
        response="$(cloudflare_api POST "/zones/$CF_ZONE_ID/dns_records" "$body")" || \
            die "Cloudflare DNS record creation failed"
    else
        response="$(cloudflare_api PUT "/zones/$CF_ZONE_ID/dns_records/$record_id" "$body")" || \
            die "Cloudflare DNS record update failed"
    fi
    require_cloudflare_success "$response" "$action a DNS record"
}

ensure_cloudflare_record() {
    local type="$1" name="$2" content="$3" priority="${4:-}" response count exact_id record_id body
    response="$(cloudflare_records "$type" "$name")"
    count="$(jq '.result | length' <<< "$response")"
    if [[ "$type" == "MX" ]]; then
        exact_id="$(jq -r --arg content "$content" --argjson priority "$priority" \
            '.result[] | select(.content == $content and .priority == $priority) | .id' <<< "$response" | head -n 1)"
        body="$(jq -cn --arg type "$type" --arg name "$name" --arg content "$content" \
            --argjson priority "$priority" '{type:$type,name:$name,content:$content,priority:$priority,ttl:1}')"
    else
        exact_id="$(jq -r --arg content "$content" \
            '.result[] | select(.content == $content and .proxied == false) | .id' <<< "$response" | head -n 1)"
        body="$(jq -cn --arg type "$type" --arg name "$name" --arg content "$content" \
            '{type:$type,name:$name,content:$content,ttl:1,proxied:false}')"
    fi

    if (( count > 1 )); then
        die "multiple Cloudflare $type records exist for $name; review them manually instead of replacing them"
    fi
    if [[ -n "$exact_id" ]]; then
        log "Cloudflare $type record is already correct: $name"
        return
    fi
    if (( count == 1 )); then
        record_id="$(jq -r '.result[0].id' <<< "$response")"
        write_cloudflare_record update "$record_id" "$body"
        log "Updated Cloudflare $type record: $name"
    else
        write_cloudflare_record create "" "$body"
        log "Created Cloudflare $type record: $name"
    fi
}

ensure_cloudflare_txt_default() {
    local name="$1" prefix="$2" content="$3" response exact existing body
    response="$(cloudflare_records TXT "$name")"
    exact="$(jq -r --arg content "$content" '.result[] | select(.content == $content) | .id' \
        <<< "$response" | head -n 1)"
    if [[ -n "$exact" ]]; then
        log "Cloudflare TXT record is already correct: $name"
        return
    fi
    existing="$(jq -r --arg prefix "$prefix" '.result[] | select(.content | startswith($prefix)) | .content' \
        <<< "$response" | head -n 1)"
    if [[ -n "$existing" ]]; then
        warn "Keeping the existing TXT policy for $name: $existing"
        return
    fi
    body="$(jq -cn --arg name "$name" --arg content "$content" \
        '{type:"TXT",name:$name,content:$content,ttl:1}')"
    write_cloudflare_record create "" "$body"
    log "Created conservative Cloudflare TXT policy: $name"
}

wait_for_cloudflare_dns() {
    log "Waiting for the mail hostname to appear in public DNS"
    local attempt answer
    for attempt in {1..20}; do
        if [[ -n "$PUBLIC_IPV4" ]]; then
            answer="$(dig +short A "$MAIL_HOSTNAME" @1.1.1.1 | grep -Fx "$PUBLIC_IPV4" || true)"
        else
            answer="$(dig +short AAAA "$MAIL_HOSTNAME" @1.1.1.1 | grep -Fxi "$PUBLIC_IPV6" || true)"
        fi
        [[ -n "$answer" ]] && return
        sleep 3
    done
    die "Cloudflare accepted the DNS records, but $MAIL_HOSTNAME is not publicly visible yet; wait briefly and rerun the installer"
}

configure_cloudflare_dns() {
    DNS_ZONE="${DNS_ZONE:-${ADMIN_EMAIL##*@}}"
    local detected_ipv4 detected_ipv6 answer response zone_count
    detected_ipv4="${PUBLIC_IPV4:-$(discover_public_address 4 || true)}"
    detected_ipv6="$(discover_public_address 6 || true)"

    if [[ -t 0 ]] && ! $ASSUME_YES; then
        read -r -p "Cloudflare zone [$DNS_ZONE]: " answer
        DNS_ZONE="${answer:-$DNS_ZONE}"
        read -r -p "Public IPv4 [$detected_ipv4]: " answer
        PUBLIC_IPV4="${answer:-$detected_ipv4}"
        if [[ -z "$PUBLIC_IPV6" ]]; then
            read -r -p "Public IPv6 (optional; detected $detected_ipv6, blank to skip): " PUBLIC_IPV6
        fi
    else
        PUBLIC_IPV4="$detected_ipv4"
    fi
    validate_dns_inputs

    CF_API_TOKEN="${CLOUDFLARE_API_TOKEN:-}"
    unset CLOUDFLARE_API_TOKEN || true
    if [[ -z "$CF_API_TOKEN" ]] && [[ -t 0 ]]; then
        read -r -s -p "Cloudflare API token (Zone:Read and DNS:Edit): " CF_API_TOKEN
        printf '\n'
    fi
    [[ -n "$CF_API_TOKEN" ]] || \
        die "Cloudflare DNS setup requires a token through the hidden prompt or CLOUDFLARE_API_TOKEN"
    [[ "$CF_API_TOKEN" != *$'\n'* && "$CF_API_TOKEN" != *'"'* && "$CF_API_TOKEN" != *'\\'* ]] || \
        die "the Cloudflare API token contains unsupported characters"

    response="$(cloudflare_api GET /user/tokens/verify)" || die "Cloudflare token verification request failed"
    require_cloudflare_success "$response" "verify the API token"
    response="$(cloudflare_api GET "/zones?name=$DNS_ZONE&status=active&per_page=2")" || \
        die "Cloudflare zone lookup failed"
    require_cloudflare_success "$response" "look up zone $DNS_ZONE"
    zone_count="$(jq '.result | length' <<< "$response")"
    [[ "$zone_count" -eq 1 ]] || die "expected one active Cloudflare zone named $DNS_ZONE; found $zone_count"
    CF_ZONE_ID="$(jq -r '.result[0].id' <<< "$response")"

    cat <<EOF

Cloudflare DNS changes for $DNS_ZONE:
  A     $MAIL_HOSTNAME -> ${PUBLIC_IPV4:-skip}
  AAAA  $MAIL_HOSTNAME -> ${PUBLIC_IPV6:-skip}
  MX    $DNS_ZONE -> $MAIL_HOSTNAME (priority 10)
  CNAME autoconfig.$DNS_ZONE -> $MAIL_HOSTNAME
  CNAME autodiscover.$DNS_ZONE -> $MAIL_HOSTNAME
  TXT   SPF and DMARC conservative defaults, only when no policy exists

DKIM is generated after domain setup. PTR must be configured at the server/IP
provider. Existing SPF or DMARC policies will never be overwritten.
EOF
    if [[ -t 0 ]] && ! $ASSUME_YES; then
        read -r -p "Apply these DNS changes? [y/N] " answer
        [[ "$answer" =~ ^[Yy]$ ]] || die "DNS configuration cancelled"
    fi

    [[ -z "$PUBLIC_IPV4" ]] || ensure_cloudflare_record A "$MAIL_HOSTNAME" "$PUBLIC_IPV4"
    [[ -z "$PUBLIC_IPV6" ]] || ensure_cloudflare_record AAAA "$MAIL_HOSTNAME" "$PUBLIC_IPV6"
    ensure_cloudflare_record MX "$DNS_ZONE" "$MAIL_HOSTNAME" 10
    ensure_cloudflare_record CNAME "autoconfig.$DNS_ZONE" "$MAIL_HOSTNAME"
    ensure_cloudflare_record CNAME "autodiscover.$DNS_ZONE" "$MAIL_HOSTNAME"
    ensure_cloudflare_txt_default "$DNS_ZONE" "v=spf1 " "v=spf1 mx ~all"
    ensure_cloudflare_txt_default "_dmarc.$DNS_ZONE" "v=DMARC1;" \
        "v=DMARC1; p=none; rua=mailto:postmaster@$DNS_ZONE"
    wait_for_cloudflare_dns
    CF_API_TOKEN=""
}

configure_dns_if_requested() {
    local answer
    if ! $CONFIGURE_DNS && [[ -t 0 ]] && ! $ASSUME_YES; then
        read -r -p "Configure the required DNS records with Cloudflare now? [y/N] " answer
        [[ "$answer" =~ ^[Yy]$ ]] && CONFIGURE_DNS=true
    fi
    $CONFIGURE_DNS || return
    configure_cloudflare_dns
}

package_is_installed() {
    dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -Fq 'install ok installed'
}

install_compose_for_existing_docker() {
    local candidates=()
    if package_is_installed docker-ce || package_is_installed docker-ce-cli; then
        candidates=(docker-compose-plugin docker-compose-v2)
    else
        candidates=(docker-compose-v2 docker-compose-plugin)
    fi

    local package
    for package in "${candidates[@]}"; do
        if apt-cache show "$package" >/dev/null 2>&1; then
            log "Installing $package for the existing Docker Engine"
            if apt-get install -y --no-install-recommends "$package" && \
               docker compose version >/dev/null 2>&1; then
                systemctl enable --now docker
                docker compose version
                return
            fi
        fi
    done

    die "Docker is installed, but no compatible Compose v2 package is available from the configured APT repositories"
}

install_docker() {
    if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
        log "Docker Engine and Compose are already installed"
        systemctl enable --now docker
        return
    fi

    if command -v docker >/dev/null 2>&1; then
        install_compose_for_existing_docker
        return
    fi

    local conflicting_packages=() package
    for package in docker.io docker-compose docker-compose-v2 podman-docker containerd runc; do
        if package_is_installed "$package"; then
            conflicting_packages+=("$package")
        fi
    done
    if (( ${#conflicting_packages[@]} > 0 )); then
        die "conflicting container packages are installed (${conflicting_packages[*]}); review them manually before installing Docker CE"
    fi

    log "Installing Docker Engine from Docker's official repository"
    install -m 0755 -d /etc/apt/keyrings
    local docker_key
    docker_key="$(mktemp /tmp/meovv-docker.XXXXXX.asc)"
    TEMP_FILES+=("$docker_key")
    curl -fsSL "https://download.docker.com/linux/$OS_ID/gpg" -o "$docker_key"
    gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg "$docker_key"
    chmod a+r /etc/apt/keyrings/docker.gpg

    local architecture
    architecture="$(dpkg --print-architecture)"
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/%s %s stable\n' \
        "$architecture" "$OS_ID" "$OS_CODENAME" \
        > /etc/apt/sources.list.d/docker.list

    apt-get update
    apt-get install -y --no-install-recommends \
        containerd.io \
        docker-buildx-plugin \
        docker-ce \
        docker-ce-cli \
        docker-compose-plugin
    systemctl enable --now docker
    docker compose version >/dev/null
}

assert_hostname_is_available() {
    local conflict=""
    if [[ -d /etc/nginx/sites-enabled ]]; then
        while IFS= read -r candidate; do
            [[ -e "$candidate" ]] || continue
            [[ "$(readlink -f "$candidate")" == "$(readlink -f "$NGINX_AVAILABLE" 2>/dev/null || printf '%s' "$NGINX_AVAILABLE")" ]] && continue
            if grep -Eq "^[[:space:]]*server_name[[:space:]][^;]*\b${MAIL_HOSTNAME//./\.}\b" "$candidate"; then
                conflict="$candidate"
                break
            fi
        done < <(find /etc/nginx/sites-enabled -maxdepth 1 \( -type f -o -type l \) 2>/dev/null)
    fi
    [[ -z "$conflict" ]] || die "$MAIL_HOSTNAME is already configured by $conflict; merge or disable that site manually"

    if [[ -e "$NGINX_AVAILABLE" ]] && ! grep -Fq "$MANAGED_MARKER" "$NGINX_AVAILABLE"; then
        die "$NGINX_AVAILABLE exists but is not managed by this installer"
    fi
}

install_challenge_site() {
    install -d -m 0755 "$CERTBOT_WEBROOT"
    install -d -m 0755 /etc/nginx/sites-available /etc/nginx/sites-enabled

    if [[ -r "/etc/letsencrypt/live/$MAIL_HOSTNAME/fullchain.pem" ]] && \
       [[ -r "/etc/letsencrypt/live/$MAIL_HOSTNAME/privkey.pem" ]] && \
       [[ -f "$NGINX_AVAILABLE" ]] && grep -Fq "listen 443 ssl" "$NGINX_AVAILABLE"; then
        log "Keeping the existing managed HTTPS site during certificate renewal"
        ln -sfn "$NGINX_AVAILABLE" "$NGINX_ENABLED"
        nginx -t
        systemctl enable --now nginx
        systemctl reload nginx
        return
    fi

    log "Preparing the initial ACME challenge site"
    cat > "$NGINX_AVAILABLE" <<EOF
# $MANAGED_MARKER
server {
    listen 80;
    listen [::]:80;
    server_name $MAIL_HOSTNAME;

    location ^~ /.well-known/acme-challenge/ {
        root $CERTBOT_WEBROOT;
        default_type text/plain;
    }

    location / {
        return 404;
    }
}
EOF
    ln -sfn "$NGINX_AVAILABLE" "$NGINX_ENABLED"
    nginx -t
    systemctl enable --now nginx
    systemctl reload nginx
}

obtain_certificate() {
    log "Obtaining or reusing the Let's Encrypt certificate"
    certbot certonly \
        --webroot \
        --webroot-path "$CERTBOT_WEBROOT" \
        --cert-name "$MAIL_HOSTNAME" \
        --domain "$MAIL_HOSTNAME" \
        --email "$ADMIN_EMAIL" \
        --agree-tos \
        --no-eff-email \
        --non-interactive \
        --keep-until-expiring

    [[ -r "/etc/letsencrypt/live/$MAIL_HOSTNAME/fullchain.pem" ]] || die "Certbot did not create the expected certificate lineage"
    [[ -r "/etc/letsencrypt/live/$MAIL_HOSTNAME/privkey.pem" ]] || die "Certbot did not create the expected private key"
}

install_final_nginx_site() {
    log "Installing the HTTPS Nginx route split"
    sed "s/__MAIL_HOSTNAME__/$MAIL_HOSTNAME/g" \
        "$BUNDLE_DIR/deploy/nginx/meovv-mail.conf.example" \
        > "$NGINX_AVAILABLE"
    ln -sfn "$NGINX_AVAILABLE" "$NGINX_ENABLED"
    nginx -t
    systemctl reload nginx
}

compose() {
    docker compose --project-directory "$BUNDLE_DIR" -f "$BUNDLE_DIR/compose.yaml" "$@"
}

build_and_install_mailctl() {
    log "Building the pinned MEOVV application image"
    MAIL_HOSTNAME="$MAIL_HOSTNAME" MEOVV_VERSION="$MEOVV_RELEASE" compose build meovv

    local image_ref temporary_binary
    image_ref="meovv-mail:$MEOVV_RELEASE"
    docker image inspect "$image_ref" >/dev/null 2>&1 || \
        die "the expected MEOVV image $image_ref was not created"

    TEMP_CONTAINER="$(docker create "$image_ref")"
    temporary_binary="$(mktemp /tmp/meovv-mailctl.XXXXXX)"
    TEMP_FILES+=("$temporary_binary")
    docker cp "$TEMP_CONTAINER:/usr/local/bin/mailctl" "$temporary_binary"
    install -m 0755 "$temporary_binary" "$MAILCTL_BIN"
    rm -f "$temporary_binary"
    docker rm "$TEMP_CONTAINER" >/dev/null
    TEMP_CONTAINER=""
}

initialize_bundle() {
    if [[ -f "$BUNDLE_DIR/.env" ]]; then
        local configured_hostname
        configured_hostname="$(sed -n 's/^MAIL_HOSTNAME=//p' "$BUNDLE_DIR/.env" | tail -n 1)"
        [[ "$configured_hostname" == "$MAIL_HOSTNAME" ]] || \
            die "$BUNDLE_DIR/.env belongs to $configured_hostname, not $MAIL_HOSTNAME"
        log "The appliance is already initialized"
    else
        log "Initializing appliance secrets"
        "$MAILCTL_BIN" init --directory "$BUNDLE_DIR" --hostname "$MAIL_HOSTNAME"
    fi

    # The MEOVV image runs with group 2001. Keep secrets unreadable to other
    # host users while allowing the application process to read its inputs.
    chown -R root:2001 "$BUNDLE_DIR/secrets"
    find "$BUNDLE_DIR/secrets" -type d -exec chmod 0750 {} +
    find "$BUNDLE_DIR/secrets" -type f -exec chmod 0640 {} +
    chmod 0600 "$BUNDLE_DIR/.env"
}

copy_certificate_to_stalwart() {
    log "Copying the Certbot certificate into the protected appliance directory"
    MEOVV_BUNDLE_DIR="$BUNDLE_DIR" \
    RENEWED_LINEAGE="/etc/letsencrypt/live/$MAIL_HOSTNAME" \
        "$BUNDLE_DIR/deploy/certbot/deploy-hook.sh"

    chown root:2001 "$BUNDLE_DIR/secrets/tls/fullchain.pem" "$BUNDLE_DIR/secrets/tls/privkey.pem"
    chmod 0644 "$BUNDLE_DIR/secrets/tls/fullchain.pem"
    chmod 0640 "$BUNDLE_DIR/secrets/tls/privkey.pem"
}

install_certbot_hook() {
    log "Installing the Certbot deploy hook"
    install -d -m 0755 "$(dirname "$CERTBOT_HOOK")"
    local escaped_bundle escaped_hook
    printf -v escaped_bundle '%q' "$BUNDLE_DIR"
    printf -v escaped_hook '%q' "$BUNDLE_DIR/deploy/certbot/deploy-hook.sh"
    cat > "$CERTBOT_HOOK" <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail
export MEOVV_BUNDLE_DIR=$escaped_bundle
$escaped_hook
nginx -t
systemctl reload nginx
EOF
    chmod 0755 "$CERTBOT_HOOK"
    systemctl enable --now certbot.timer
}

start_appliance() {
    log "Starting MEOVV Mail"
    compose up -d --build --remove-orphans

    local attempt
    for attempt in {1..30}; do
        if curl --fail --silent --show-error --max-time 3 http://127.0.0.1:8080/health/live >/dev/null; then
            return
        fi
        sleep 2
    done
    compose ps
    die "MEOVV did not become live within 60 seconds; inspect 'docker compose logs'"
}

install_command() {
    require_root
    [[ -n "$MAIL_HOSTNAME" ]] || die "install requires --hostname"
    [[ -n "$ADMIN_EMAIL" ]] || die "install requires --email"
    validate_hostname
    validate_email
    require_bundle
    confirm_install
    detect_platform
    install_base_packages
    assert_hostname_is_available
    install_docker
    build_and_install_mailctl
    initialize_bundle
    configure_dns_if_requested
    install_challenge_site
    obtain_certificate
    copy_certificate_to_stalwart
    install_certbot_hook
    install_final_nginx_site
    start_appliance

    cat <<EOF

MEOVV Mail prerequisites and services are installed.

Next:
  1. Open https://$MAIL_HOSTNAME and complete the setup wizard.
  2. Create and verify a permanent Stalwart administrator.
  3. Run:
       sudo $BUNDLE_DIR/scripts/install-server.sh finalize --bundle-dir $BUNDLE_DIR

Required provider-side work that this script cannot perform:
  - Permit inbound TCP 25, 80, 443, 465, 587, and 993.
  - Permit outbound TCP 25 or configure a smart host.
  - Configure A/AAAA, MX, PTR, SPF, DKIM, and DMARC.

The one-time setup token is stored at:
  $BUNDLE_DIR/secrets/bootstrap_token
EOF
}

finalize_command() {
    require_root
    require_bundle
    [[ -x "$MAILCTL_BIN" ]] || die "$MAILCTL_BIN is missing; run install first"
    [[ -f "$BUNDLE_DIR/.env" ]] || die "$BUNDLE_DIR is not initialized"

    if ! $ASSUME_YES; then
        [[ -t 0 ]] || die "non-interactive use requires --yes"
        warn "Finalize removes the temporary Stalwart recovery administrator."
        read -r -p "Have you completed the wizard and verified a permanent administrator? [y/N] " answer
        [[ "$answer" =~ ^[Yy]$ ]] || die "finalization cancelled"
    fi

    log "Registering Certbot TLS with Stalwart"
    "$MAILCTL_BIN" configure-tls --directory "$BUNDLE_DIR"
    log "Removing temporary recovery access"
    "$MAILCTL_BIN" harden --directory "$BUNDLE_DIR"

    nginx -t
    systemctl reload nginx
    log "Final appliance status"
    compose ps

    cat <<EOF

Finalization complete. Recovery access is disabled.
Run the following after DNS and firewall configuration is complete:
  sudo $MAILCTL_BIN doctor --directory $BUNDLE_DIR
  sudo certbot renew --dry-run
EOF
}

status_command() {
    require_root
    require_bundle
    log "Docker services"
    compose ps || true
    log "Nginx"
    systemctl --no-pager --full status nginx || true
    log "Certbot lineages"
    certbot certificates || true
    log "Loopback endpoints"
    curl --silent --show-error --max-time 3 http://127.0.0.1:8080/health/live || true
    printf '\n'
    curl --silent --show-error --max-time 3 http://127.0.0.1:8081/.well-known/jmap || true
    printf '\n'
}

main() {
    parse_arguments "$@"
    case "$COMMAND" in
        install) install_command ;;
        finalize) finalize_command ;;
        status) status_command ;;
    esac
}

main "$@"
