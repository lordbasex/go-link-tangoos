#!/bin/bash
# cluster-ssh.sh <1|2|3|all> <comando>: SSH root a los nodos del clúster de prueba (VMs 112–114).
#   CLUSTER_NET (por defecto 172.17.0.) y CLUSTER_BASE (47: n1 = .48). SSH_KEY como el resto de tests/.
K=${SSH_KEY:-$HOME/.ssh/id_ed25519}
NET=${CLUSTER_NET:-172.17.0.}
BASE=${CLUSTER_BASE:-47}
which=$1
shift
case $which in all) ns="1 2 3" ;; *) ns=$which ;; esac
rc=0
for n in $ns; do
	ip=$NET$((BASE + n))
	[ "$which" = all ] && echo "== n$n ($ip)"
	ssh -i "$K" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=20 -o BatchMode=yes -- "root@$ip" "$@" || rc=$?
done
exit $rc
