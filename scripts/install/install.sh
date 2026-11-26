#!/usr/bin/env bash
set -Eeuo pipefail

release_version='' # Populated when building the standalone release installer.
directory=/opt/papiliovpn-node
health_url=http://127.0.0.1:8080/healthz
archive=''
yes=false
work=''
candidate=''
pending=''
previous=''
deploying=false
install_log=''
diagnostics_fd=2
stage=0
stage_started=0
blue=''
green=''
red=''
reset=''

usage() {
  cat <<'HELP'
PapilioVPN Node installer for Linux amd64

  --yes               Quick install or update without a terminal prompt
  --directory PATH    Install to an absolute path (default /opt/papiliovpn-node)
  --archive FILE      Use a full release archive and its adjacent .sha256 file
  --health-url URL    HTTP health URL for a custom management address
  --help              Show this help

Run with sudo to install missing Docker components or write to /opt.
HELP
}

fail() {
  printf '%bError:%b %s\n' "$red" "$reset" "$*" >&"$diagnostics_fd"
  if [[ -n "$install_log" ]]; then
    printf 'Error: %s\n' "$*" >> "$install_log"
    printf 'Installation log: %s\n' "$install_log" >&"$diagnostics_fd"
  fi
  exit 1
}

step() {
  stage=$((stage + 1))
  stage_started=$SECONDS
  printf '\n%b[%02d/05] %s%b\n' "$blue" "$stage" "$*" "$reset"
  printf '[%s] %s\n' "$(date -Iseconds)" "$*" >> "$install_log"
}

done_step() {
  local elapsed=$((SECONDS - stage_started))
  printf '        %b✓%b %s (%ss)\n' "$green" "$reset" "$*" "$elapsed"
  printf '        %s (%ss)\n' "$*" "$elapsed" >> "$install_log"
}

fetch() {
  local -a display=(--silent)
  if [[ "$2" == *.tar.gz && -t 2 ]]; then display=(--progress-bar); fi
  curl --disable --fail "${display[@]}" --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 600 --retry 2 --output "$2" -- "$1"
}

check_hash() {
  local result
  result=$(sha256sum < "$1")
  [[ ${result%% *} == "$2" ]] || fail "SHA-256 mismatch for $3."
}

local_docker() {
  docker --host unix:///var/run/docker.sock "$@"
}

install_docker() {
  case "$os_id" in
    debian|ubuntu)
      [[ ${VERSION_CODENAME:-} =~ ^[a-z0-9]+$ ]] || fail 'The apt release codename is missing.'
      if ! grep -qF "download.docker.com/linux/$os_id" /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources 2>/dev/null; then
        install -d -m 0755 /etc/apt/keyrings
        fetch "https://download.docker.com/linux/$os_id/gpg" "$work/docker.asc"
        install -m 0644 "$work/docker.asc" /etc/apt/keyrings/papiliovpn-docker.asc
        cat > "$work/docker.sources" <<APT
