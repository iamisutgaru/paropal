#!/usr/bin/env bash
set -euo pipefail

USER_NAME="${1:-linuxuser}"
STATE_DIR="/var/lib/paropal"
DONE_MARKER="${STATE_DIR}/base-init.done"

log() {
  printf '[paropal-base-init] %s\n' "$*"
}

main() {
  if [[ -f "$DONE_MARKER" ]]; then
    log "Already initialized; exiting"
    exit 0
  fi

  mkdir -p "$STATE_DIR"

  log "Configuring sshd (port 443, key OR TOTP, no root)"
  install -d -m 0755 /etc/ssh/sshd_config.d
  cat >/etc/ssh/sshd_config.d/40-paropal.conf <<EOF
Port 443
PermitRootLogin no
AllowUsers ${USER_NAME}

PasswordAuthentication no
PubkeyAuthentication yes
KbdInteractiveAuthentication yes
UsePAM yes
PAMServiceName paropal-sshd

# Space-separated lists mean OR between lists.
AuthenticationMethods publickey keyboard-interactive

LoginGraceTime 15s
MaxAuthTries 2
MaxSessions 1
MaxStartups 5:20:20
ClientAliveInterval 120
ClientAliveCountMax 2

X11Forwarding no
EOF

  log "Configuring PAM service (keyboard-interactive TOTP only)"
  cat >/etc/pam.d/paropal-sshd <<'EOF'
#%PAM-1.0

# Only used for keyboard-interactive. Publickey auth bypasses PAM auth modules, but still runs account/session.
auth required pam_google_authenticator.so

# Respect /etc/nologin.
account required pam_nologin.so

@include common-account
@include common-session
EOF

  log "Configuring UFW (only allow 443/tcp)"
  ufw --force reset
  ufw default deny incoming
  ufw default allow outgoing
  ufw allow 443/tcp
  ufw --force enable

  log "Configuring fail2ban for sshd on 443"
  install -d -m 0755 /etc/fail2ban/jail.d
  cat >/etc/fail2ban/jail.d/sshd.conf <<'EOF'
[sshd]
enabled = true
mode = aggressive
port = 443
maxretry = 3
findtime = 10m
bantime = 12h
EOF

  sshd -t
  systemctl restart ssh 2>/dev/null || systemctl restart sshd
  systemctl enable --now fail2ban

  touch "$DONE_MARKER"
  log "Base init complete"
}

main "$@"
