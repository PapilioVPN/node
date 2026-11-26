#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/install/install.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/install.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
trace=$work/trace

# Record the selected package-manager plan. These checks never write to /etc
# or install packages on the runner.
record() { printf '%s\n' "$*" >> "$trace"; }
grep() { return 1; }
fetch() {
  record "fetch $1"
  [[ "$1" != "${unavailable_url:-}" ]]
}
install() { record "install $*"; }
apt-get() { record "apt-get $*"; }
dnf() { record "dnf $*"; }
amazon-linux-extras() { record "amazon-linux-extras $*"; }
pacman() { record "pacman $*"; }
zypper() { record "zypper $*"; }

select_host() {
  : > "$trace"
  os_id=$1
  VERSION_ID=${2:-}
  VERSION_CODENAME=${3:-}
  install_docker
}

expect() { command grep -Fxq "$1" "$trace"; }

select_host ubuntu 24.04 noble
expect 'fetch https://download.docker.com/linux/ubuntu/gpg'
expect 'apt-get update'
expect 'apt-get install -y --no-install-recommends docker-ce docker-ce-cli containerd.io docker-compose-plugin'
command grep -Fxq 'Suites: noble' "$work/docker.sources"
command grep -Fxq 'Architectures: amd64' "$work/docker.sources"

unavailable_url=https://download.docker.com/linux/ubuntu/gpg
select_host ubuntu 24.04 noble
expect 'fetch https://download.docker.com/linux/debian/gpg'
unset unavailable_url

select_host debian 12 bookworm
expect 'fetch https://download.docker.com/linux/debian/gpg'
command grep -Fxq 'Suites: bookworm' "$work/docker.sources"

for distro in centos fedora rhel rocky; do
  select_host "$distro"
  command grep -Fxq "baseurl=https://download.docker.com/linux/$distro/\$releasever/\$basearch/stable" "$work/docker-ce.repo"
  command grep -Fxq 'gpgcheck=1' "$work/docker-ce.repo"
  expect 'dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin'
done

select_host almalinux
command grep -Fq '/linux/alma/' "$work/docker-ce.repo"
select_host ol
command grep -Fq '/linux/oracle/' "$work/docker-ce.repo"

select_host amzn 2023
expect 'dnf install -y docker'
select_host amzn 2
expect 'amazon-linux-extras install -y docker'
select_host arch
expect 'pacman -S --needed --noconfirm docker docker-compose'
select_host opensuse-leap
expect 'zypper --non-interactive install docker'
select_host opensuse-tumbleweed
expect 'zypper --non-interactive install docker'

if (select_host unknown) > "$work/unsupported" 2>&1; then
  printf 'The automatic installer accepted an unsupported distribution.\n' >&2
  exit 1
fi
command grep -Fq 'Automatic Docker installation is unavailable' "$work/unsupported"
printf 'Installer package-manager plans verified. No host packages were changed.\n'