Types: deb
URIs: https://download.docker.com/linux/$os_id
Suites: $VERSION_CODENAME
Components: stable
Architectures: amd64
Signed-By: /etc/apt/keyrings/papiliovpn-docker.asc
APT
        install -m 0644 "$work/docker.sources" /etc/apt/sources.list.d/papiliovpn-docker.sources
      fi
      apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends docker-ce docker-ce-cli containerd.io docker-compose-plugin
      ;;
    almalinux|centos|fedora|ol|rhel|rocky)
      local repository=$os_id
      case "$os_id" in
        almalinux) repository=alma ;;
        ol) repository=oracle ;;
      esac
      if ! grep -qE '^\[docker-ce-stable\]' /etc/yum.repos.d/*.repo 2>/dev/null; then
        fetch "https://download.docker.com/linux/$repository/docker-ce.repo" "$work/docker-ce.repo"
        install -m 0644 "$work/docker-ce.repo" /etc/yum.repos.d/papiliovpn-docker.repo
      fi
      dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
      ;;
    amzn)
      case "${VERSION_ID:-}" in
        2023) dnf install -y docker ;;
        2) amazon-linux-extras install -y docker ;;
        *) fail "Automatic Docker installation is unavailable for Amazon Linux ${VERSION_ID:-unknown}." ;;
      esac
      ;;
    arch)
      pacman -S --needed --noconfirm docker docker-compose
      ;;
    opensuse|opensuse-leap|opensuse-tumbleweed|opensuse-slowroll|sles)
      zypper --non-interactive install docker
      ;;
    *)
      fail "Automatic Docker installation is unavailable for $os_id. Install Docker and Compose for this distribution, then rerun."
      ;;
  esac
}

ensure_docker() {
  if ! command -v docker >/dev/null; then
    (( EUID == 0 )) || fail 'Docker is missing. Run the installer with sudo.'
    [[ -d /run/systemd/system ]] || fail 'Automatic Docker setup requires a running systemd instance.'
    printf '        Installing Docker for %s. See install.log for package output.\n' "${PRETTY_NAME:-$os_id}"
    install_docker >> "$install_log" 2>&1
  fi
  if (( EUID == 0 )) && [[ -d /run/systemd/system ]] &&
      [[ $(systemctl show -p LoadState --value docker.service) == loaded ]]; then
    systemctl enable --now docker.service >> "$install_log" 2>&1
  fi
  local_docker info >/dev/null || fail 'The local Docker daemon is unavailable. Check docker.service and access to /var/run/docker.sock.'
  if local_docker compose version >/dev/null 2>&1; then
    return
  fi

  (( EUID == 0 )) || fail 'Docker Compose is missing. Run the installer with sudo.'
  printf '        Installing the official Docker Compose plugin.\n'
  fetch 'https://github.com/docker/compose/releases/download/v5.1.4/docker-compose-linux-x86_64' "$work/docker-compose"
  check_hash "$work/docker-compose" 33b208d7e76639db742fae84b966cc01dacae58ca3fc4dabbc907045aefdf0c4 'Docker Compose'
  install -d -m 0755 /usr/local/lib/docker/cli-plugins
  install -m 0755 "$work/docker-compose" /usr/local/lib/docker/cli-plugins/docker-compose
  local_docker compose version >> "$install_log"
}

compose() {
  local slot=$1 image
  shift
  image=$(< "$slot/image-id")
  local -a args=(--project-name "$project" --project-directory "$directory" --env-file /dev/null --file "$slot/compose.yml")
  if [[ -f "$directory/compose.override.yml" ]]; then
    args+=(--file "$directory/compose.override.yml")
  fi
  NODE_IMAGE="$image" local_docker compose "${args[@]}" "$@"
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$candidate" && -f "$directory/current" && $(< "$directory/current") == "${candidate##*/}" ]]; then
    deploying=false
    candidate=''
  fi
  if [[ "$deploying" == true ]]; then
    if [[ -n "$previous" ]]; then
      printf 'Restoring the previous container configuration…\n' >&2
      compose "$previous" up -d --pull never >&2 ||
        printf 'Restoration failed. Inspect Docker Compose logs for project %s.\n' "$project" >&2
    else
      compose "$candidate" down --timeout 8 >&2 ||
        printf 'Container cleanup failed for project %s.\n' "$project" >&2
    fi
  fi
  if [[ -n "$pending" ]]; then rm -f -- "$pending"; fi
  if [[ -n "$candidate" ]]; then rm -rf -- "$candidate"; fi
  if [[ -n "$work" ]]; then rm -rf -- "$work"; fi
  if [[ -n "$install_log" ]]; then
    printf 'Exit status: %s\n' "$status" >> "$install_log"
  fi
  exit "$status"
}

