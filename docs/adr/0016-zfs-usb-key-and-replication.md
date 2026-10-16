# ADR 0016 — ZFS: USB key and replication with zfs send

- Status: Accepted
- Context: ZFS volumes (ADR 0013) only unlocked with the passphrase, and there was no way to keep a copy of a
  volume on another pool or another NAS. LUKS volumes already open with a key file on a USB stick (ADR 0011).

## USB key

- Decision:
  1. ZFS has **one wrapping key per encryption root**: there are no slots as in LUKS. Switching the volume to a raw key
     would make the passphrase a TangoOS-only secret (no `zfs load-key` with it on another machine). So the passphrase
     stays the ZFS key, and the stick holds an **X25519 private key** (32 bytes, `tangoos-keys/zfs-<hash of the public
     key>.key`). The volume stores the passphrase **sealed** for the public key (NaCl anonymous box) in the user
     property `tangoos:keyseal`, and the stick's filesystem UUID in `tangoos:keydev`.
  2. Unlock with the stick: the agent mounts it read-only for an instant (the same transient unit as LUKS), opens the
     seal and runs `zfs load-key` with the passphrase on stdin. The passphrase exists only in the agent's memory.
  3. Changing the passphrase re-seals it for the same public key, without the stick. Removing the key clears both
     properties.
  4. The key file name depends on the public key, not on the dataset, so the stick still opens the volume after it is
     replicated and promoted on another NAS.
- Consequences: whoever has the stick **and** the disks learns the passphrase (with LUKS they would get a slot, not the
  passphrase): users should not reuse it elsewhere. To revoke a lost stick: remove the key and then change the
  passphrase (an old copy of the seal only reveals the old passphrase). Seal and file are tested in
  `internal/zfs/keyseal_test.go`.

## Replication

- Decision:
  1. **Per dataset, raw.** A run sends each dataset of the volume (the volume, `shares`, `apps`, each folder and app)
     as its own `zfs send -w` stream, not a `-R` replication stream. Raw: an encrypted volume is never decrypted outside
     the source and the target needs no key. Per dataset: so a cut transfer resumes with that dataset's
     `receive_resume_token` (`zfs send -t`); `-R` streams cannot be resumed as a whole.
  2. **History.** A dataset the target lacks gets its oldest snapshot whole and then `-I` up to the run's snapshot, so the
     scheduled snapshots (Previous Versions) travel too. Later runs send `-I <newest common>`. After a run the source
     keeps only the task's last `tgrepl-<task>-<time>` snapshot (the next base) and the target **mirrors** the source's
     snapshots (what the scheduler prunes here is pruned there). A folder deleted at the source stays on the target.
  3. **Where.** `<pool>/tgrepl/<origin>/<vol>`, under a parent with `canmount=off mountpoint=none readonly=on`: a replica
     is never mounted, shared or listed as a volume. Streams carry no properties (no `-p`), so the source's mountpoints
     never collide. "Convert into a volume" renames it into the pool and applies the TangoOS layout.
  4. **Who moves the bytes.** The agent has no network (`PrivateNetwork=yes`): `zfs.repl.send|recv` hand tangod one end
     of a pipe by SCM_RIGHTS, and `zfs.repl.wait` reports the exit status (polled, since agent ops time out at 30 s).
     For another pool of the same NAS tangod copies pipe to pipe.
  5. **Between NAS** over TLS 1.3 on TCP 24020, listening only while "Receive replicas" is on. Each NAS has its own key
     and self-signed certificate; the source pins the target's key and the target accepts only linked client keys.
     Linking reuses the cluster pattern: the target shows a one-use code (15 min) and its fingerprint, the source shows
     the fingerprint it sees, and after the user confirms it proves the code with an HMAC bound to both certificates (a
     relay in the middle cannot reuse it). No dependency on GlusterFS or the cluster CA: two TangoOS that are not in a
     cluster can replicate.
  6. Schedule per task (hourly to weekly, or manual); a failed run is retried after 15 minutes.
- Consequences: replicas are mirrors, not independent backups with their own retention. A replica that diverged
  (no common snapshot) has to be deleted on the target to start over. Received datasets may arrive as their own
  encryption roots; promoting with the passphrase loads each and makes it inherit the volume's key again.
