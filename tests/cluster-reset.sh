#!/bin/bash
# cluster-reset.sh: deja los 3 nodos del clúster de prueba (VMs 112–114) como recién
# instalados con GlusterFS: sin volúmenes del clúster, sin pool, sin LV de bricks, sin TLS
# ni certificados. Conserva volume1, el grupo gpool y volume2. Solo para el laboratorio.
here=$(dirname "$0")
NET=${CLUSTER_NET:-172.17.0.}
BASE=${CLUSTER_BASE:-47}
n2=$NET$((BASE + 2))
n3=$NET$((BASE + 3))
"$here/cluster-ssh.sh" all '
for m in /cluster/*; do [ -d "$m" ] && systemctl stop "cluster-$(basename "$m").mount" 2>/dev/null; done
for v in $(gluster volume list 2>/dev/null | grep -v "^No volumes"); do
  gluster --mode=script volume stop "$v" force >/dev/null 2>&1
  gluster --mode=script volume delete "$v" >/dev/null 2>&1
done
for s in $(gluster snapshot list 2>/dev/null | grep -v "^No snapshots"); do gluster --mode=script snapshot delete "$s" >/dev/null 2>&1; done
rm -f /etc/systemd/system/cluster-*.mount
true'
"$here/cluster-ssh.sh" 1 "gluster --mode=script peer detach $n2 >/dev/null 2>&1; gluster --mode=script peer detach $n3 >/dev/null 2>&1; true"
"$here/cluster-ssh.sh" all '
for m in $(findmnt -rn -o TARGET | grep -E "^/cluster/|^/run/gluster/snaps/|^/srv/gluster/|^/mnt/nfsclu"); do umount -l "$m"; done
pkill -f "[/]usr/sbin/glusterfs .*--volfile-id" 2>/dev/null
for u in /etc/systemd/system/srv-gluster-*.mount; do [ -f "$u" ] && systemctl disable --now "$(basename "$u")" >/dev/null 2>&1; rm -f "$u"; done
systemctl daemon-reload
lvs --noheadings -o lv_name,origin gpool 2>/dev/null | awk "\$2 ~ /^gb_/ {print \$1}" | xargs -r -I{} lvremove -qq -y gpool/{}
lvs --noheadings -o lv_name gpool 2>/dev/null | awk "\$1 ~ /^gb_/ && \$1 != \"gb_thin\" {print \$1}" | xargs -r -I{} lvremove -qq -y gpool/{}
lvremove -qq -y gpool/gb_thin 2>/dev/null
rm -rf /srv/gluster /volume1/@gluster/* /volume2/@gluster/* /var/lib/tango/cluster /etc/ssl/glusterfs.pem /etc/ssl/glusterfs.key /etc/ssl/glusterfs.ca /var/lib/glusterd/secure-access
rm -f /etc/exports.d/tango.exports; exportfs -ra 2>/dev/null
systemctl reset-failed glusterd 2>/dev/null; systemctl restart glusterd tangod
echo "$(hostname): $(gluster pool list | tail -n +2 | wc -l) nodo(s), $(gluster volume list 2>&1 | head -1), lvs: $(lvs --noheadings -o lv_name gpool 2>/dev/null | xargs)"'
