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

## A local folder

The folder must already exist: winterflow never creates it, so a USB disk
that isn't mounted fails the test instead of filling up the system disk.
It also must not be inside winterflow's own data folder.

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

Then add `/mnt/backup/winterflow` as a local storage. If the test says the
folder isn't writable by the user winterflow runs as, fix the ownership as
above.

### A folder outside /mnt and /media

To use another folder, say `/srv/backups`, allow it with a systemd drop-in:

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
`hetzner`:

```yaml
services:
  litestream:
    image: litestream/litestream:0.3
    command: replicate /data/app.db s3://${WF_STORAGE_HETZNER_BUCKET}/${WF_STORAGE_HETZNER_PREFIX}app.db
    environment:
      LITESTREAM_ACCESS_KEY_ID: ${WF_STORAGE_HETZNER_ACCESS_KEY_ID}
      LITESTREAM_SECRET_ACCESS_KEY: ${WF_STORAGE_HETZNER_SECRET_ACCESS_KEY}
      LITESTREAM_ENDPOINT: ${WF_STORAGE_HETZNER_ENDPOINT}
    volumes:
      - data:/data
```

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
