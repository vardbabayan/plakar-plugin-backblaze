# plakar-plugin-backblaze

A [Plakar](https://www.plakar.io) **storage connector** for [Backblaze B2](https://www.backblaze.com) via S3-compatible API. It lets a Kloset repository live in a B2 bucket, so
`plakar backup` and `plakar restore` work against B2 the same way they work against a local
directory. Registers the `b2://` protocol.

## Requirements

- Go 1.24+
- `plakar` v1.1.x on your `PATH`
- A Backblaze B2 account and a bucket

## Install and Build

```sh
make install

make build
```

This builds `b2Storage`, packages it with `plakar pkg create`, and installs it with
`plakar pkg add`. Verify:

```sh
plakar pkg show | grep backblaze
```

## Configuration

The connector takes its settings from a Plakar store definition

Copy [`b2.ini`](b2.ini), fill in your values, and import it:

```sh
plakar store import -config b2.ini
```

`b2.ini`:

```ini
[byte]
location          = b2://s3.us-west-004.backblazeb2.com/YOUR-BUCKET-NAME
access_key        = YOUR_B2_KEY_ID
secret_access_key = YOUR_B2_APPLICATION_KEY
region            = us-west-004
use_tls           = true
```

The section name is the **store alias**, so this repository is addressed as `@byte`

Equivalently, without a file:

```sh
plakar store add byte b2://s3.us-west-004.backblazeb2.com/YOUR-BUCKET \
    access_key=... secret_access_key=... region=us-west-004
```

### Options

| Key | Required | Default | Meaning |
|---|---|---|---|
| `location` | yes | — | `b2://<endpoint>/<bucket>[/<prefix>]` |
| `access_key` | yes | — | B2 `keyID` |
| `secret_access_key` | yes | — | B2 `applicationKey` |
| `region` | no | derived from endpoint | e.g. `us-west-004` |
| `endpoint` | no | host from `location` | overrides the location host |
| `root` | no | `/` | bucket path prefix |
| `use_tls` | no | `true` | HTTPS to the endpoint |
| `tls_insecure_no_verify` | no | `false` | skip certificate verification (testing only) |
| `use_path_style` | no | `false` | address the bucket as a path element instead of a virtual host; needed for bucket names that are not TLS-safe DNS labels |

## Usage

```sh
plakar at @byte create              # initialise the repository
plakar at @byte backup ~/Documents  # back up
plakar at @byte ls                  # list snapshots
plakar at @byte check               # verify integrity
plakar at @byte restore -to /tmp/restored <snapshot-id>
```

## Repository layout in the bucket

The same shape the official `fs` and `s3` connectors use, so a Kloset is recognisable
across backends:

```
<prefix>/CONFIG
<prefix>/packfiles/<xx>/<64-hex-mac>
<prefix>/states/<xx>/<64-hex-mac>
<prefix>/locks/<64-hex-mac>
```
