#!/bin/sh
# tangoctl como admin contra el NAS de laboratorio. La contraseña se lee por SSH del NAS y nunca se imprime.
#   VM (172.17.0.44 por defecto), SSH_KEY (llave SSH root del laboratorio, por defecto ~/.ssh/id_ed25519)
VM=${VM:-172.17.0.44}
SSH_KEY=${SSH_KEY:-$HOME/.ssh/id_ed25519}
DIR=$(cd "$(dirname "$0")/.." && pwd)
PW=$(ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@"$VM" cat /var/lib/tango/initial-admin-password)
exec "$DIR/dist/tangoctl" -url "wss://$VM/signal?v=1" -insecure -user admin -password "$PW" "$@"
