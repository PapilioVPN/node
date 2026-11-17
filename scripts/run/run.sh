#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$1" = --help ]; then
    printf '%s\n' 'Usage: ./run.sh /absolute/config-directory CONTAINER_NAME [DOCKER_RUN_OPTIONS...]' \
        'Starts the installed immutable image using Docker and the invoking UID/GID.' \
        'Mounts the config directory read-only at /etc/node; requires config.yml.' \
        'Choose native Docker network/port options, such as --network host, explicitly.' \
        'Add --detach for background execution; inspect logs and stop with native Docker commands.'
    [ "${1-}" = --help ] && exit 0
    exit 2
fi
case "$1" in
    /*) config_directory=$1 ;;
    *) printf '%s\n' 'Configuration directory must be absolute.' >&2; exit 2 ;;
esac
case "$config_directory" in
    *[[:cntrl:]]*) printf '%s\n' 'Docker mount paths cannot contain control characters.' >&2; exit 2 ;;
esac
[ -f "$config_directory/config.yml" ] || { printf '%s\n' 'config.yml is missing from the configuration directory.' >&2; exit 1; }
name=$2
shift 2
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
image=$(cat "$root/image.id")
case "$image" in sha256:*) ;; *) exit 1 ;; esac
hash=${image#sha256:}
case "$hash" in *[!0-9a-f]*) exit 1 ;; esac
[ "${#hash}" -eq 64 ]
mount_source=$(printf '%s' "$config_directory" | sed 's/"/""/g')
exec docker run --pull=never --read-only --user "$(id -u):$(id -g)" \
    --name "$name" --mount "type=bind,\"src=$mount_source\",dst=/etc/node,readonly" \
    "$@" "$image" -config /etc/node/config.yml
