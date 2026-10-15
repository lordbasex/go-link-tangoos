#!/bin/bash
# cluster-ui.sh: capturas de la app Clúster contra el clúster real armado (web/e2e/cluster2.spec.ts,
# recorrido "clúster real"). Lee la contraseña inicial de admin del nodo 1 por SSH (no la imprime).
#   SSH_KEY, CLUSTER_NODE (por defecto 172.17.0.48)
set -e
cd "$(dirname "$0")/../web/e2e"
node=${CLUSTER_NODE:-172.17.0.48}
pw=$(ssh -i "${SSH_KEY:-$HOME/.ssh/id_ed25519}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -- "root@$node" cat /var/lib/tango/initial-admin-password)
CLUSTER_NODE=$node TANGO_E2E_ADMIN_PASSWORD="$pw" npx playwright test cluster2.spec.ts -g "real" --timeout 180000 --reporter=line "$@"
