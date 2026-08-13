#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPOSITORY_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"
readonly PROJECT_NAME="meovv-mail"
readonly NGINX_AVAILABLE="/etc/nginx/sites-available/meovv-mail"
readonly NGINX_ENABLED="/etc/nginx/sites-enabled/meovv-mail"
readonly NGINX_CONFD="/etc/nginx/conf.d/meovv-mail.conf"
readonly CERTBOT_WEBROOT="/var/www/certbot"
readonly CERTBOT_HOOK="/etc/letsencrypt/renewal-hooks/deploy/meovv-mail"
readonly MAILCTL_BIN="/usr/local/bin/mailctl"
readonly -a MEOVV_VOLUMES=(
    meovv-mail-app-data
    meovv-mail-stalwart-config
    meovv-mail-stalwart-data
)
readonly -a OPTIONAL_APT_PACKAGES=(
    certbot
    containerd.io
    docker-buildx-plugin
    docker-ce
    docker-ce-cli
    docker-compose-plugin
    docker-compose-v2
    nginx
    python3-certbot-nginx
)

BUNDLE_DIR="$REPOSITORY_DIR"
MAIL_HOSTNAME=""
ASSUME_YES=false
PURGE_PACKAGES=false
REMOVE_BUNDLE=false
KEEP_CERTIFICATE=false
FAILURES=0

unexpected_error() {
    local status=$?
    printf '\n\033[1;31mERROR:\033[0m uninstall stopped unexpectedly near line %s (exit %d).\n' \
        "${BASH_LINENO[0]:-unknown}" "$status" >&2
    exit "$status"
}
trap unexpected_error ERR

log() {
    printf '\n\033[1;35m==>\033[0m %s\n' "$*"
}

warn() {
    printf '\033[1;33mWARNING:\033[0m %s\n' "$*" >&2
}

record_failure() {
    warn "$*"
    FAILURES=$((FAILURES + 1))
}

usage() {
    cat <<'EOF'
Permanently uninstall MEOVV Mail from one server.

Usage:
  sudo ./scripts/uninstall-server.sh \
    [--bundle-dir /opt/meovv-mail] [--hostname mail.example.com] \
    [--purge-packages] [--remove-bundle] [--keep-certificate] [--yes]

The uninstaller always removes:
  - MEOVV and Stalwart containers, network, images, and named volumes
  - all mailbox data, application data, local backups, secrets, and .env
  - MEOVV Nginx sites, Certbot deploy hook, and /usr/local/bin/mailctl
  - the hostname's Certbot certificate (unless --keep-certificate is used)

Options:
  --purge-packages    Also purge installed Docker CE/Compose, Nginx, and
                      Certbot packages, then remove the Docker APT source.
                      This can affect other services using those packages.
  --remove-bundle     Also delete the entire repository/bundle directory.
  --keep-certificate  Keep the Certbot certificate and renewal configuration.
  --yes               Non-interactive confirmation. Package and bundle removal
                      still require their explicit flags.

Cloudflare/public DNS, PTR records, firewall rules, unrelated Nginx sites,
unrelated Docker resources, and unrelated certificates are never changed.
EOF
}

parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --bundle-dir)
                [[ $# -ge 2 ]] || { printf 'ERROR: --bundle-dir requires a value\n' >&2; exit 2; }
                BUNDLE_DIR="$2"
                shift 2
                ;;
            --hostname)
                [[ $# -ge 2 ]] || { printf 'ERROR: --hostname requires a value\n' >&2; exit 2; }
                MAIL_HOSTNAME="$2"
                shift 2
                ;;
            --purge-packages)
                PURGE_PACKAGES=true
                shift
                ;;
            --remove-bundle)
                REMOVE_BUNDLE=true
                shift
                ;;
            --keep-certificate)
                KEEP_CERTIFICATE=true
                shift
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
                printf 'ERROR: unknown argument: %s\n' "$1" >&2
                exit 2
                ;;
        esac
    done
}

