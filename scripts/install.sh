#!/bin/bash
# User-invoked installer/updater. Only release binaries are replaced; preferences
# and shell profiles are never read or changed. Requires macOS's Bash 3.2 tools.
set -euo pipefail
export LC_ALL=C
umask 077

repository=https://github.com/matthewmorek/gitbuddy
version=
install_dir=
pinned=false
temp_dir=

fail() { printf 'gitbuddy installer: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
Usage: bash install.sh [--version X.Y.Z] [--install-dir /absolute/directory]
Installs or updates gitbuddy (macOS arm64) in ~/.local/bin by default.
Without --version, installs the latest stable release, never a downgrade.
Use --version X.Y.Z to pin a release, including an intentional rollback.
No sudo, profile edits, credentials, or quarantine changes are performed.
Initial releases are unsigned and not notarized. macOS may block execution.
  --help    Show this help
HELP
}
stable_version() { [[ $1 =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; }
while [ "$#" -gt 0 ]; do
  case $1 in
    --help) usage; exit 0 ;;
    --version)
      [ "$#" -ge 2 ] || fail '--version requires X.Y.Z'
      [ "$pinned" = false ] || fail 'duplicate --version'
      version=$2; pinned=true; shift 2
      stable_version "$version" || fail 'version must be stable X.Y.Z (no leading zeros)'
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || fail '--install-dir requires an absolute directory'
      [ -z "$install_dir" ] || fail 'duplicate --install-dir'
      install_dir=$2; shift 2
      [ -n "$install_dir" ] || fail 'empty installation directory'
      ;;
    *) fail "unknown argument: $1 (see --help)" ;;
  esac
done
[ "$(uname -s)" = Darwin ] || fail 'unsupported OS; only macOS arm64 is supported'
[ "$(uname -m)" = arm64 ] || fail 'unsupported architecture; only macOS arm64 is supported'
for tool in curl shasum stat mktemp git id mkdir chmod mv rm tr; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
done
git --version >/dev/null 2>&1 || fail 'Git must be installed and usable'
uid=$(id -u)
if [ -z "$install_dir" ]; then
  [ -n "${HOME:-}" ] || fail 'HOME is unset; use --install-dir'
  install_dir=$HOME/.local/bin
fi
case $install_dir in
  /*) ;;
  *) fail '--install-dir must be an absolute directory' ;;
esac
while [ "$install_dir" != / ] && [[ $install_dir = */ ]]; do install_dir=${install_dir%/}; done
# Reject ambiguous components instead of resolving symlinks through an untrusted
# path. Whitespace and shell metacharacters are otherwise ordinary path bytes.
case "$install_dir/" in
  *'/../'*|*'/./'*|*'//'*) fail 'installation directory must not contain ., .., or empty components' ;;
esac

