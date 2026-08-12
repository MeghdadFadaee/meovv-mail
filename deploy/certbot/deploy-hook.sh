#!/bin/sh
set -eu

: "${RENEWED_LINEAGE:?Certbot must provide RENEWED_LINEAGE}"

bundle_dir="${MEOVV_BUNDLE_DIR:-/opt/meovv-mail}"
tls_dir="$bundle_dir/secrets/tls"

if [ ! -f "$bundle_dir/compose.yaml" ]; then
    echo "MEOVV bundle not found at $bundle_dir" >&2
    exit 1
fi

install -d -m 700 "$tls_dir"
install -m 644 "$RENEWED_LINEAGE/fullchain.pem" "$tls_dir/fullchain.pem"
install -m 600 "$RENEWED_LINEAGE/privkey.pem" "$tls_dir/privkey.pem"

# Once the Certificate object points at /etc/stalwart/tls, restarting Stalwart
# makes renewed files active for SMTP submission and IMAP TLS as well as HTTPS.
if [ -n "$(cd "$bundle_dir" && docker compose ps -q stalwart 2>/dev/null || true)" ]; then
    cd "$bundle_dir"
    docker compose restart stalwart
fi
