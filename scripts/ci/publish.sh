#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "${GITHUB_EVENT_NAME-}" != workflow_dispatch ] || [ "${GITHUB_REF-}" != refs/heads/main ]; then
    printf '%s\n' 'Publication requires a manually dispatched main-branch workflow and its checked runtime archive.' >&2
    exit 2
fi
: "${GITHUB_SHA:?}" "${GITHUB_REPOSITORY:?}" "${GITHUB_ACTOR:?}" "${GH_TOKEN:?}"
[ "$(gh api "repos/$GITHUB_REPOSITORY/git/ref/heads/main" --jq .object.sha)" = "$GITHUB_SHA" ]
archive=$(realpath -- "$1")
[ "$(basename -- "$archive")" = "node-linux-amd64-$GITHUB_SHA.tar.gz" ]
(cd "$(dirname -- "$archive")" && sha256sum --check --strict "$(basename -- "$archive").sha256")
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM
# Credentials and extracted artifacts are private to this job invocation.
export DOCKER_CONFIG="$stage/docker"
mkdir -m 0700 "$DOCKER_CONFIG"
tar -xzf "$archive" -C "$stage"
cd "$stage/node"
sha256sum --check --strict checksums.sha256
[ "$(cat revision)" = "$GITHUB_SHA" ]
reference=$(cat image.ref)
[ "$reference" = "papilio-node-build:$GITHUB_SHA" ]
docker image load --input image.tar
[ "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$reference")" = "$GITHUB_SHA" ]
repository=$(printf '%s' "$GITHUB_REPOSITORY" | tr '[:upper:]' '[:lower:]')
destination="ghcr.io/$repository:$GITHUB_SHA"
printf '%s' "$GH_TOKEN" | docker login ghcr.io --username "$GITHUB_ACTOR" --password-stdin
# Tag and push the loaded artifact. No build is performed in this job.
docker image tag "$reference" "$destination"
docker image push "$destination"
docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$destination"