# Ancestors may be owned by root; the final installation directory must be ours.
# Root-owned sticky temporary ancestors are safe, unlike arbitrary writable dirs.
check_directory() {
  local directory=$1 owner mode bits
  [ ! -L "$directory" ] && [ -d "$directory" ] || fail "unsafe directory: $directory"
  owner=$(stat -f '%u' "$directory") || fail 'cannot inspect directory owner'
  mode=$(stat -f '%Lp' "$directory") || fail 'cannot inspect directory permissions'
  bits=$((8#$mode))
  [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || fail "directory is not owned by you or root: $directory"
  if [ $((bits & 0022)) -ne 0 ]; then
    [ "$owner" = 0 ] && [ $((bits & 01000)) -ne 0 ] || fail "unsafe writable directory: $directory"
  fi
}
prepare_directory() {
  local directory=$1 parent
  if [ "$directory" != / ]; then
    parent=${directory%/*}; [ -n "$parent" ] || parent=/
    prepare_directory "$parent"
  fi
  if [ ! -e "$directory" ] && [ ! -L "$directory" ]; then
    mkdir "$directory" || fail "cannot create directory: $directory"
  fi
  check_directory "$directory"
}
check_install_directory() {
  local mode
  prepare_directory "$install_dir"
  [ "$(stat -f '%u' "$install_dir")" = "$uid" ] || fail 'installation directory must be owned by you'
  mode=$(stat -f '%Lp' "$install_dir")
  [ $((8#$mode & 0022)) -eq 0 ] || fail 'installation directory is writable by other users'
  [ -w "$install_dir" ] && [ -x "$install_dir" ] || fail 'installation directory is not writable/searchable'
}
check_install_directory
directory_identity=$(stat -f '%d:%i' "$install_dir")
target=$install_dir/gitbuddy

check_target() {
  local mode
  [ ! -L "$target" ] && [ -f "$target" ] && [ -x "$target" ] || fail "unsafe target (expected an owned executable): $target"
  [ "$(stat -f '%u' "$target")" = "$uid" ] || fail 'target is not owned by you'
  [ "$(stat -f '%l' "$target")" = 1 ] || fail 'target has multiple hard links'
  mode=$(stat -f '%Lp' "$target")
  [ $((8#$mode & 0022)) -eq 0 ] || fail 'target is writable by other users'
}
fingerprint() {
  stat -f '%d:%i:%u:%p:%l:%z:%m:%c' "$target" || return 1
  shasum -a 256 < "$target" || return 1
}
baseline=absent
current=
if [ -e "$target" ] || [ -L "$target" ]; then
  check_target
  baseline=$(fingerprint) || fail 'cannot fingerprint target'
  current_output=$("$target" --version) || fail 'existing target does not report a gitbuddy release version'
  current=${current_output#gitbuddy }
  stable_version "$current" && [ "$current_output" = "gitbuddy $current" ] || fail 'refusing to overwrite an unrelated or unrecognized executable'
fi

# -q MUST be first: no project/user curl configuration, including credentials or
# insecure transport options. Both initial requests and redirects are HTTPS-only.
download() {
  curl -q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --max-redirs 5 --connect-timeout 15 --max-time 180 --max-filesize "$3" --output "$2" "$1"
}
if [ "$pinned" = false ]; then
  release_url=$(curl -q --fail --silent --show-error --head --location --proto '=https' --proto-redir '=https' \
    --max-redirs 5 --connect-timeout 15 --max-time 60 --output /dev/null --write-out '%{url_effective}' \
    "$repository/releases/latest") || fail 'could not resolve latest stable release; check connectivity and whether a stable release exists (use --version X.Y.Z for a published prerelease candidate)'
  version=${release_url#"$repository/releases/tag/v"}
  stable_version "$version" && [ "$release_url" = "$repository/releases/tag/v$version" ] || fail 'latest release redirect is not a stable tag URL for this repository'
fi

# Compare decimal components without shell integer overflow (SemVer has no numeric
# size limit). Stable, no-leading-zero components compare by length then byte order.
version_greater() {
  local left=$1 right=$2 a b i
  for i in 1 2 3; do
    a=${left%%.*}; b=${right%%.*}
    if [ "${#a}" -gt "${#b}" ]; then return 0; fi
    if [ "${#a}" -lt "${#b}" ]; then return 1; fi
    if [[ $a > $b ]]; then return 0; fi
    if [[ $a < $b ]]; then return 1; fi
    left=${left#*.}; right=${right#*.}
  done
  return 1
}
if [ -n "$current" ]; then
  if [ "$current" = "$version" ]; then
    printf 'gitbuddy %s is already installed at %s\n' "$version" "$target"
    exit 0
  fi
  if [ "$pinned" = false ] && version_greater "$current" "$version"; then
    fail "latest release $version is older than installed $current; use --version $version for an explicit rollback"
  fi
fi

cleanup() { if [ -n "$temp_dir" ]; then rm -rf "$temp_dir"; fi; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
# Staging in the destination directory guarantees same-filesystem rename. This
# invocation owns only the unpredictable directory returned by mktemp.
temp_dir=$(mktemp -d "$install_dir/.gitbuddy-install.XXXXXXXX") || fail 'cannot create staging directory'
asset=gitbuddy-$version-darwin-arm64
base_url=$repository/releases/download/v$version
staged=$temp_dir/$asset
download "$base_url/$asset" "$staged" 157286400 || fail "binary download failed; check connectivity and that v$version is published (not a draft) with the macOS arm64 asset"
download "$base_url/SHA256SUMS" "$temp_dir/SHA256SUMS" 1048576 || fail 'checksum download failed'

# Only one exact asset entry is accepted; never let a remote checksum manifest
# select local filenames or ask shasum to read arbitrary paths.
expected=
matches=0
while IFS= read -r line || [ -n "$line" ]; do
  if [[ $line =~ ^([0-9a-fA-F]{64})[[:blank:]][\ \*](.+)$ ]]; then
    if [ "${BASH_REMATCH[2]}" = "$asset" ]; then
      expected=${BASH_REMATCH[1]}; matches=$((matches + 1))
    fi
  fi
done < "$temp_dir/SHA256SUMS"
[ "$matches" = 1 ] || fail 'SHA256SUMS must contain exactly one matching asset entry'
actual=$(shasum -a 256 < "$staged") || fail 'cannot hash download'
actual=${actual%% *}
# shasum emits lowercase; accept either case in the manifest.
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
[ "$actual" = "$expected" ] || fail 'download checksum mismatch'
chmod 755 "$staged" || fail 'cannot mark staged binary executable'
staged_output=$("$staged" --version) || fail 'staged binary could not run --version (check macOS security requirements)'
[ "$staged_output" = "gitbuddy $version" ] || fail 'staged binary version does not match requested release'

# These bounded rechecks prevent overwriting changes observed since inspection.
# This is not a lock against a hostile process with the same user's privileges;
# no portable shell primitive provides an atomic compare-and-rename.
check_install_directory
[ "$(stat -f '%d:%i' "$install_dir")" = "$directory_identity" ] || fail 'installation directory changed during download'
if [ "$baseline" = absent ]; then
  [ ! -e "$target" ] && [ ! -L "$target" ] || fail 'target appeared during download; rerun installer'
else
  check_target
  [ "$(fingerprint)" = "$baseline" ] || fail 'target changed during download; rerun installer'
fi
mv -f "$staged" "$target" || fail 'atomic replacement failed'
printf 'Installed gitbuddy %s at %s\n' "$version" "$target"
case :${PATH:-}: in
  *:"$install_dir":*) ;;
  *) printf 'Add this directory to PATH in your shell configuration: %s\n' "$install_dir" ;;
esac
