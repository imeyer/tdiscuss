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

1. Be a [tailscale](https://tailscale.com) user
1. Have an [auth key](https://login.tailscale.com/admin/settings/keys) created for the last step in this list.
1. Grant yourself admin in your tailnet policy file — see [Admin access](#admin-access). Doing this before the first launch means you are an admin on your first request; there is no automatic admin.
1. Set up a PostgreSQL database version 17+ (see [README.database-setup.md](README.database-setup.md))
1. `psql < sqlc/schema.sql`
2. `DATABASE_URL=<valid dsn> TS_AUTHKEY=<key from step 2> make run-binary`

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
