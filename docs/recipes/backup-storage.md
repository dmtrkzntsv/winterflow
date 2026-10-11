# Backup storage: a folder or an S3 bucket

A **storage** is a place on a server that backups and apps can write to: a
local folder (a USB disk, an NFS or CIFS share) or an S3-compatible bucket
(AWS S3, Cloudflare R2, Backblaze B2, Hetzner Object Storage, MinIO).
Storages are added per server under **Settings → Storages**.

Winterflow tests every storage before it saves it: it writes a small
`.winterflow-probe-…` file and deletes it again. A storage that fails the
test is not saved.

## An S3-compatible bucket

| Field | Example | Notes |
|---|---|---|
| Endpoint | `https://fsn1.your-objectstorage.com` | `https://` only; plain `http://` is accepted for a loopback or private address (MinIO on your LAN) |
| Region | `fsn1`, `auto` (R2), `us-east-1` | may be empty when the provider ignores it |
| Bucket | `my-backups` | must exist already |
| Prefix | `winterflow/` | optional; everything winterflow writes stays under it |
| Access key / secret | | the secret is encrypted in your browser; only the server can read it |
| Path-style | on for MinIO | leave off for providers that use `bucket.endpoint` hostnames |

To edit a storage without re-entering the secret, leave the secret empty.
A storage's name and type can't change: remove it and add it again. Adding a
storage under a name the server already has is refused.

## A local folder

The folder must already exist: winterflow never creates it. It also must
not be inside winterflow's own data folder.

**Register a folder inside the mounted disk, never the mount point
itself.** Create it after mounting, e.g. `/mnt/usb/winterflow` for a disk
mounted on `/mnt/usb`. When the disk isn't mounted (after a reboot, or
unplugged), the mount point is still there as an empty folder on the system
disk, and every check winterflow makes can accept it: backups and apps
would then quietly fill the system disk. A folder created on the disk disappears
with it, so the test, deploys and writes fail instead.

The installer's systemd unit runs winterflow with `ProtectSystem=strict`:
the service can write only its data folder, the Docker socket, and
everything under **`/mnt`** and **`/media`**. Mount your disks or shares
there:

```sh
sudo mkdir -p /mnt/backup
sudo mount /dev/sdb1 /mnt/backup          # or an /etc/fstab entry
sudo mkdir -p /mnt/backup/winterflow
sudo chown winterflow:winterflow /mnt/backup/winterflow
```

The last two lines run with the disk mounted, so `winterflow` is a folder
on the disk. Then add `/mnt/backup/winterflow` (not `/mnt/backup`) as a
local storage. If the check says the folder isn't writable by the user
winterflow runs as, fix the ownership as above.

### A folder outside /mnt and /media

To use another folder, say `/srv/backups/winterflow` on a disk mounted on
`/srv/backups`, allow the mount with a systemd drop-in:

```sh
sudo systemctl edit winterflow
```

```ini
[Service]
ReadWritePaths=/srv/backups
```

Save, then restart the service (`sudo systemctl restart winterflow`) and
test the storage again. The folder still has to be writable by the
`winterflow` user.

## Using a storage in an app

An app's compose file can reference any storage on its server by name with
`${WF_STORAGE_<NAME>_<FIELD>}`, where `<NAME>` is the storage name in upper
case with `-` replaced by `_`:

| Field | Applies to |
|---|---|
| `TYPE`, `PREFIX` | every storage (`PREFIX` is empty for a local folder) |
| `PATH` | local folders |
| `ENDPOINT`, `REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY` | S3 buckets |

For example, Litestream replicating a SQLite database to a storage named
`hetzner`. Litestream reads its replica settings from a config file, and
expands `${VAR}` in it from its own environment, so the compose file passes
the storage variables in with `environment:` and mounts the config, an app
file next to the compose file:

```yaml
# compose.yml
services:
  app:
    image: ghcr.io/example/app
    volumes:
      - data:/data
  litestream:
    image: litestream/litestream:0.3
    command: replicate
    environment:
      WF_STORAGE_HETZNER_ENDPOINT: ${WF_STORAGE_HETZNER_ENDPOINT}
      WF_STORAGE_HETZNER_REGION: ${WF_STORAGE_HETZNER_REGION}
      WF_STORAGE_HETZNER_BUCKET: ${WF_STORAGE_HETZNER_BUCKET}
      WF_STORAGE_HETZNER_PREFIX: ${WF_STORAGE_HETZNER_PREFIX}
      LITESTREAM_ACCESS_KEY_ID: ${WF_STORAGE_HETZNER_ACCESS_KEY_ID}
      LITESTREAM_SECRET_ACCESS_KEY: ${WF_STORAGE_HETZNER_SECRET_ACCESS_KEY}
    volumes:
      - data:/data
      - ./litestream.yml:/etc/litestream.yml:ro

volumes:
  data:
```

```yaml
# litestream.yml
dbs:
  - path: /data/app.db
    replicas:
      - type: s3
        endpoint: ${WF_STORAGE_HETZNER_ENDPOINT}
        region: ${WF_STORAGE_HETZNER_REGION}
        bucket: ${WF_STORAGE_HETZNER_BUCKET}
        path: ${WF_STORAGE_HETZNER_PREFIX}app.db
```

Litestream itself only reads the keys from the environment
(`LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`); the endpoint
has to be in the config, or it talks to AWS. Add `force-path-style: true`
to the replica for a path-style storage (MinIO).

- Only the variables the compose file mentions are passed to the app, so an
  app never receives credentials for a storage it doesn't use.
- Saving an app that references a storage the server doesn't have fails
  with the variable's name.
- The variables show up read-only in the app's **Variables** panel. You
  can't define your own variables starting with `WF_STORAGE_` or
  `WF_BACKUP_`.
- Saving a storage redeploys the running apps that reference it, so they
  pick up the new values. A storage can't be removed while an app
  references it.

### A local folder as a bind mount

Mount a local storage with the long volume syntax and
`create_host_path: false`:

```yaml
services:
  app:
    image: ghcr.io/example/app
    volumes:
      - type: bind
        source: ${WF_STORAGE_BACKUP_DISK_PATH}
        target: /backup
        bind:
          create_host_path: false
```

With the short syntax (`- ${WF_STORAGE_BACKUP_DISK_PATH}:/backup`) Docker
creates a missing folder, empty, on the system disk: when the USB disk
isn't mounted (after a reboot, or unplugged), the app's writes then fill
the system disk instead of failing. Winterflow refuses a deploy while the
folder of a storage the compose file references is missing, but Docker
also restarts containers on its own after a reboot, possibly before the
disk is mounted — `create_host_path: false` makes that start fail instead.
