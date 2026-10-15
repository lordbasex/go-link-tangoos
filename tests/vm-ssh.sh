#!/bin/sh
# Corre un comando en el NAS de laboratorio por SSH (lo usa web/e2e/connect.spec.ts para comparar sha256).
VM=${VM:-172.17.0.44}
SSH_KEY=${SSH_KEY:-$HOME/.ssh/id_ed25519}
exec ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@"$VM" "$@"
