# tdiscuss

Discussion board for your tailnet

## Building

1. Install [bazelisk](https://github.com/bazelbuild/bazelisk)
1. `make` will run tests and build

### Platforms

Prebuilt release binaries are published for Linux (amd64, arm64) and Apple
Silicon macOS (arm64).

**Intel macOS (amd64) is still supported** — it just isn't part of the release
matrix, because the macOS build needs cgo (Tailscale's `certstore` links Apple
frameworks) and GitHub has retired its free Intel macOS runners. To use tdiscuss
on an Intel Mac, build from source on that machine (`make`). If someone is kind
enough to donate an Intel macOS runner, we'll happily add it back to releases.

## Running for development

`make help` lists every target. The short version:

1. Be a [tailscale](https://tailscale.com) user, with `podman` or `docker`
   installed for the dev database.
1. Grant yourself admin in your tailnet policy file — see [Admin access](#admin-access). Doing this before the first launch means you are an admin on your first request; there is no automatic admin.
1. Give `make dev` a way to register the node on your tailnet — either an
   [auth key](https://login.tailscale.com/admin/settings/keys) in `TS_AUTHKEY`,
   or an OAuth client (see below) so it registers itself on every run.
1. `make dev`

`make dev` does the whole dev environment: starts a PostgreSQL container and
loads the schema, registers the node on your tailnet, and runs the binary as
**`discuss-dev`** with `-debug` on. tsnet state lives under `.dev/discuss-dev/`,
never your production state in `~/.config`. Override anything on the command
line: `make dev DEV_HOSTNAME=discuss-ian DEV_DB_PORT=5544`.

### The dev database

`make dev` brings up `postgres:17-alpine` as the container `tdiscuss-dev-db`,
bound to **127.0.0.1:5433** — not 5432, so it cannot collide with a Postgres
you already run — with trust auth, a named volume for its data, and
`sqlc/schema.sql` loaded on first start. That gives
`postgres://discuss@127.0.0.1:5433/discuss`.

It is safe to run repeatedly: an existing container is started rather than
recreated, and the schema is only loaded when `board_data` is absent, so your
test threads survive a restart.

| Target | |
|---|---|
| `make dev-db` | start it and load the schema, without running the app |
| `make dev-db-psql` | a `psql` shell on it |
| `make dev-db-stop` | stop the container, keep the data |
| `make dev-db-reset` | destroy container **and data**, then rebuild empty |

**To use your own Postgres instead, set `DATABASE_URL`** — in the env file
below, or per-run (`DATABASE_URL=... make dev`). When it is set, `make dev`
touches no containers at all, and you are responsible for the schema
(`psql < sqlc/schema.sql`; see
[README.database-setup.md](README.database-setup.md)). `make clean-db` is the
older host-Postgres reset and is unrelated to the container.

### Automatic tailnet registration

If `TS_AUTHKEY` is set, `make dev` uses that key as-is and none of this
applies. Otherwise it runs `./dev-authkey.sh`, which mints a fresh key through
the Tailscale API. Setting that up is three steps, and **the order matters** —
the tag has to exist in your policy file before the OAuth client can be granted
it.

**1. Declare the dev tag in your [policy
file](https://login.tailscale.com/admin/acls/file).** `tagOwners` is what makes
the tag selectable in step 2; the grants are what let you reach the dev board
and be an admin on it:

```json
{
  "tagOwners": {
    "tag:discuss-dev": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["tag:discuss-dev"],
      "ip":  ["80", "443", "9090"]
    },
    {
      "src": ["you@example.com"],
      "dst": ["tag:discuss-dev"],
      "app": {
        "github.com/imeyer/tdiscuss/cap/board": [{"role": "admin"}]
      }
    }
  ]
}
```

Save the policy file before moving on. (Port 9090 is the debug/metrics
listener — handy in dev, drop it if you don't want it. See [Tailnet
ports](#tailnet-ports) and [Admin access](#admin-access).)

**2. Create an OAuth client** at [Settings › Keys ›
OAuth clients](https://login.tailscale.com/admin/settings/oauth) → **Generate
OAuth client…**. In the dialog:

- Give it a description, e.g. `tdiscuss dev`.
- Find the **Keys** scope group and check **Write** on **Auth Keys**. Read
  access is not enough, and this is the only scope the script needs — leave the
  rest unchecked.
- Checking that box reveals a **tags** selector directly underneath it. Pick
  `tag:discuss-dev`. This selector only lists tags already in `tagOwners`, so
  if it is empty or the tag is missing, step 1 hasn't been saved yet. A key can
  only ever be minted for a tag the client holds here.
- **Generate client.**

The client ID and secret are shown once. The secret starts with `tskey-client-`
and is *not* an auth key — don't put it in `TS_AUTHKEY`.

**3. Put the client where the dev run can find it.** The client ID is not a
secret; the secret is, and unlike an auth key it does not expire, so treat it
like a password. `make dev-env` creates a file for it, mode `0600`, outside the
repo:

```sh
make dev-env          # creates ~/.config/tdiscuss/dev.env
$EDITOR ~/.config/tdiscuss/dev.env
```

Fill in the two lines it left blank:

```sh
TS_API_CLIENT_ID=k123ABCDEF
TS_API_CLIENT_SECRET=tskey-client-...
```

That's it — `make dev` loads the file, and so does `./dev-authkey.sh` when you
run it directly. Anything else you want in the dev environment can go in there
too, `DATABASE_URL` included. Values already in your environment win, so
`DATABASE_URL=... make dev` still overrides the file for one run.

Both the Makefile and the script **refuse to read the file if anyone but you
can**, since a `0644` secret is a secret shared with every account on the
machine. If you ever see that error, `chmod 600 ~/.config/tdiscuss/dev.env`.
`DEV_ENV_FILE` (make) and `TDISCUSS_DEV_ENV` (the script) move the file
elsewhere.

If you would rather no secret sat in plaintext at all, put it in your keyring
and give the script a command that reads it back instead:

```sh
secret-tool store --label='tdiscuss dev OAuth' service tdiscuss-dev key client-secret

# in the env file, or your shell rc - no secret material in either line
TS_API_CLIENT_ID=k123ABCDEF
TS_API_CLIENT_SECRET_CMD='secret-tool lookup service tdiscuss-dev key client-secret'
```

Any command that prints the secret on stdout works — `op read
op://Private/tdiscuss-dev/credential`, `pass show tailscale/tdiscuss-dev`,
`gpg -dq ~/.secrets/tdiscuss-dev.gpg`. `TS_API_CLIENT_ID_CMD` does the same for
the ID.

#### Keeping the client secret safe

What the secret can do, if it leaks, is bounded by the tag: it mints keys that
can only create devices tagged `tag:discuss-dev`, and those devices can only
reach what your policy file lets that tag reach. So write the dev tag's grants
as narrowly as you would any other — `dst: tag:discuss-dev` rules let people
reach the board without giving the board's node any access outbound. Use a
separate tag from production (`tag:tdiscuss`), one OAuth client per machine,
named for that machine, and delete the client when you stop using it.

Where to put it, best to worst:

1. **A keyring or password manager, read on demand** via
   `TS_API_CLIENT_SECRET_CMD`. The value exists only inside the one
   `dev-authkey.sh` process, for the length of one API call. Nothing on disk in
   the clear, nothing in your shell history, nothing to commit, and no
   unrelated process inherits it.
2. **`~/.config/tdiscuss/dev.env`, mode `0600`** — what `make dev-env` sets up.
   Plaintext at rest, readable only by you, out of the working tree, and loaded
   only by the dev run rather than by every shell you open.
3. **`export TS_API_CLIENT_SECRET=...` in your shell rc.** Works, but now every
   process you launch from that shell carries the secret in its environment,
   and it sits in a file you may well keep in a dotfiles repo.

Never put it in a `.envrc` committed to this repo, and never pass it as a
command-line argument to anything: on Linux `/proc/<pid>/cmdline` is readable
by any user on the machine, so an argument is a broadcast. `dev-authkey.sh`
feeds both the secret and the resulting API token to `curl` on stdin
(`curl --config -`) for exactly that reason, and unsets the credential
variables so `curl` does not inherit them.

The key the script mints is much less sensitive: single-use, ephemeral, and
expires in an hour (`DEV_KEY_EXPIRY`). It reaches the binary through the
environment rather than argv. `make dev-authkey` prints one to your terminal,
so it lands in scrollback — fine for a smoke test, but that is a live key until
it is used or expires.

If the API call fails, `dev-authkey.sh` prints Tailscale's own message. The one
you are most likely to hit first:

```
auth key request: HTTP 400
  requested tags [tag:discuss-dev] are invalid or not permitted
```

The tag has to be in **both** places — `tagOwners` in the policy file *and*
granted to the OAuth client — and **a client's tags are fixed when you generate
it**. So a client created before the tag existed in the policy file holds no
tags, and no amount of policy-file editing afterwards will fix it. The OAuth
clients page lists each client's tags in its row; if that list is empty or
lacks the tag, add the tag to the policy file, then delete the client and
generate a new one. `make dev DEV_TAGS=tag:something` uses a tag the client
already holds instead.

#### If the node comes up as `discuss-dev-1`

```
msg="tsnet running" certDomains="[discuss-dev-1.cougar-monitor.ts.net]"
msg="error expanding SNI name"
msg="listening on https://"
```

A device named `discuss-dev` already exists in the tailnet, so control gave the
new node the next free name. Check with `tailscale status | grep discuss` —
it is usually an *offline* node from an old run still holding the name.

The empty `https://` in the log is a consequence, not a TLS failure:
`expandSNIName` asks for the FQDN of the `-hostname` you passed, and
`ExpandSNIName` only matches a cert domain whose next character is a `.`, so
`discuss-dev` does not match `discuss-dev-1.cougar-monitor.ts.net`. HTTPS
itself is served by `tsnet`'s own `ListenTLS`, which uses the node's real name,
so the board is reachable at `https://discuss-dev-1.cougar-monitor.ts.net`
while that log line stays blank.

To get the name back:

1. Delete the stale device in the [admin
   console](https://login.tailscale.com/admin/machines).
2. `make dev-clean`, so tsnet re-registers instead of reusing the identity in
   its state directory.

Step 2 matters. `Authkey is set; but state is Starting. Ignoring authkey` in
the log means tsnet found existing state and kept the identity it already has —
including the `-1` name — so deleting the device alone changes nothing. (
`TSNET_FORCE_LOGIN=1` forces the key to be used instead.)

`403 calling actor does not have enough permissions` means the client is
missing the Keys › Auth Keys write scope, and `401 invalid client credentials`
means the ID or secret is wrong or the client has been deleted.

## Running for production

### Prerequisites

- PostgreSQL 17+ configured per [README.database-setup.md](README.database-setup.md)
- A [Tailscale auth key](https://login.tailscale.com/admin/settings/keys), ideally
  carrying a tag such as `tag:tdiscuss`
- An admin grant in your tailnet policy file, added **before the first launch** —
  see [Admin access](#admin-access). There is no automatic admin.
- The `tdiscuss` binary (build with `make` or download a release)

<details>
<summary><strong>Linux (systemd)</strong></summary>

```bash
# Create service account
getent group tdiscuss >/dev/null || groupadd -r tdiscuss
getent passwd tdiscuss >/dev/null || useradd -r -g tdiscuss -d /var/lib/tdiscuss -s /sbin/nologin -c "tdiscuss service account" tdiscuss

# Create directories
install -d -m 0750 -o tdiscuss -g tdiscuss /var/lib/tdiscuss

# Install binary
install -D -m 0755 tdiscuss /usr/bin/tdiscuss

# Install config and service
install -D -m 0640 -o root -g tdiscuss contrib/rpm/tdiscuss.sysconfig /etc/sysconfig/tdiscuss
install -D -m 0644 contrib/tdiscuss.service /usr/lib/systemd/system/tdiscuss.service
```

Edit `/etc/sysconfig/tdiscuss`:

```bash
TS_AUTHKEY=tskey-auth-xxx                 # Your Tailscale auth key
TSNET_HOSTNAME=discuss                    # Your tailnet hostname
DATABASE_URL=postgres://tdiscuss@localhost/tdiscuss?sslmode=disable
OPTIONS="-data-location=/var/lib/tdiscuss"
```

Set up `.pgpass` for the tdiscuss user per [README.database-setup.md](README.database-setup.md#authentication-using-pgpass).

Start the service:

```bash
systemctl daemon-reload
systemctl enable --now tdiscuss
```

</details>

<details>
<summary><strong>macOS (launchd)</strong></summary>

```bash
# Create directories
sudo mkdir -p /usr/local/var/lib/tdiscuss /usr/local/var/log
sudo chown $(whoami) /usr/local/var/lib/tdiscuss

# Install binary
sudo cp tdiscuss /usr/local/bin/tdiscuss

# Create launchd plist
cat > ~/Library/LaunchAgents/com.tdiscuss.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.tdiscuss</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/tdiscuss</string>
        <string>-data-location=/usr/local/var/lib/tdiscuss</string>
        <!-- Add -otlp to enable OpenTelemetry export -->
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>TS_AUTHKEY</key>
        <string>tskey-auth-xxx</string>
        <key>TSNET_HOSTNAME</key>
        <string>discuss</string>
        <key>DATABASE_URL</key>
        <string>postgres://tdiscuss@localhost/tdiscuss?sslmode=disable</string>
        <!-- OpenTelemetry Configuration (requires -otlp flag above) -->
        <!-- <key>OTEL_EXPORTER_OTLP_ENDPOINT</key> -->
        <!-- <string>http://localhost:4318</string> -->
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/usr/local/var/log/tdiscuss.log</string>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/tdiscuss.log</string>
</dict>
</plist>
EOF
```

Edit the plist to set your `TS_AUTHKEY` and `DATABASE_URL`. Set up `~/.pgpass` per [README.database-setup.md](README.database-setup.md#authentication-using-pgpass).

For OpenTelemetry export, add `-otlp` to ProgramArguments and uncomment the OTEL environment variables. See `contrib/rpm/tdiscuss.sysconfig` for all available OTLP options.

Start the service:

```bash
launchctl load ~/Library/LaunchAgents/com.tdiscuss.plist
```

To stop: `launchctl unload ~/Library/LaunchAgents/com.tdiscuss.plist`

</details>

## Who can sign in

Every request on the application ports is identified with the Tailscale
LocalAPI's `WhoIs`, which resolves **the owner of the calling node**. tdiscuss
accepts a request only when that owner is a real person in your tailnet:

- **Untagged nodes owned by a tailnet user** are accepted. The owner's login
  name is the member identity, and a member row is created on first sight.
- **Tagged nodes are rejected with 401.** A tagged node has no human owner, so
  `WhoIs` reports a synthetic account that is identical for *every* tagged node
  in the tailnet. Accepting it would file all of your automation under one
  shared member — and on an empty board, make that shared member an admin.
  Automation that needs to reach tdiscuss should use port 9090.
- **Nodes shared in from another tailnet are rejected** by default, because
  their login name is issued by a foreign identity provider. Pass
  `-allow-shared-nodes` (add it to `OPTIONS` in
  `/etc/sysconfig/tdiscuss`) to let them join the board.

Rejections are logged at warn level with the node name and, for a tagged node,
its tags.

## Admin access

**There is no automatic admin.** tdiscuss does not promote the first member to
sign in — privilege is never inferred from signup order. Admin comes from one
of two places:

1. a **capability grant** in your tailnet policy file (recommended), or
2. the `member.is_admin` column, which an existing admin can set from the
   [Members section of the admin page](#promoting-members-from-the-board), or
   which you can set with SQL.

The first admin has to come from source 1 or from SQL — a board with no admin
has nobody who can use the admin page. Configuring a grant before you deploy
avoids that entirely.

### Before you deploy

Do this *before* the first launch and admin works on your very first request.
There is no chicken-and-egg step and nothing to run afterwards.

**1. Tag the node.** When you create the [auth
key](https://login.tailscale.com/admin/settings/keys), attach a tag to it —
`tag:tdiscuss` below. The node picks the tag up when it registers, which gives
you a stable name to write policy against. (Tagging the *tdiscuss* node is
unrelated to tdiscuss rejecting tagged *callers*; see [Who can sign
in](#who-can-sign-in).) You will need a `tagOwners` entry for the tag before
the key can carry it.

**2. Add the grant.** In the [policy
file](https://login.tailscale.com/admin/acls/file), a complete working
configuration looks like this:

```json
{
  "tagOwners": {
    "tag:tdiscuss": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["autogroup:member"],
      "dst": ["tag:tdiscuss"],
      "ip":  ["80", "443"]
    },
    {
      "src": ["group:board-admins"],
      "dst": ["tag:tdiscuss"],
      "app": {
        "github.com/imeyer/tdiscuss/cap/board": [{"role": "admin"}]
      }
    }
  ],
  "groups": {
    "group:board-admins": ["you@example.com"]
  }
}
```

The two grants do different jobs and you need both:

- the **`ip`** grant lets members reach the board at all. If your tailnet
  still has the default allow-all policy this is already covered, but once you
  write any grants you have to include it.
- the **`app`** grant is what makes someone an admin. `src` accepts users,
  groups, or SCIM groups, so admin membership follows the same review as the
  rest of your policy file — with the policy file's history as the audit trail.

**3. Deploy.** Start tdiscuss with that auth key and open the board. You are
an admin immediately.

### If the node is untagged

An untagged node has no tag to name in `dst`, and a MagicDNS name is not valid
there. Define a host alias for its tailnet IP (`tailscale ip -4` on the
tdiscuss host) and use that:

```json
{
  "hosts": {
    "tdiscuss": "100.101.102.103"
  },
  "grants": [
    {
      "src": ["group:board-admins"],
      "dst": ["tdiscuss"],
      "app": {
        "github.com/imeyer/tdiscuss/cap/board": [{"role": "admin"}]
      }
    }
  ]
}
```

Tagging is still the better option: the alias breaks if the node's IP changes.

### Verifying a grant

Grants take effect as soon as the policy file change reaches the node — there
is nothing to restart, and no cache to clear, because tdiscuss reads the grant
on every request.

To confirm one is landing, run this **on the tdiscuss host**, which is the only
node that computes capabilities for traffic addressed to it:

```bash
tailscale whois <the member's tailnet IP>
```

The output has a `Capabilities:` section listing the grants that peer holds for
this node. If it is missing or empty, `dst` does not match the tdiscuss node —
that is the usual mistake.

### Roles, and what happens when a grant is wrong

Only `role: "admin"` is recognized. Any other role is ignored, so a policy file
can name a role that a future version understands without breaking an older
binary. A malformed grant is logged at warn level and grants nothing, rather
than failing requests — one policy-file typo should not take the board down.

### Promoting members from the board

The **Members** section of `/admin` lists every member and lets an admin
promote or demote any other member. That writes the `member.is_admin` column —
the same thing the SQL below does — so it needs no policy file access and is
the easiest way to hand out admin day to day.

Two things it deliberately does not do:

- **You cannot change your own admin status.** Demoting yourself would drop
  the privilege mid-session and can leave a board with no database admin at
  all. Another admin has to demote you.
- **It cannot show or revoke policy-file grants.** Capability grants arrive
  per-request from the member who holds them, so the board only ever sees the
  grants of whoever is currently signed in — it has no way to ask "does this
  other member have a grant?". A member listed as not an admin may still be
  one by grant, and removing admin here will not take that away.

Every promotion and demotion is logged at info level with both the acting and
the target member id.

### Blocking and unblocking members

The same table has **Block** / **Unblock** buttons. A blocked member is refused
on every route — the auth middleware returns 404 before any handler runs — and
their posts stay on the board rather than being deleted.

Unlike admin, blocking is board state only, so this page controls it
completely: a blocked member stays blocked no matter what grants they hold.

Two rules constrain it:

- **You cannot block yourself.** A blocked member is refused on every route
  including this page, so it would be an immediate and unrecoverable lockout
  for that account.
- **You cannot block another admin.** Blocking outranks every other privilege,
  so this would otherwise let one admin lock another out of the board with no
  way back short of a SQL update. Remove their admin first, then block them.
  This checks the `is_admin` column — the only admin status the board can see
  for another member — so a member who is an admin *solely* through a
  capability grant is not protected by this rule.

Unblocking is always allowed, including for an admin, so anyone blocked before
these rules existed still has a way back. Blocked status changes are logged at
info level with the acting and target member id.

### Granting admin without a policy file or the board

To set the column directly:

```sql
UPDATE member SET is_admin = true WHERE email = 'you@example.com';
```

The member has to have signed in at least once for the row to exist. This is
the escape hatch for bootstrapping when you can't edit your policy file.

### Revoking admin

**Admin is the union of both sources.** That lets you adopt grants without
stranding an existing admin, but it means **revoking admin requires clearing
both** — removing the grant alone leaves a database admin in place, and
removing admin on the board does not touch the grant. Logs and traces carry
`is_admin_by_grant` so you can tell which source applied for a given member's
own session.

## Tailnet ports

tdiscuss listens on three tailnet ports:

| Port | Purpose |
| ---- | ------- |
| 80   | Application over HTTP |
| 443  | Application over HTTPS (requires [HTTPS certificates](https://tailscale.com/kb/1153/enabling-https) enabled for your tailnet) |
| 9090 | Prometheus metrics at `/_/metrics`, plus `/health` |

The application ports authenticate every request against the peer's tailnet
identity. Port 9090 does not, because a scraper has no user identity to check
— access to it is governed by your tailnet policy file instead. Grant it
explicitly, e.g.:

```json
{
  "action": "accept",
  "src":    ["tag:prom"],
  "dst":    ["discuss:9090"]
}
```

If you don't grant anything, no peer can reach the metrics endpoint.

## Issues

Issues building or running? General questions? [File an issue](https://github.com/imeyer/tdiscuss/issues/new)!
