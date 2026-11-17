#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "$1" = --help ]; then
    printf '%s\n' 'Usage: ./install.sh /absolute/installation-directory' \
        'Run from an extracted, checksum-verified Linux amd64 runtime bundle.' \
        'Requires Docker, a POSIX shell and GNU coreutils. Loads the bundled image.' \
        'Preserves private configuration, certificates, data and running containers.'
    [ "${1-}" = --help ] && exit 0
    exit 2
fi
case "$1" in
    /|*[[:cntrl:]]*) printf '%s\n' 'Invalid installation directory.' >&2; exit 2 ;;
    /*) destination=$(realpath -m -- "$1") ;;
    *) printf '%s\n' 'Installation directory must be absolute.' >&2; exit 2 ;;
esac
[ "$destination" != / ] || { printf '%s\n' 'Invalid installation directory.' >&2; exit 2; }
for path in scripts scripts/run deploy run.sh scripts/run/run.sh deploy/config.example.yml revision image.id; do
    [ ! -L "$destination/$path" ] || { printf '%s\n' 'Installation-owned paths must not be symbolic links.' >&2; exit 2; }
done
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
sha256sum --check --strict checksums.sha256
reference=$(cat image.ref)
revision=$(cat revision)
case "$revision" in *[!0-9a-f]*) exit 1 ;; esac
[ "${#revision}" -eq 40 ]
[ "$reference" = "papilio-node-build:$revision" ]
docker image load --input image.tar
# Docker storage backends can represent imported image IDs differently. Load
# the checksum-verified tagged image, then pin its local immutable identifier.
image=$(docker image inspect --format '{{.Id}}' "$reference")
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64 ]
[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" = "$revision" ]

umask 077
mkdir -p -- "$destination/scripts/run" "$destination/deploy"
install -m 0755 run.sh "$destination/run.sh"
install -m 0755 scripts/run/run.sh "$destination/scripts/run/run.sh"
install -m 0644 deploy/config.example.yml "$destination/deploy/config.example.yml"
install -m 0644 revision "$destination/revision"
# Publish the selected immutable image only after the required files are installed.
pending=$(mktemp "$destination/.image-id.XXXXXX")
trap 'rm -f -- "$pending"' EXIT HUP INT TERM
printf '%s\n' "$image" > "$pending"
mv -f -- "$pending" "$destination/image.id"
printf '%s\n' "Installed revision $revision in $destination" \
    'Provide a private config directory containing config.yml and its certificates.' \
    "Run: $destination/run.sh /absolute/config-directory CONTAINER_NAME [DOCKER_RUN_OPTIONS...]" \
    'Choose network and port options explicitly. Stop with docker stop --time SECONDS CONTAINER_NAME.'