verify_bundle() {
  local name=${archive##*/} expected listed extra
  [[ -f "$archive" && -f "$archive.sha256" ]] || fail 'The archive and its matching .sha256 file are required.'
  read -r expected listed extra < "$archive.sha256"
  [[ "$expected" =~ ^[[:xdigit:]]{64}$ && "$listed" == "$name" && -z "$extra" ]] ||
    fail "The checksum file must identify $name."
  check_hash "$archive" "${expected,,}" "$name"
  tar -xzf "$archive" -C "$work" --no-same-owner --no-same-permissions \
    node/compose.yml node/config.example.yml node/image-name node/image.tar
  local file
  for file in compose.yml config.example.yml image-name image.tar; do
    [[ -f "$work/node/$file" && ! -L "$work/node/$file" ]] || fail "Invalid bundle member: $file."
  done
}

wait_ready() {
  local slot=$1 attempts=$2 status started image probe attempt restarts
  container=$(compose "$slot" ps --all --quiet node) || return 1
  [[ -n "$container" ]] || return 1
  for ((attempt=0; attempt<attempts; attempt++)); do
    IFS='|' read -r status started image restarts < <(
      local_docker inspect --format '{{.State.Status}}|{{.State.StartedAt}}|{{.Image}}|{{.RestartCount}}' "$container"
    )
    if [[ "$status" == running && "$image" == "$(< "$slot/image-id")" ]]; then
      # Check this container reached readiness before trusting a host-network address.
      if local_docker logs --since "$started" "$container" 2>&1 | grep -F '"msg":"management listeners ready"' >/dev/null; then
        probe=$(curl --disable --silent --noproxy '*' --max-time 1 --output /dev/null --write-out '%{http_code}' -- "$health_url") || probe=''
        [[ "$probe" != 204 ]] || return 0
      fi
    fi
    if [[ "$status" == dead || "$status" == exited || "$status" == restarting && "${restarts:-0}" -ge 3 ]]; then
      return 1
    fi
    if (( attempt + 1 < attempts )); then sleep 1; fi
  done
  return 1
}

show_ready() {
  local logs_command='docker'
  if [[ -n ${SUDO_USER:-} ]]; then logs_command='sudo docker'; fi
  printf '\n%b╭─ Node v%s is ready%b\n  Configuration  %s/config/config.yml\n  Health         %s\n  Installer log  %s\n  Live logs      %s logs --follow %s\n' \
    "$green" "$1" "$reset" "$directory" "$health_url" "$install_log" "$logs_command" "$container"
}

main() {
  while (( $# )); do
    case "$1" in
      --yes) yes=true; shift ;;
      --directory|--archive|--health-url)
        if (( $# < 2 )) || [[ -z "$2" || "$2" == --* ]]; then
          fail "$1 requires a value."
        fi
        case "$1" in
          --directory) directory=$2 ;;
          --archive) archive=$2 ;;
          --health-url) health_url=$2 ;;
        esac
        shift 2
        ;;
      --help|-h) usage; return ;;
      *) fail "Unknown option: $1" ;;
    esac
  done
  [[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || fail 'This release requires Linux amd64.'
  [[ "$directory" == /* && "$directory" != / && $(realpath -m -- "$directory") == "$directory" ]] ||
    fail 'Choose a canonical absolute installation path without symbolic links.'
  [[ "$health_url" == http://*/healthz || "$health_url" == https://*/healthz ]] || fail 'Use an HTTP or HTTPS /healthz URL.'
  local command
  for command in curl tar sha256sum flock; do
    command -v "$command" >/dev/null || fail "Required host command is missing: $command."
  done
  if [[ -r /etc/os-release ]]; then
    # shellcheck source=/dev/null
    . /etc/os-release
  fi
  os_id=${ID:-unknown}

  local version=$release_version
  if [[ -n "$archive" ]]; then
    [[ ${archive##*/} =~ ^node-([0-9]+\.[0-9]+\.[0-9]+)-linux-amd64\.tar\.gz$ ]] ||
      fail 'Use a full node-VERSION-linux-amd64.tar.gz archive.'
    version=${BASH_REMATCH[1]}
  fi
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Use a versioned release installer or provide --archive.'

  if [[ -t 1 && ${TERM:-dumb} != dumb && -z ${NO_COLOR:-} ]]; then
    blue=$'\033[1;36m'
    green=$'\033[1;32m'
    red=$'\033[1;31m'
    reset=$'\033[0m'
  fi
  printf '%b╭─ PapilioVPN Node Installer  /  v%s%b\n  Host        %s / amd64\n  Install to  %s\n  Management  %s\n' \
    "$blue" "$version" "$reset" "${PRETTY_NAME:-$os_id}" "$directory" "$health_url"
  if [[ "$yes" != true ]]; then
    local reply
    [[ -t 0 ]] || fail 'Use --yes for non-interactive installs.'
    printf '\nInstall or update Node? [y/N] ' >&2
    if ! read -r reply < /dev/tty; then fail 'Use --yes for non-interactive installs.'; fi
    [[ "$reply" == y || "$reply" == Y ]] || return 0
  fi

  work=$(mktemp -d)
  exec {diagnostics_fd}>&2
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'fail "Installation stopped at line $LINENO. Review the package or service output."' ERR

  local path
  for path in "$directory" "$directory/config" "$directory/releases" "$directory/current" "$directory/compose.override.yml"; do
    [[ ! -L "$path" ]] || fail "Installation path points to a symbolic link: $path."
    [[ ! -e "$path" || -O "$path" ]] || fail "Installation path must be owned by the invoking user: $path."
  done
  mkdir -p -- "$directory"
  local lock
  exec {lock}< "$directory"
  flock -n "$lock" || fail 'Another installation is running in this directory.'
  pending=$(mktemp "$directory/.install-log.XXXXXXXX")
  mv -Tf -- "$pending" "$directory/install.log"
  pending=''
  install_log=$directory/install.log
  printf '[%s] PapilioVPN Node v%s on %s\n' "$(date -Iseconds)" "$version" "${PRETTY_NAME:-$os_id}" >> "$install_log"
  step 'Inspect local installation'

  local entry image_name identity image_id
  if [[ -e "$directory/current" ]]; then
    [[ -f "$directory/current" ]] || fail 'Invalid installed release marker.'
    entry=$(< "$directory/current")
    [[ "$entry" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\.[a-zA-Z0-9]+$ ]] || fail 'Invalid installed release marker.'
    previous=$directory/releases/$entry
    [[ -d "$previous" && ! -L "$previous" &&
       -f "$previous/compose.yml" && ! -L "$previous/compose.yml" &&
       -f "$previous/image-id" && ! -L "$previous/image-id" ]] ||
      fail 'The previous release files are missing.'
  fi
  project=papiliovpn-node
  if [[ "$directory" != /opt/papiliovpn-node ]]; then
    local name_hash
    name_hash=$(printf '%s' "$directory" | sha256sum)
    project+=-${name_hash:0:12}
  fi
  if [[ -z "$archive" && -n "$previous" && "${previous##*/}" == "v$version."* ]] &&
      command -v docker >/dev/null && local_docker compose version >/dev/null 2>&1 &&
      wait_ready "$previous" 1; then
    done_step 'This version is already running. No download needed.'
    show_ready "$version"
    return
  fi
  done_step 'Local installation checked'

  step 'Verify the release archive'
  if [[ -z "$archive" ]]; then
    local name=node-$version-linux-amd64.tar.gz
    local base=https://github.com/PapilioVPN/node/releases/download/v$version
    archive=$work/$name
    fetch "$base/$name.sha256" "$archive.sha256"
    fetch "$base/$name" "$archive"
  fi
  verify_bundle
  done_step 'Archive checksum verified'

  step 'Prepare Docker and Compose'
  ensure_docker

  if [[ -z "$previous" && -n $(local_docker ps --all --quiet --filter "label=com.docker.compose.project=$project") ]]; then
    fail "Docker Compose project $project already exists without an installer record."
  fi
  done_step 'Local Docker and Compose available'

  step 'Load the image locally'
  image_name=$(< "$work/node/image-name")
  [[ "$image_name" =~ ^papiliovpn/node:[a-zA-Z0-9._-]+$ ]] || fail 'Unexpected image tag in the release archive.'
  local_docker load -i "$work/node/image.tar" >> "$install_log" 2>&1
  identity=$(local_docker image inspect --format '{{.Os}}/{{.Architecture}} {{index .Config.Labels "org.opencontainers.image.version"}} {{.Id}}' "$image_name")
  [[ "$identity" == "linux/amd64 $version sha256:"* ]] || fail 'The imported image does not match the release.'
  image_id=${identity##* }
  [[ "$image_id" =~ ^sha256:[[:xdigit:]]{64}$ ]] || fail 'The imported image has an invalid ID.'
  done_step "Image v$version verified"

  mkdir -p -- "$directory/config" "$directory/releases"
  if [[ ! -e "$directory/config/config.yml" && ! -L "$directory/config/config.yml" ]]; then
    install -m 0644 "$work/node/config.example.yml" "$directory/config/config.yml"
  fi
  candidate=$(mktemp -d "$directory/releases/v$version.XXXXXXXX")
  install -m 0644 "$work/node/compose.yml" "$candidate/compose.yml"
  printf '%s\n' "$image_id" > "$candidate/image-id"

  step 'Start Node and check readiness'
  compose "$candidate" config --quiet
  deploying=true
  compose "$candidate" up -d --pull never >> "$install_log" 2>&1
  if ! wait_ready "$candidate" 30; then
    compose "$candidate" logs --tail 15 >> "$install_log" 2>&1 || true
    fail "Node did not become healthy at $health_url."
  fi
  done_step 'Management API is healthy'

  if [[ -n "$previous" ]] && cmp -s "$candidate/compose.yml" "$previous/compose.yml" &&
      cmp -s "$candidate/image-id" "$previous/image-id"; then
    rm -rf -- "$candidate"
  else
    pending=$(mktemp "$directory/.current.XXXXXXXX")
    printf '%s\n' "${candidate##*/}" > "$pending"
    mv -Tf -- "$pending" "$directory/current"
    pending=''
  fi
  deploying=false
  candidate=''

  show_ready "$version"
}

if [[ ${BASH_SOURCE[0]:-$0} == "$0" ]]; then
  main "$@"
fi
