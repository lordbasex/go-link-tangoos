#!/bin/bash
# cluster-deploy.sh <paquete.deb>: instala un build de TangoOS en los 3 nodos del clúster de
# prueba (VMs 112–114). Mismas variables que cluster-ssh.sh.
set -e
deb=$1
[ -f "$deb" ] || { echo "uso: $0 dist/tangoos_<versión>_amd64.deb" >&2; exit 2; }
K=${SSH_KEY:-$HOME/.ssh/id_ed25519}
NET=${CLUSTER_NET:-172.17.0.}
BASE=${CLUSTER_BASE:-47}
for n in 1 2 3; do
	ip=$NET$((BASE + n))
	scp -q -i "$K" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -- "$deb" "root@$ip:/tmp/tangoos.deb" &
done
wait
"$(dirname "$0")/cluster-ssh.sh" all 'DEBIAN_FRONTEND=noninteractive apt-get install -y -q --reinstall --no-install-recommends --allow-downgrades /tmp/tangoos.deb >/dev/null 2>&1 && echo "$(dpkg-query -W -f="\${Version}" tangoos) tangod=$(systemctl is-active tangod)"'
