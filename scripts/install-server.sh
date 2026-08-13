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

COMMAND=""
MAIL_HOSTNAME=""
ADMIN_EMAIL=""
BUNDLE_DIR="$REPOSITORY_DIR"
ASSUME_YES=false
TEMP_CONTAINER=""
TEMP_FILES=()

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

This script does not modify DNS, PTR records, provider firewalls, SSH, or UFW.
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

It will not alter unrelated Nginx files, DNS, PTR, or firewall rules.
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
        nginx \
        openssl \
        python3-certbot-nginx
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
    MAIL_HOSTNAME="$MAIL_HOSTNAME" MEOVV_VERSION=0.1.0 compose build meovv

    local image_id temporary_binary
    image_id="$(MAIL_HOSTNAME="$MAIL_HOSTNAME" MEOVV_VERSION=0.1.0 compose images -q meovv)"
    [[ -n "$image_id" ]] || die "the MEOVV image was not created"

    TEMP_CONTAINER="$(docker create "$image_id")"
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
