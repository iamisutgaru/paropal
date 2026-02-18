#!/usr/bin/env bash
set -euo pipefail

USER_NAME="${1:-linuxuser}"
DEV="/dev/vdb1"
MNT="/mnt/blockstorage"
STATE_DIR="/var/lib/paropal"
BLOCK_DONE_MARKER="${STATE_DIR}/block-init.done"
DEV_DONE_MARKER="${STATE_DIR}/dev-init.done"

log() {
  printf '[paropal-block-init] %s\n' "$*"
}

fail() {
  echo "[paropal-block-init] ERROR: $*" >&2
  exit 1
}

user_home() {
  getent passwd "$USER_NAME" | cut -d: -f6
}

ensure_line_in_file() {
  local line="$1"
  local file="$2"
  touch "$file"
  if ! grep -Fxq "$line" "$file"; then
    printf '%s\n' "$line" >>"$file"
  fi
}

is_mounted() {
  if command -v findmnt >/dev/null 2>&1; then
    # --target returns the filesystem that contains the path (often /), not whether the path is a mountpoint.
    findmnt -rn --mountpoint "$MNT" >/dev/null 2>&1
    return $?
  fi
  mountpoint -q "$MNT"
}

mount_block_device() {
  mkdir -p "$MNT"
  chmod 0755 "$MNT"

  [[ -b "$DEV" ]] || fail "block device not found yet: ${DEV}"

  if is_mounted; then
    return 0
  fi

  local uuid
  uuid="$(blkid -s UUID -o value "$DEV" || true)"
  [[ -n "$uuid" ]] || fail "cannot read filesystem UUID from ${DEV}"

  local fstab_line
  fstab_line="UUID=${uuid} ${MNT} ext4 defaults,noatime,nofail 0 0"
  if ! grep -qF "UUID=${uuid} ${MNT} " /etc/fstab; then
    ensure_line_in_file "$fstab_line" /etc/fstab
  fi

  mount "$MNT"
}

copy_secret() {
  local src="$1"
  local dst="$2"
  local mode="$3"
  local owner_user="$4"
  local owner_group="$5"

  [[ -f "$src" ]] || fail "missing required secret: ${src}"

  mkdir -p "$(dirname "$dst")"
  install -m "$mode" -o "$owner_user" -g "$owner_group" "$src" "$dst"
}

copy_block_secrets() {
  local home
  home="$(user_home)"
  [[ -n "$home" ]] || fail "cannot determine home directory for user: ${USER_NAME}"

  log "Copying secrets from ${MNT} to ${home}"

  mkdir -p "${home}/.ssh" "${home}/.config/gh" "${home}/.codex"
  chown -R "${USER_NAME}:${USER_NAME}" "${home}/.ssh" "${home}/.config" "${home}/.codex"
  chmod 0700 "${home}/.ssh" "${home}/.codex"
  chmod 0755 "${home}/.config"
  chmod 0700 "${home}/.config/gh"

  copy_secret "${MNT}/codex_auth.json" "${home}/.codex/auth.json" 0600 "${USER_NAME}" "${USER_NAME}"
  copy_secret "${MNT}/gh_hosts.yml" "${home}/.config/gh/hosts.yml" 0600 "${USER_NAME}" "${USER_NAME}"
  copy_secret "${MNT}/config" "${home}/.ssh/config" 0600 "${USER_NAME}" "${USER_NAME}"
  copy_secret "${MNT}/id_ed25519" "${home}/.ssh/id_ed25519" 0600 "${USER_NAME}" "${USER_NAME}"
  copy_secret "${MNT}/id_ed25519.pub" "${home}/.ssh/id_ed25519.pub" 0644 "${USER_NAME}" "${USER_NAME}"
  copy_secret "${MNT}/${USER_NAME}.google_authenticator" "${home}/.google_authenticator" 0400 "${USER_NAME}" "${USER_NAME}"
}

ensure_fd_command() {
  if command -v fdfind >/dev/null 2>&1; then
    ln -sf "$(command -v fdfind)" /usr/local/bin/fd
  fi
}

install_base_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    fd-find \
    git \
    gnupg \
    jq \
    ripgrep \
    tmux \
    xz-utils
}

install_node_22() {
  if command -v node >/dev/null 2>&1; then
    return 0
  fi

  log "Installing Node.js v22"
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs
}

resolve_go_126_version() {
  local resolved
  resolved="$({
    curl -fsSL 'https://go.dev/dl/?mode=json' \
      | jq -r '.[].version' \
      | grep -E '^go1\\.26\\.' \
      | sed 's/^go//' \
      | sort -V \
      | tail -n1
  } || true)"

  if [[ -n "$resolved" ]]; then
    printf '%s\n' "$resolved"
  else
    printf '1.26.0\n'
  fi
}