require_root() {
    [[ ${EUID:-$(id -u)} -eq 0 ]] || {
        printf 'ERROR: run this command with sudo or as root\n' >&2
        exit 1
    }
}

resolve_installation() {
    [[ "$BUNDLE_DIR" = /* ]] || {
        printf 'ERROR: --bundle-dir must be an absolute path\n' >&2
        exit 2
    }
    [[ "$BUNDLE_DIR" != *$'\n'* ]] || {
        printf 'ERROR: --bundle-dir contains a newline\n' >&2
        exit 2
    }
    [[ -d "$BUNDLE_DIR" ]] || {
        printf 'ERROR: bundle directory does not exist: %s\n' "$BUNDLE_DIR" >&2
        exit 2
    }
    BUNDLE_DIR="$(cd -- "$BUNDLE_DIR" && pwd -P)"
    [[ -f "$BUNDLE_DIR/compose.yaml" ]] || {
        printf 'ERROR: compose.yaml not found in %s\n' "$BUNDLE_DIR" >&2
        exit 2
    }

    if [[ -z "$MAIL_HOSTNAME" && -f "$BUNDLE_DIR/.env" ]]; then
        MAIL_HOSTNAME="$(sed -n 's/^MAIL_HOSTNAME=//p' "$BUNDLE_DIR/.env" | tail -n 1)"
    fi
    if [[ -n "$MAIL_HOSTNAME" ]]; then
        MAIL_HOSTNAME="${MAIL_HOSTNAME,,}"
        [[ "$MAIL_HOSTNAME" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$ ]] || {
            printf 'ERROR: invalid fully-qualified hostname: %s\n' "$MAIL_HOSTNAME" >&2
            exit 2
        }
    elif ! $KEEP_CERTIFICATE; then
        printf 'ERROR: hostname is unavailable; pass --hostname or use --keep-certificate\n' >&2
        exit 2
    fi
}

choose_optional_cleanup() {
    $ASSUME_YES && return
    [[ -t 0 ]] || {
        printf 'ERROR: non-interactive use requires --yes\n' >&2
        exit 1
    }

    if ! $PURGE_PACKAGES; then
        printf '\nDocker, Nginx, and Certbot may be shared with other services.\n'
        read -r -p "Also purge their APT packages and Docker APT source? [y/N] " answer
        if [[ "$answer" =~ ^[Yy]$ ]]; then
            PURGE_PACKAGES=true
        fi
    fi
    if ! $REMOVE_BUNDLE; then
        read -r -p "Also delete the repository directory $BUNDLE_DIR? [y/N] " answer
        if [[ "$answer" =~ ^[Yy]$ ]]; then
            REMOVE_BUNDLE=true
        fi
    fi
    return 0
}

confirm_uninstall() {
    cat <<EOF

MEOVV Mail will be permanently removed.

  Hostname:             ${MAIL_HOSTNAME:-unknown}
  Bundle:               $BUNDLE_DIR
  Mailbox/app volumes:  meovv-mail-app-data, meovv-mail-stalwart-config,
                        meovv-mail-stalwart-data
  Certbot certificate:  $([[ "$KEEP_CERTIFICATE" == true ]] && printf 'keep' || printf 'delete')
  Host packages:        $([[ "$PURGE_PACKAGES" == true ]] && printf 'purge' || printf 'keep')
  Repository checkout:  $([[ "$REMOVE_BUNDLE" == true ]] && printf 'delete' || printf 'keep')

All messages, accounts, application state, secrets, and local MEOVV backups
will be irreversibly deleted. Public DNS and provider settings are unchanged.
EOF

    $ASSUME_YES && return
    local expected answer
    expected="${MAIL_HOSTNAME:-UNINSTALL}"
    read -r -p "Type '$expected' to permanently uninstall: " answer
    [[ "$answer" == "$expected" ]] || {
        printf 'Uninstall cancelled.\n'
        exit 1
    }
}

remove_docker_resources() {
    command -v docker >/dev/null 2>&1 || {
        warn "Docker is not installed; skipping container cleanup"
        return
    }
    if ! docker info >/dev/null 2>&1; then
        systemctl start docker >/dev/null 2>&1 || {
            record_failure "Docker is installed but its daemon could not be started; MEOVV Docker data remains"
            return
        }
    fi

    log "Removing MEOVV containers, network, images, and volumes"
    local -a image_ids=() container_ids=() network_ids=()
    local image_ref image_id

    if docker compose version >/dev/null 2>&1 && [[ -f "$BUNDLE_DIR/compose.yaml" ]]; then
        while IFS= read -r image_ref; do
            [[ -n "$image_ref" ]] || continue
            image_id="$(docker image inspect --format '{{.Id}}' "$image_ref" 2>/dev/null || true)"
            [[ -n "$image_id" ]] && image_ids+=("$image_id")
        done < <(
            docker compose --project-directory "$BUNDLE_DIR" \
                -f "$BUNDLE_DIR/compose.yaml" config --images 2>/dev/null
        )
        docker compose --project-directory "$BUNDLE_DIR" \
            -f "$BUNDLE_DIR/compose.yaml" down --volumes --remove-orphans --timeout 30 || \
            record_failure "Docker Compose could not remove every MEOVV resource"
    fi

    mapfile -t container_ids < <(docker ps -aq --filter "label=com.docker.compose.project=$PROJECT_NAME")
    if ((${#container_ids[@]})); then
        docker rm -f "${container_ids[@]}" >/dev/null || \
            record_failure "some MEOVV containers could not be removed"
    fi

    local volume
    for volume in "${MEOVV_VOLUMES[@]}"; do
        if docker volume inspect "$volume" >/dev/null 2>&1; then
            docker volume rm "$volume" >/dev/null || \
                record_failure "Docker volume $volume could not be removed"
        fi
    done

    mapfile -t network_ids < <(docker network ls -q --filter "label=com.docker.compose.project=$PROJECT_NAME")
    if ((${#network_ids[@]})); then
        docker network rm "${network_ids[@]}" >/dev/null || \
            record_failure "some MEOVV Docker networks could not be removed"
    fi

    while IFS= read -r image_id; do
        [[ -n "$image_id" ]] && image_ids+=("$image_id")
    done < <(docker image ls --format '{{.Repository}} {{.ID}}' | awk '$1 == "meovv-mail" {print $2}')
    if ((${#image_ids[@]})); then
        mapfile -t image_ids < <(printf '%s\n' "${image_ids[@]}" | awk 'NF && !seen[$0]++')
        docker image rm "${image_ids[@]}" >/dev/null 2>&1 || \
            record_failure "one or more MEOVV images are still used elsewhere and were retained"
    fi
}

remove_nginx_integration() {
    log "Removing MEOVV Nginx configuration"
    rm -f -- "$NGINX_ENABLED" "$NGINX_CONFD" "$NGINX_AVAILABLE"

    if command -v nginx >/dev/null 2>&1; then
        if nginx -t; then
            systemctl reload nginx || record_failure "Nginx could not be reloaded"
        else
            record_failure "remaining Nginx configuration is invalid; Nginx was not reloaded"
        fi
    fi
}

remove_certbot_integration() {
    log "Removing MEOVV Certbot integration"
    rm -f -- "$CERTBOT_HOOK"

    if ! $KEEP_CERTIFICATE && [[ -n "$MAIL_HOSTNAME" ]]; then
        if command -v certbot >/dev/null 2>&1; then
            if [[ -e "/etc/letsencrypt/renewal/$MAIL_HOSTNAME.conf" || \
                  -e "/etc/letsencrypt/live/$MAIL_HOSTNAME" || \
                  -e "/etc/letsencrypt/archive/$MAIL_HOSTNAME" ]]; then
                certbot delete --cert-name "$MAIL_HOSTNAME" --non-interactive || \
                    record_failure "Certbot certificate $MAIL_HOSTNAME could not be deleted"
            fi
        else
            record_failure "Certbot is unavailable; certificate $MAIL_HOSTNAME was not deleted"
        fi
    fi

    rmdir --ignore-fail-on-non-empty "$CERTBOT_WEBROOT/.well-known/acme-challenge" 2>/dev/null || true
    rmdir --ignore-fail-on-non-empty "$CERTBOT_WEBROOT/.well-known" 2>/dev/null || true
    rmdir --ignore-fail-on-non-empty "$CERTBOT_WEBROOT" 2>/dev/null || true
}

remove_local_state() {
    log "Removing MEOVV host utility, secrets, state, and local backups"
    rm -f -- "$MAILCTL_BIN"
    rm -rf -- \
        "$BUNDLE_DIR/.env" \
        "$BUNDLE_DIR/secrets" \
        "$BUNDLE_DIR/backups" \
        "$BUNDLE_DIR/web-dist"
}

purge_host_packages() {
    if ! $PURGE_PACKAGES; then
        return 0
    fi
    command -v apt-get >/dev/null 2>&1 || {
        record_failure "APT is unavailable; requested host packages were not purged"
        return
    }

    log "Purging Docker, Nginx, and Certbot packages"
    local package
    local -a installed=()
    for package in "${OPTIONAL_APT_PACKAGES[@]}"; do
        if dpkg-query -W -f='${db:Status-Abbrev}' "$package" 2>/dev/null | grep -q '^ii'; then
            installed+=("$package")
        fi
    done
    if ((${#installed[@]})); then
        DEBIAN_FRONTEND=noninteractive apt-get purge -y "${installed[@]}" || \
            record_failure "some requested APT packages could not be purged"
        DEBIAN_FRONTEND=noninteractive apt-get autoremove --purge -y || \
            record_failure "APT automatic dependency cleanup failed"
    fi

    rm -f -- /etc/apt/sources.list.d/docker.list /etc/apt/keyrings/docker.gpg
    apt-get update || record_failure "APT metadata could not be refreshed after Docker source removal"
}

remove_bundle_checkout() {
    if ! $REMOVE_BUNDLE; then
        return 0
    fi
    case "$BUNDLE_DIR" in
        /|/bin|/boot|/dev|/etc|/home|/opt|/root|/srv|/tmp|/usr|/var)
            record_failure "refusing to delete unsafe bundle path $BUNDLE_DIR"
            return
            ;;
    esac
    if [[ ! -f "$BUNDLE_DIR/compose.yaml" ]] || \
       ! grep -Eq '^name:[[:space:]]+meovv-mail[[:space:]]*$' "$BUNDLE_DIR/compose.yaml"; then
        record_failure "refusing to delete $BUNDLE_DIR because it is not a recognizable MEOVV bundle"
        return
    fi

    log "Removing repository checkout"
    rm -rf -- "$BUNDLE_DIR"
}

main() {
    parse_arguments "$@"
    require_root
    resolve_installation
    choose_optional_cleanup
    confirm_uninstall

    remove_docker_resources
    remove_nginx_integration
    remove_certbot_integration
    remove_local_state
    purge_host_packages
    remove_bundle_checkout

    if ((FAILURES)); then
        printf '\nMEOVV uninstall completed with %d warning(s). Review the messages above.\n' "$FAILURES" >&2
        exit 1
    fi

    cat <<EOF

MEOVV Mail has been removed from this server.
Public DNS, PTR records, and provider firewall rules were intentionally retained.
$([[ "$PURGE_PACKAGES" == false ]] && printf 'Docker, Nginx, and Certbot packages were retained.\n')$([[ "$REMOVE_BUNDLE" == false ]] && printf 'The source checkout was retained at %s.\n' "$BUNDLE_DIR")
EOF
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