install_go_126() {
  if [[ -x /usr/local/go/bin/go ]] && /usr/local/go/bin/go version | grep -q ' go1\\.26\\.'; then
    return 0
  fi

  local go_version tarball tmp
  go_version="$(resolve_go_126_version)"
  tarball="go${go_version}.linux-amd64.tar.gz"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  log "Installing Go ${go_version}"
  if ! curl -fL "https://go.dev/dl/${tarball}" -o "$tmp/$tarball"; then
    curl -fL "https://dl.google.com/go/${tarball}" -o "$tmp/$tarball"
  fi

  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp/$tarball"

  cat >/etc/profile.d/go-path.sh <<'EOF'
export PATH="/usr/local/go/bin:$PATH"
EOF
}

install_gh() {
  if command -v gh >/dev/null 2>&1; then
    return 0
  fi

  log "Installing GitHub CLI"
  install -m 0755 -d /etc/apt/keyrings
  if [[ ! -f /etc/apt/keyrings/githubcli-archive-keyring.gpg ]]; then
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
  fi

  local repo_line
  repo_line="deb [arch=amd64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main"
  if [[ ! -f /etc/apt/sources.list.d/github-cli.list ]] || ! grep -Fxq "$repo_line" /etc/apt/sources.list.d/github-cli.list; then
    printf '%s\n' "$repo_line" >/etc/apt/sources.list.d/github-cli.list
  fi

  apt-get update -y
  apt-get install -y gh
}

ensure_profile_path() {
  local home profile
  home="$(user_home)"
  profile="${home}/.profile"

  # npm/uv installs to $HOME/.local/bin; ensure it exists for login shells and tool installs.
  install -d -m 0755 -o "${USER_NAME}" -g "${USER_NAME}" "${home}/.local/bin"

  ensure_line_in_file 'export PATH="/usr/local/go/bin:$PATH"' "$profile"
  ensure_line_in_file 'export PATH="$HOME/.local/bin:$PATH"' "$profile"
  chown "${USER_NAME}:${USER_NAME}" "$profile"
}

configure_tmux() {
  local home tmuxconf
  home="$(user_home)"
  tmuxconf="${home}/.tmux.conf"

  touch "$tmuxconf"
  chown "${USER_NAME}:${USER_NAME}" "$tmuxconf"
  chmod 0644 "$tmuxconf"

  ensure_line_in_file 'set -g mouse on' "$tmuxconf"
}

configure_tmux_autostart() {
  local home bashrc
  home="$(user_home)"
  bashrc="${home}/.bashrc"

  touch "$bashrc"
  chown "${USER_NAME}:${USER_NAME}" "$bashrc"
  chmod 0644 "$bashrc"

  if grep -qF "# >>> paropal tmux >>>" "$bashrc"; then
    return 0
  fi

  cat >>"$bashrc" <<'EOF'

# >>> paropal tmux >>>
# Auto-start tmux for interactive SSH sessions (TTY allocated).
# This avoids breaking scp/rsync/remote commands (no TTY) and prevents nested tmux shells.
if [ -n "${SSH_TTY:-}" ] && [ -z "${TMUX:-}" ] && command -v tmux >/dev/null 2>&1; then
  exec tmux new-session -A -s paropal
fi
# <<< paropal tmux <<<
EOF

  chown "${USER_NAME}:${USER_NAME}" "$bashrc"
}

install_uv_for_user() {
  local home
  home="$(user_home)"

  if [[ -x "${home}/.local/bin/uv" ]]; then
    return 0
  fi

  log "Installing uv for ${USER_NAME}"
  install -d -m 0755 -o "${USER_NAME}" -g "${USER_NAME}" "${home}/.local/bin"
  runuser -l "${USER_NAME}" -c 'export UV_UNMANAGED_INSTALL="$HOME/.local/bin"; curl -LsSf https://astral.sh/uv/install.sh | sh'
}

install_codex_for_user() {
  local home
  home="$(user_home)"

  if [[ -x "${home}/.local/bin/codex" ]]; then
    return 0
  fi

  log "Installing @openai/codex for ${USER_NAME}"
  install -d -m 0755 -o "${USER_NAME}" -g "${USER_NAME}" "${home}/.local/bin"
  runuser -l "${USER_NAME}" -c 'npm install -g --prefix "$HOME/.local" @openai/codex'
}

main() {
  mkdir -p "$STATE_DIR"

  id "$USER_NAME" >/dev/null 2>&1 || fail "user not found yet: ${USER_NAME}"

  if [[ ! -f "$BLOCK_DONE_MARKER" ]]; then
    mount_block_device
    copy_block_secrets
    touch "$BLOCK_DONE_MARKER"
    log "Block-init stage complete"
  fi

  if [[ ! -f "$DEV_DONE_MARKER" ]]; then
    log "Installing dev tools"
    install_base_packages
    ensure_fd_command
    install_node_22
    install_go_126
    install_gh
    install_uv_for_user
    install_codex_for_user
    ensure_profile_path
    configure_tmux
    configure_tmux_autostart
    touch "$DEV_DONE_MARKER"
    log "Dev-init stage complete"
  fi

  if [[ -f "$BLOCK_DONE_MARKER" && -f "$DEV_DONE_MARKER" ]]; then
    systemctl disable --now paropal-block-init.timer >/dev/null 2>&1 || true
    log "All stages complete; timer disabled"
  fi
}

main "$@"
